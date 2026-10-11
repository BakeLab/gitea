// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// ActionScope identifies exactly one repository, organization, or account scope.
type ActionScope struct {
	Repository, Organization string
	Account                  bool
}

// ActionSecret describes a secret without containing its value.
type ActionSecret struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

func secretFrom(secret *gitea.Secret) ActionSecret {
	return ActionSecret{Name: secret.Name, Description: secret.Description, CreatedAt: timestamp(secret.Created)}
}

// ActionVariable contains one non-secret workflow variable.
type ActionVariable struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

func variableFrom(variable *gitea.ActionsVariable) ActionVariable {
	return ActionVariable{Name: variable.Name, Value: variable.Data, Description: variable.Description}
}

// ActionSecrets manages scoped Actions secrets without returning secret data.
type ActionSecrets struct {
	Client *transport.Client
	Scope  ActionScope
}

// List returns secret metadata only.
func (s ActionSecrets) List(ctx context.Context, limit, page int) ([]ActionSecret, error) {
	if s.Scope.Account {
		return nil, &CapabilityError{Feature: "account secret listing", Reason: "Gitea exposes account secret set/delete operations only"}
	}
	options := listOptions(limit, page)
	secrets, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Secret, *gitea.Response, error) {
		if s.Scope.Organization != "" {
			return c.Actions.ListOrgSecrets(ctx, s.Scope.Organization, gitea.ListOrgActionsSecretOption{ListOptions: options})
		}
		owner, repo, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Actions.ListRepoSecrets(ctx, owner, repo, gitea.ListRepoActionsSecretOption{ListOptions: options})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(secrets, secretFrom), nil
}

// Set creates or replaces a secret; the API's PUT operation defines upsert semantics.
func (s ActionSecrets) Set(ctx context.Context, name, value, description string) (ActionSecret, error) {
	option := gitea.CreateOrUpdateSecretOption{Data: value, Description: description}
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		switch {
		case s.Scope.Account:
			return c.Actions.CreateUserSecret(ctx, name, option)
		case s.Scope.Organization != "":
			return c.Actions.CreateOrgSecret(ctx, s.Scope.Organization, name, option)
		}
		owner, repo, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, err
		}
		return c.Actions.CreateRepoSecret(ctx, owner, repo, name, option)
	})
	return ActionSecret{Name: name, Description: description}, err
}

// Delete removes only the named secret in the selected scope.
func (s ActionSecrets) Delete(ctx context.Context, name string) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		switch {
		case s.Scope.Account:
			return c.Actions.DeleteUserSecret(ctx, name)
		case s.Scope.Organization != "":
			return c.Actions.DeleteOrgSecret(ctx, s.Scope.Organization, name)
		}
		owner, repo, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, err
		}
		// TODO(sdk): DeleteRepoSecret does not escape the secret name.
		return c.Actions.DeleteRepoSecret(ctx, owner, repo, url.PathEscape(name))
	})
}

// ActionVariables manages scoped non-secret Actions variables.
type ActionVariables struct {
	Client *transport.Client
	Scope  ActionScope
}

// repoVariables addresses repository variables, or one of them when name is set, for raw requests.
// TODO(sdk): RepoActionsVariable.Description, a description on Create/UpdateRepoVariable, and name
// escaping in the repository variable methods.
func (s ActionVariables) repoVariables(name string) string {
	endpoint := "repos/" + s.Scope.Repository + "/actions/variables"
	if name != "" {
		endpoint += "/" + url.PathEscape(name)
	}
	return endpoint
}

// List returns a non-nil page of variables.
func (s ActionVariables) List(ctx context.Context, limit, page int) ([]ActionVariable, error) {
	var variables []*gitea.ActionsVariable
	var err error
	switch {
	case s.Scope.Account:
		variables, _, err = call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.ActionsVariable, *gitea.Response, error) {
			return c.Actions.ListUserVariables(ctx, listOptions(limit, page))
		})
	case s.Scope.Organization != "":
		variables, _, err = call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.ActionsVariable, *gitea.Response, error) {
			return c.Actions.ListOrgVariables(ctx, s.Scope.Organization, gitea.ListOrgActionsVariableOption{ListOptions: listOptions(limit, page)})
		})
	default:
		err = getJSON(ctx, s.Client, fmt.Sprintf("%s?limit=%d&page=%d", s.repoVariables(""), limit, page), &variables)
	}
	if err != nil {
		return nil, err
	}
	return convertAll(variables, variableFrom), nil
}

// View retrieves one variable.
func (s ActionVariables) View(ctx context.Context, name string) (ActionVariable, error) {
	variable := new(gitea.ActionsVariable)
	var err error
	switch {
	case s.Scope.Account:
		variable, _, err = call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsVariable, *gitea.Response, error) {
			return c.Actions.GetUserVariable(ctx, name)
		})
	case s.Scope.Organization != "":
		variable, _, err = call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsVariable, *gitea.Response, error) {
			return c.Actions.GetOrgVariable(ctx, s.Scope.Organization, name)
		})
	default:
		err = getJSON(ctx, s.Client, s.repoVariables(name), variable)
	}
	if err != nil {
		return ActionVariable{}, err
	}
	return variableFrom(variable), nil
}

// Set chooses create or update from an authoritative read.
func (s ActionVariables) Set(ctx context.Context, name, value, description string) (ActionVariable, error) {
	exists := true
	if _, err := s.View(ctx, name); err != nil {
		if apiError, ok := errors.AsType[*transport.APIError](err); !ok || apiError.Status != http.StatusNotFound {
			return ActionVariable{}, err
		}
		exists = false
	}
	create := gitea.CreateActionsVariableOption{Value: value, Description: description}
	update := gitea.UpdateActionsVariableOption{Name: name, Value: value, Description: description}
	var err error
	switch {
	case s.Scope.Account:
		err = do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			if exists {
				return c.Actions.UpdateUserVariable(ctx, name, update)
			}
			return c.Actions.CreateUserVariable(ctx, name, create)
		})
	case s.Scope.Organization != "":
		err = do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			if exists {
				return c.Actions.UpdateOrgVariable(ctx, s.Scope.Organization, name, update)
			}
			return c.Actions.CreateOrgVariable(ctx, s.Scope.Organization, name, create)
		})
	default:
		method := http.MethodPost
		if exists {
			method = http.MethodPut
		}
		err = sendJSON(ctx, s.Client, method, s.repoVariables(name), update, nil)
	}
	return ActionVariable{Name: name, Value: value, Description: description}, err
}

// Delete removes only the named variable in the selected scope.
func (s ActionVariables) Delete(ctx context.Context, name string) error {
	switch {
	case s.Scope.Account:
		return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			return c.Actions.DeleteUserVariable(ctx, name)
		})
	case s.Scope.Organization != "":
		return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			return c.Actions.DeleteOrgVariable(ctx, s.Scope.Organization, name)
		})
	}
	return sendJSON(ctx, s.Client, http.MethodDelete, s.repoVariables(name), nil, nil)
}
