// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
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

// TeamUnits are the repository units a team grants access to, in the order Gitea shows them.
var TeamUnits = []string{"repo.code", "repo.issues", "repo.pulls", "repo.releases", "repo.wiki", "repo.projects", "repo.packages", "repo.actions"}

// ExternalTeamUnits link to external trackers and wikis, which teams can at most read.
var ExternalTeamUnits = []string{"repo.ext_issues", "repo.ext_wiki"}

// UnitAccess lists the access levels a team can have to unit, or nil for an unknown unit.
func UnitAccess(unit string) []string {
	switch {
	case slices.Contains(TeamUnits, unit):
		return []string{"none", "read", "write"}
	case slices.Contains(ExternalTeamUnits, unit):
		return []string{"none", "read"}
	}
	return nil
}

// UnitPolicy grants access, read or write, to every unit, and read to the external units.
func UnitPolicy(access string) map[string]string {
	units := make(map[string]string, len(TeamUnits)+len(ExternalTeamUnits))
	for _, unit := range TeamUnits {
		units[unit] = access
	}
	for _, unit := range ExternalTeamUnits {
		units[unit] = "read"
	}
	return units
}

// Access summarizes what the team may do in its repositories: owner or admin; the one level it
// grants, such as write, followed by the units when it does not grant all of them, as in
// "write (code, issues, pulls)"; none; or custom when it grants different levels.
func (t Team) Access() string {
	if t.Permission == "owner" || t.Permission == "admin" {
		return t.Permission
	}
	var level string
	var granted []string
	for _, unit := range TeamUnits {
		access := t.Units[unit]
		if access == "" || access == "none" {
			continue
		}
		if level != "" && access != level {
			return "custom"
		}
		level = access
		granted = append(granted, strings.TrimPrefix(unit, "repo."))
	}
	switch {
	case level == "":
		return "none"
	case len(granted) == len(TeamUnits):
		return level
	}
	return level + " (" + strings.Join(granted, ", ") + ")"
}

// Slug names the team as ORG/TEAM, or by name alone when its organization is unknown.
func (t Team) Slug() string {
	if t.Organization == "" {
		return t.Name
	}
	return t.Organization + "/" + t.Name
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
	// ReplaceUnits makes Edit replace the unit policy with Units instead of merging Units into it.
	ReplaceUnits bool `json:"-"`
}

// Teams implements team, member and repository access operations.
type Teams struct{ Client *transport.Client }

// List lists teams in one organization.
func (s Teams) List(ctx context.Context, org string, limit, page int) (Page[Team], error) {
	teams, err := listPage(limit, page, func(limit, page int) ([]*gitea.Team, http.Header, error) {
		teams, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Team, *gitea.Response, error) {
			return c.Organizations.ListOrgTeams(ctx, org, gitea.ListTeamsOptions{ListOptions: listOptions(limit, page)})
		})
		return teams, headerOf(resp), err
	})
	return inOrganization(convertPage(teams, teamFrom), org), err
}

// inOrganization sets the organization of teams that the server listed without it.
func inOrganization(teams Page[Team], org string) Page[Team] {
	for i := range teams.Items {
		teams.Items[i].Organization = org
	}
	return teams
}

// ByName indexes every team of an organization by lower-case name, which Gitea keeps unique.
func (s Teams) ByName(ctx context.Context, org string) (map[string]Team, error) {
	teams, err := listAll(func(page int) ([]Team, error) {
		teams, err := s.List(ctx, org, 50, page)
		return teams.Items, err
	})
	if err != nil {
		return nil, err
	}
	result := make(map[string]Team, len(teams))
	for _, team := range teams {
		result[strings.ToLower(team.Name)] = team
	}
	return result, nil
}

// Find returns the team of org that team addresses by name or ID, as NameOrID matches them.
func (s Teams) Find(ctx context.Context, org, team string) (Team, error) {
	query := team
	if _, err := strconv.ParseInt(team, 10, 64); err == nil {
		query = ""
	}
	teams, err := listAll(func(page int) ([]Team, error) {
		teams, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Team, *gitea.Response, error) {
			return c.Organizations.SearchOrgTeams(ctx, org, &gitea.SearchTeamsOptions{ListOptions: listOptions(50, page), Query: query})
		})
		return convertAll(teams, teamFrom), err
	})
	if err != nil {
		return Team{}, Classify(err, "organization", strconv.Quote(org), "")
	}
	found, err := NameOrID[Team]{Kind: "team", Scope: org, ID: func(t Team) int64 { return t.ID }, Name: func(t Team) string { return t.Name }}.Find(teams, team)
	found.Organization = org
	return found, err
}

