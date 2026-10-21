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
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Organization string `json:"organization"`
	// Permission is the team-wide level Gitea reports: owner, admin, read, write, or none for a
	// team that grants its access per unit.
	Permission string `json:"permission"`
	// Access is the team's effective access, as EffectiveAccess summarizes it.
	Access          string            `json:"access"`
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

// EffectiveAccess summarizes what the team may do in its repositories: owner or admin; the one
// level it grants, such as write, followed by the units when it does not grant all of them, as in
// "write (code, issues, pulls)"; none; or custom when it grants different levels.
func (t Team) EffectiveAccess() string {
	if t.Permission == "owner" || t.Permission == "admin" {
		return t.Permission
	}
	var level string
	var granted []string
	for _, unit := range TeamUnits {
		access := t.UnitLevel(unit)
		if access == "none" {
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

// UnitLevel is the access the team has to unit: the higher of the unit's own level and the team
// permission, which Gitea grants on every unit, capped at the levels the unit allows.
func (t Team) UnitLevel(unit string) string {
	levels := UnitAccess(unit)
	level := "none"
	for _, candidate := range []string{t.Units[unit], t.Permission} {
		if candidate == "write" && !slices.Contains(levels, "write") {
			candidate = "read"
		}
		if slices.Index(levels, candidate) > slices.Index(levels, level) {
			level = candidate
		}
	}
	return level
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
	result.Access = result.EffectiveAccess()
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
	teams, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Team, *gitea.Response, error) {
		return c.Organizations.ListOrgTeams(ctx, org, gitea.ListTeamsOptions{ListOptions: list})
	})
	return inOrganization(convertPage(teams, teamFrom), org), Classify(err, "organization", strconv.Quote(org), "")
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
	teams, err := listEvery(50, func(limit, page int) (Page[Team], error) { return s.List(ctx, org, limit, page) })
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
	listed, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Team, *gitea.Response, error) {
		return c.Organizations.SearchOrgTeams(ctx, org, &gitea.SearchTeamsOptions{ListOptions: list, Query: query})
	})
	teams := convertAll(listed, teamFrom)
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
		if err != nil || teams[i].AllRepositories {
			sizes[i] = TeamSize{Members: members}
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
	if IsNotFound(err) {
		return Team{}, Classify(err, "organization", strconv.Quote(org), "")
	}
	if err != nil {
		return Team{}, Classify(err, "team", strconv.Quote(deref(opts.Name)), org)
	}
	created := teamFrom(team)
	created.Organization = org
	return created, nil
}

// Edit merges Units into the current unit policy unless ReplaceUnits is set, and preserves unit
// permissions when changing access to all repositories. Gitea replaces the whole policy on edit.
func (s Teams) Edit(ctx context.Context, id int64, opts TeamOptions) (Team, error) {
	repositoryPolicy := opts.AllRepositories != nil && opts.Permission == nil
	changesPermission := opts.Permission != nil
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
			if len(units) == 0 && (current.Permission == "read" || current.Permission == "write") {
				// The team grants its permission to every unit and has no unit policy yet.
				units = UnitPolicy(current.Permission)
			}
			maps.Copy(units, opts.Units)
			opts.Units = units
		}
	}
	// Gitea 28 keeps an admin team admin when one request changes its permission and sets units_map,
	// so change the permission alone first and then apply the units.
	if changesPermission && opts.Units != nil {
		first := TeamOptions{Name: opts.Name, Permission: opts.Permission}
		if first.Name == nil {
			current, err := s.View(ctx, id)
			if err != nil {
				return Team{}, err
			}
			first.Name = &current.Name
		}
		// TODO(sdk): EditTeamOption requires a name of at most 30 characters and a permission, and EditTeam should return the team.
		if err := sendJSON(ctx, s.Client, http.MethodPatch, fmt.Sprintf("teams/%d", id), first, nil); err != nil {
			return Team{}, err
		}
	}
	var team gitea.Team
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

// TeamMember identifies a team member without exposing private profile fields.
type TeamMember struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	FullName string `json:"full_name"`
}

func teamMemberFrom(user *gitea.User) TeamMember {
	return TeamMember{ID: user.ID, Username: user.UserName, FullName: user.FullName}
}

// Members lists members of a team.
func (s Teams) Members(ctx context.Context, id int64, limit, page int) (Page[TeamMember], error) {
	users, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.User, *gitea.Response, error) {
		return c.Organizations.ListTeamMembers(ctx, id, gitea.ListTeamMembersOptions{ListOptions: list})
	})
	return convertPage(users, teamMemberFrom), err
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
	repos, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Repository, *gitea.Response, error) {
		return c.Organizations.ListTeamRepositories(ctx, id, gitea.ListTeamRepositoriesOptions{ListOptions: list})
	})
	return convertPage(repos, repositoryFrom), err
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
