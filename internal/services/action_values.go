// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// ActionScope identifies exactly one repository, organization, or account scope.
type ActionScope struct {
	Repository, Organization string
	Account                  bool
}

// String names the scope for people: "alice/project", "organization acme" or "your account".
func (s ActionScope) String() string {
	switch {
	case s.Account:
		return "your account"
	case s.Organization != "":
		return "organization " + s.Organization
	}
	return s.Repository
}

// ActionSecret describes a secret without containing its value. CreatedAt is empty for a secret
// that was just set, as Gitea does not return it.
type ActionSecret struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// ActionValueName is the name Gitea stores a secret or variable under: names are upper-cased.
func ActionValueName(name string) string { return strings.ToUpper(name) }

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

// ListPage lists a page of secrets, with what the server reports about the other pages.
func (s ActionSecrets) ListPage(ctx context.Context, limit, page int) (Page[ActionSecret], error) {
	if s.Scope.Account {
		return Page[ActionSecret]{}, &CapabilityError{Feature: "account secret listing", Reason: "Gitea exposes account secret set/delete operations only"}
	}
	window, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Secret, *gitea.Response, error) {
		if s.Scope.Organization != "" {
			return c.Actions.ListOrgSecrets(ctx, s.Scope.Organization, gitea.ListOrgActionsSecretOption{ListOptions: list})
		}
		owner, repo, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Actions.ListRepoSecrets(ctx, owner, repo, gitea.ListRepoActionsSecretOption{ListOptions: list})
	})
	return convertPage(window, secretFrom), err
}

// Set creates or replaces a secret; the API's PUT operation defines upsert semantics. The result
// names the secret as Gitea stores it.
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
	return ActionSecret{Name: ActionValueName(name), Description: description}, err
}

// Delete removes only the named secret in the selected scope.
func (s ActionSecrets) Delete(ctx context.Context, name string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
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
	return Classify(err, "secret", ActionValueName(name), s.Scope.String())
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

// ListPage lists a page of variables, with what the server reports about the other pages.
func (s ActionVariables) ListPage(ctx context.Context, limit, page int) (Page[ActionVariable], error) {
	var window Page[*gitea.ActionsVariable]
	var err error
	if s.Scope.Account || s.Scope.Organization != "" {
		window, err = callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ActionsVariable, *gitea.Response, error) {
			if s.Scope.Account {
				return c.Actions.ListUserVariables(ctx, list)
			}
			return c.Actions.ListOrgVariables(ctx, s.Scope.Organization, gitea.ListOrgActionsVariableOption{ListOptions: list})
		})
	} else {
		window, err = rawPage[*gitea.ActionsVariable](ctx, s.Client, s.repoVariables(""), nil, limit, page)
	}
	return convertPage(window, variableFrom), err
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
		return ActionVariable{}, Classify(err, "variable", ActionValueName(name), s.Scope.String())
	}
	return variableFrom(variable), nil
}

// Set chooses create or update from an authoritative read.
func (s ActionVariables) Set(ctx context.Context, name, value, description string) (ActionVariable, error) {
	exists := true
	if _, err := s.View(ctx, name); err != nil {
		if !errors.As(err, new(*NotFoundError)) {
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
	return ActionVariable{Name: ActionValueName(name), Value: value, Description: description}, err
}

// Delete removes only the named variable in the selected scope.
func (s ActionVariables) Delete(ctx context.Context, name string) error {
	var err error
	switch {
	case s.Scope.Account:
		err = do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			return c.Actions.DeleteUserVariable(ctx, name)
		})
	case s.Scope.Organization != "":
		err = do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			return c.Actions.DeleteOrgVariable(ctx, s.Scope.Organization, name)
		})
	default:
		err = sendJSON(ctx, s.Client, http.MethodDelete, s.repoVariables(name), nil, nil)
	}
	return Classify(err, "variable", ActionValueName(name), s.Scope.String())
}
