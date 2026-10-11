// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Team is an organization's access group and its permission policy.
type Team struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	Organization    string            `json:"organization"`
	Permission      string            `json:"permission"`
	CanCreateRepo   bool              `json:"can_create_repo"`
	AllRepositories bool              `json:"all_repositories"`
	Units           map[string]string `json:"units"`
}

func teamFrom(team *gitea.Team) Team {
	result := Team{
		ID: team.ID, Name: team.Name, Description: team.Description, Permission: string(team.Permission),
		CanCreateRepo: team.CanCreateOrgRepo, AllRepositories: team.IncludesAllRepositories, Units: team.UnitsMap,
	}
	if result.Units == nil {
		result.Units = map[string]string{}
	}
	if team.Organization != nil {
		result.Organization = organizationFrom(team.Organization).Name
	}
	return result
}

// TeamOptions preserves omitted fields when changing a team.
type TeamOptions struct {
	Name            *string           `json:"name,omitempty"`
	Description     *string           `json:"description,omitempty"`
	Permission      *string           `json:"permission,omitempty"`
	CanCreateRepo   *bool             `json:"can_create_org_repo,omitempty"`
	AllRepositories *bool             `json:"includes_all_repositories,omitempty"`
	Units           map[string]string `json:"units_map,omitempty"`
}

// Teams implements team, member and repository access operations.
type Teams struct{ Client *transport.Client }

// List lists teams in one organization.
func (s Teams) List(ctx context.Context, org string, limit, page int) ([]Team, error) {
	teams, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Team, *gitea.Response, error) {
		return c.Organizations.ListOrgTeams(ctx, org, gitea.ListTeamsOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(teams, teamFrom), nil
}

// ByName indexes every team of an organization by lower-case name, which Gitea keeps unique.
func (s Teams) ByName(ctx context.Context, org string) (map[string]Team, error) {
	teams, err := listAll(func(page int) ([]Team, error) { return s.List(ctx, org, 50, page) })
	if err != nil {
		return nil, err
	}
	result := make(map[string]Team, len(teams))
	for _, team := range teams {
		result[strings.ToLower(team.Name)] = team
	}
	return result, nil
}

// View retrieves a team and its current permissions.
func (s Teams) View(ctx context.Context, id int64) (Team, error) {
	team, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Team, *gitea.Response, error) {
		return c.Organizations.GetTeam(ctx, id)
	})
	if err != nil {
		return Team{}, err
	}
	return teamFrom(team), nil
}

// Create creates an organization team with explicit initial permissions.
func (s Teams) Create(ctx context.Context, org string, opts TeamOptions) (Team, error) {
	team, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Team, *gitea.Response, error) {
		return c.Organizations.CreateTeam(ctx, org, gitea.CreateTeamOption{
			Name: deref(opts.Name), Description: deref(opts.Description), Permission: gitea.AccessMode(deref(opts.Permission)),
			CanCreateOrgRepo: deref(opts.CanCreateRepo), IncludesAllRepositories: deref(opts.AllRepositories), UnitsMap: opts.Units,
		})
	})
	if err != nil {
		return Team{}, err
	}
	return teamFrom(team), nil
}

// Edit preserves unit permissions when changing access to all repositories.
func (s Teams) Edit(ctx context.Context, id int64, opts TeamOptions) (Team, error) {
	if opts.AllRepositories != nil && opts.Permission == nil {
		current, err := s.View(ctx, id)
		if err != nil {
			return Team{}, err
		}
		if current.Permission == "owner" {
			return Team{}, &CapabilityError{Feature: "owner-team repository policy editing", Reason: "owner teams already manage all organization repositories"}
		}
		opts.Permission = &current.Permission
		if opts.Units == nil {
			opts.Units = current.Units
		}
	}
	var team gitea.Team
	// TODO(sdk): EditTeamOption requires a name of at most 30 characters and a permission, and EditTeam should return the team.
	if err := sendJSON(ctx, s.Client, http.MethodPatch, fmt.Sprintf("teams/%d", id), opts, &team); err != nil {
		return Team{}, err
	}
	return teamFrom(&team), nil
}

// Delete deletes a team and its associated access grants.
func (s Teams) Delete(ctx context.Context, id int64) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Organizations.DeleteTeam(ctx, id)
	})
}

// Members lists members of a team.
func (s Teams) Members(ctx context.Context, id int64, limit, page int) ([]OrganizationMember, error) {
	users, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.User, *gitea.Response, error) {
		return c.Organizations.ListTeamMembers(ctx, id, gitea.ListTeamMembersOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(users, memberFrom), nil
}

// SetMember adds or removes one team membership.
func (s Teams) SetMember(ctx context.Context, id int64, username string, add bool) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		if add {
			return c.Organizations.AddTeamMember(ctx, id, username)
		}
		return c.Organizations.RemoveTeamMember(ctx, id, username)
	})
}

// Repositories lists repositories accessible to a team.
func (s Teams) Repositories(ctx context.Context, id int64, limit, page int) ([]Repository, error) {
	repos, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Repository, *gitea.Response, error) {
		return c.Organizations.ListTeamRepositories(ctx, id, gitea.ListTeamRepositoriesOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(repos, repositoryFrom), nil
}

// SetRepository adds or removes team access to a repository.
func (s Teams) SetRepository(ctx context.Context, id int64, repository string, add bool) error {
	return repoDo(ctx, s.Client, repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		if add {
			return c.Organizations.AddTeamRepository(ctx, id, owner, name)
		}
		return c.Organizations.RemoveTeamRepository(ctx, id, owner, name)
	})
}
