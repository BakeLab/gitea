// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
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

// ActionSecret describes a secret without containing its value.
type ActionSecret struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// SetActionSecret names a secret that was just set; Gitea does not return when it was created.
type SetActionSecret struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ActionValueName is the name Gitea stores a secret or variable under: names are upper-cased.
func ActionValueName(name string) string { return strings.ToUpper(name) }

// classifyValue classifies err as a missing secret or variable only when the 404 says so: Gitea
// answers a plain "not found" when the repository or organization itself does not exist.
func classifyValue(err error, kind, name string, scope ActionScope) error {
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusNotFound && !strings.Contains(apiErr.Message, kind) {
		return classifyScope(err, scope)
	}
	return Classify(err, kind, ActionValueName(name), scope.String())
}

// classifyScope reports a 404 as the repository or organization of scope not being found.
func classifyScope(err error, scope ActionScope) error {
	switch {
	case scope.Account:
		return err
	case scope.Organization != "":
		return Classify(err, "organization", scope.Organization, "")
	}
	return Classify(err, "repository", scope.Repository, "")
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

func repoVariableFrom(variable *gitea.RepoActionsVariable) ActionVariable {
	return ActionVariable{Name: variable.Name, Value: variable.Value, Description: variable.Description}
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
	return convertPage(window, secretFrom), classifyScope(err, s.Scope)
}

// Set creates or replaces a secret; the API's PUT operation defines upsert semantics. The result
// names the secret as Gitea stores it.
func (s ActionSecrets) Set(ctx context.Context, name, value, description string) (SetActionSecret, error) {
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
	return SetActionSecret{Name: ActionValueName(name), Description: description}, classifyValue(err, "secret", name, s.Scope)
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
		return c.Actions.DeleteRepoSecret(ctx, owner, repo, name)
	})
	return classifyValue(err, "secret", name, s.Scope)
}

// ActionVariables manages scoped non-secret Actions variables.
type ActionVariables struct {
	Client *transport.Client
	Scope  ActionScope
}

// ListPage lists a page of variables, with what the server reports about the other pages.
func (s ActionVariables) ListPage(ctx context.Context, limit, page int) (Page[ActionVariable], error) {
	if s.Scope.Account || s.Scope.Organization != "" {
		window, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ActionsVariable, *gitea.Response, error) {
			if s.Scope.Account {
				return c.Actions.ListUserVariables(ctx, list)
			}
			return c.Actions.ListOrgVariables(ctx, s.Scope.Organization, gitea.ListOrgActionsVariableOption{ListOptions: list})
		})
		return convertPage(window, variableFrom), classifyScope(err, s.Scope)
	}
	window, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.RepoActionsVariable, *gitea.Response, error) {
		owner, repo, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Actions.ListRepoVariables(ctx, owner, repo, gitea.ListRepoActionsVariableOption{ListOptions: list})
	})
	return convertPage(window, repoVariableFrom), classifyScope(err, s.Scope)
}

// View retrieves one variable.
func (s ActionVariables) View(ctx context.Context, name string) (ActionVariable, error) {
	if !s.Scope.Account && s.Scope.Organization == "" {
		variable, err := repoConvert(ctx, s.Client, s.Scope.Repository, repoVariableFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.RepoActionsVariable, *gitea.Response, error) {
			return c.Actions.GetRepoVariable(ctx, owner, repo, name)
		})
		return variable, classifyValue(err, "variable", name, s.Scope)
	}
	variable, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsVariable, *gitea.Response, error) {
		if s.Scope.Account {
			return c.Actions.GetUserVariable(ctx, name)
		}
		return c.Actions.GetOrgVariable(ctx, s.Scope.Organization, name)
	})
	if err != nil {
		return ActionVariable{}, classifyValue(err, "variable", name, s.Scope)
	}
	return variableFrom(variable), nil
}

// Set chooses create or update from an authoritative read. A nil description keeps the one of an
// existing variable, as Gitea replaces it on every update.
func (s ActionVariables) Set(ctx context.Context, name, value string, newDescription *string) (ActionVariable, error) {
	exists := true
	current, err := s.View(ctx, name)
	if err != nil {
		// A missing repository or organization is no missing variable.
		if notFound, ok := errors.AsType[*NotFoundError](err); !ok || notFound.Kind != "variable" {
			return ActionVariable{}, err
		}
		exists = false
	}
	description := current.Description
	if newDescription != nil {
		description = *newDescription
	}
	create := gitea.CreateActionsVariableOption{Value: value, Description: description}
	update := gitea.UpdateActionsVariableOption{Name: name, Value: value, Description: description}
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
		err = repoDo(ctx, s.Client, s.Scope.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
			if exists {
				return c.Actions.UpdateRepoVariableWithOption(ctx, owner, repo, name, update)
			}
			return c.Actions.CreateRepoVariableWithOption(ctx, owner, repo, name, create)
		})
	}
	return ActionVariable{Name: ActionValueName(name), Value: value, Description: description}, classifyValue(err, "variable", name, s.Scope)
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
		err = repoDo(ctx, s.Client, s.Scope.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
			return c.Actions.DeleteRepoVariable(ctx, owner, repo, name)
		})
	}
	return classifyValue(err, "variable", name, s.Scope)
}