// View retrieves a team and its current permissions.
func (s Teams) View(ctx context.Context, id int64) (Team, error) {
	team, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Team, *gitea.Response, error) {
		return c.Organizations.GetTeam(ctx, id)
	})
	if err != nil {
		return Team{}, Classify(err, "team", strconv.FormatInt(id, 10), "")
	}
	return teamFrom(team), nil
}

// TeamSize counts the members and repositories of a team; a count the server omits is nil.
type TeamSize struct {
	Members, Repositories *int
}

// Sizes counts the members and repositories of each team.
func (s Teams) Sizes(ctx context.Context, teams []Team) ([]TeamSize, error) {
	sizes := make([]TeamSize, len(teams))
	err := concurrently(len(teams), func(i int) error {
		id, first := teams[i].ID, listOptions(1, 1)
		members, err := listTotal(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			_, resp, err := c.Organizations.ListTeamMembers(ctx, id, gitea.ListTeamMembersOptions{ListOptions: first})
			return resp, err
		})
		if err != nil {
			return err
		}
		repositories, err := listTotal(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			_, resp, err := c.Organizations.ListTeamRepositories(ctx, id, gitea.ListTeamRepositoriesOptions{ListOptions: first})
			return resp, err
		})
		sizes[i] = TeamSize{Members: members, Repositories: repositories}
		return err
	})
	return sizes, err
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
	created := teamFrom(team)
	created.Organization = org
	return created, nil
}

// Edit merges Units into the current unit policy unless ReplaceUnits is set, and preserves unit
// permissions when changing access to all repositories. Gitea replaces the whole policy on edit.
func (s Teams) Edit(ctx context.Context, id int64, opts TeamOptions) (Team, error) {
	repositoryPolicy := opts.AllRepositories != nil && opts.Permission == nil
	if repositoryPolicy || (opts.Units != nil && !opts.ReplaceUnits) {
		current, err := s.View(ctx, id)
		if err != nil {
			return Team{}, err
		}
		if repositoryPolicy {
			if current.Permission == "owner" {
				return Team{}, &CapabilityError{Feature: "owner-team repository policy editing", Reason: "owner teams already manage all organization repositories"}
			}
			opts.Permission = &current.Permission
		}
		switch {
		case opts.Units == nil:
			opts.Units = current.Units
		case !opts.ReplaceUnits:
			units := maps.Clone(current.Units)
			maps.Copy(units, opts.Units)
			opts.Units = units
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
func (s Teams) Members(ctx context.Context, id int64, limit, page int) (Page[OrganizationMember], error) {
	users, err := listPage(limit, page, func(limit, page int) ([]*gitea.User, http.Header, error) {
		users, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.User, *gitea.Response, error) {
			return c.Organizations.ListTeamMembers(ctx, id, gitea.ListTeamMembersOptions{ListOptions: listOptions(limit, page)})
		})
		return users, headerOf(resp), err
	})
	return convertPage(users, memberFrom), err
}

// IsMember reports whether username belongs to the team.
func (s Teams) IsMember(ctx context.Context, id int64, username string) (bool, error) {
	_, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Organizations.GetTeamMember(ctx, id, username)
	})
	if statusNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// SetMember adds or removes one team membership.
func (s Teams) SetMember(ctx context.Context, id int64, username string, add bool) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		if add {
			return c.Organizations.AddTeamMember(ctx, id, username)
		}
		return c.Organizations.RemoveTeamMember(ctx, id, username)
	})
	return Classify(err, "user", strconv.Quote(username), "")
}

// Repositories lists repositories accessible to a team.
func (s Teams) Repositories(ctx context.Context, id int64, limit, page int) (Page[Repository], error) {
	repos, err := listPage(limit, page, func(limit, page int) ([]*gitea.Repository, http.Header, error) {
		repos, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Repository, *gitea.Response, error) {
			return c.Organizations.ListTeamRepositories(ctx, id, gitea.ListTeamRepositoriesOptions{ListOptions: listOptions(limit, page)})
		})
		return repos, headerOf(resp), err
	})
	return convertPage(repos, repositoryFrom), err
}

// HasRepository reports whether the team has access to the owner/name repository.
func (s Teams) HasRepository(ctx context.Context, id int64, repository string) (bool, error) {
	_, err := repoCall(ctx, s.Client, repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Organizations.GetTeamRepository(ctx, id, owner, name)
	})
	if statusNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// SetRepository adds or removes team access to a repository.
func (s Teams) SetRepository(ctx context.Context, id int64, repository string, add bool) error {
	err := repoDo(ctx, s.Client, repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		if add {
			return c.Organizations.AddTeamRepository(ctx, id, owner, name)
		}
		return c.Organizations.RemoveTeamRepository(ctx, id, owner, name)
	})
	return Classify(err, "repository", repository, "")
}
