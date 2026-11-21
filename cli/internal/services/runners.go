// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Runner is an Actions runner.
type Runner struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Busy      bool     `json:"busy"`
	Disabled  bool     `json:"disabled"`
	Ephemeral bool     `json:"ephemeral"`
	Labels    []string `json:"labels"`
}

func runnerFrom(runner *gitea.ActionsRunner) Runner {
	labels := make([]string, 0, len(runner.Labels))
	for _, label := range runner.Labels {
		if label != nil {
			labels = append(labels, label.Name)
		}
	}
	return Runner{ID: runner.ID, Name: runner.Name, Status: runner.Status, Busy: runner.Busy, Disabled: runner.Disabled, Ephemeral: runner.Ephemeral, Labels: labels}
}

// RegistrationToken lets a runner register itself with the scope.
type RegistrationToken struct {
	Token string `json:"token"`
}

// Runners manages the Actions runners of one scope.
type Runners struct {
	Client *transport.Client
	Scope  OwnerScope
}

// List lists a page of runners, only the disabled or only the enabled ones when disabled is set.
func (s Runners) List(ctx context.Context, limit, page int, disabled *bool) (Page[Runner], error) {
	runners, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]Runner, http.Header, error) {
		response, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsRunnersResponse, *gitea.Response, error) {
			opts := gitea.ListActionsRunnersOptions{ListOptions: listOptions(limit, page), Disabled: disabled}
			switch {
			case s.Scope.Global:
				return c.Actions.ListGlobalRunners(ctx, opts)
			case s.Scope.Account:
				return c.Actions.ListUserRunners(ctx, opts)
			case s.Scope.Organization != "":
				return c.Actions.ListOrgRunners(ctx, s.Scope.Organization, opts)
			}
			owner, name, err := splitRepository(s.Scope.Repository)
			if err != nil {
				return nil, nil, err
			}
			return c.Actions.ListRepoRunners(ctx, owner, name, opts)
		})
		if err != nil {
			return nil, nil, err
		}
		return convertAll(response.Runners, runnerFrom), bodyCounted(resp, response.TotalCount, len(response.Runners)), nil
	})
	return runners, s.Scope.listError(err)
}

// itemError names runner id in the failure of a request about it.
func (s Runners) itemError(ctx context.Context, err error, id int64) error {
	return s.Scope.itemError(ctx, s.Client, err, "runner", id)
}

// View returns one runner.
func (s Runners) View(ctx context.Context, id int64) (Runner, error) {
	runner, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsRunner, *gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Actions.GetGlobalRunner(ctx, id)
		case s.Scope.Account:
			return c.Actions.GetUserRunner(ctx, id)
		case s.Scope.Organization != "":
			return c.Actions.GetOrgRunner(ctx, s.Scope.Organization, id)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Actions.GetRepoRunner(ctx, owner, name, id)
	})
	if err != nil {
		return Runner{}, s.itemError(ctx, err, id)
	}
	return runnerFrom(runner), nil
}

// SetDisabled stops a runner from taking jobs, or lets it take them again, and returns it as it is then.
func (s Runners) SetDisabled(ctx context.Context, id int64, disabled bool) (Runner, error) {
	option := gitea.EditActionsRunnerOption{Disabled: &disabled}
	runner, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsRunner, *gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Actions.UpdateGlobalRunner(ctx, id, option)
		case s.Scope.Account:
			return c.Actions.UpdateUserRunner(ctx, id, option)
		case s.Scope.Organization != "":
			return c.Actions.UpdateOrgRunner(ctx, s.Scope.Organization, id, option)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Actions.UpdateRepoRunner(ctx, owner, name, id, option)
	})
	if err != nil {
		return Runner{}, s.itemError(ctx, err, id)
	}
	return runnerFrom(runner), nil
}

// Delete unregisters a runner.
func (s Runners) Delete(ctx context.Context, id int64) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Actions.DeleteGlobalRunner(ctx, id)
		case s.Scope.Account:
			return c.Actions.DeleteUserRunner(ctx, id)
		case s.Scope.Organization != "":
			return c.Actions.DeleteOrgRunner(ctx, s.Scope.Organization, id)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, err
		}
		return c.Actions.DeleteRepoRunner(ctx, owner, name, id)
	})
	if err != nil {
		return s.itemError(ctx, err, id)
	}
	return nil
}

// RegistrationToken returns the token that registers new runners with the scope, which Gitea
// creates when the scope has none.
func (s Runners) RegistrationToken(ctx context.Context) (RegistrationToken, error) {
	token, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.RegistrationToken, *gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Actions.CreateGlobalRunnerRegistrationToken(ctx)
		case s.Scope.Account:
			return c.Actions.CreateUserRunnerRegistrationToken(ctx)
		case s.Scope.Organization != "":
			return c.Actions.CreateOrgRunnerRegistrationToken(ctx, s.Scope.Organization)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Actions.CreateRepoRunnerRegistrationToken(ctx, owner, name)
	})
	if err != nil {
		return RegistrationToken{}, s.Scope.listError(err)
	}
	return RegistrationToken{Token: token.Token}, nil
}
