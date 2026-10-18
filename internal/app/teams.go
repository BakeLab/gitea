// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// teamArguments describes the <team> argument in help.
const teamArguments = `A team can be supplied as ORG/TEAM, such as acme/developers, where TEAM is the team's
name or ID, or as its numeric ID alone.`

// teamRef is a parsed <team> argument: ORG/TEAM, or a team ID.
type teamRef struct {
	org, name string
	id        int64
}

func parseTeam(raw string) (teamRef, error) {
	raw = strings.TrimSpace(raw)
	if org, name, scoped := strings.Cut(raw, "/"); scoped {
		if org != "" && name != "" && !strings.Contains(name, "/") {
			return teamRef{org: org, name: name}, nil
		}
	} else if id, err := strconv.ParseInt(strings.TrimPrefix(raw, "#"), 10, 64); err == nil && id > 0 {
		return teamRef{id: id}, nil
	}
	return teamRef{}, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid team %q: use ORG/TEAM, such as acme/developers, or a team ID", raw))
}

func (r teamRef) find(ctx context.Context, service services.Teams) (services.Team, error) {
	if r.org != "" {
		return service.Find(ctx, r.org, r.name)
	}
	return service.View(ctx, r.id)
}

// team resolves the target and the team that raw addresses.
func (a *application) team(cmd *cobra.Command, raw string) (target, services.Teams, services.Team, error) {
	ref, err := parseTeam(raw)
	if err != nil {
		return target{}, services.Teams{}, services.Team{}, err
	}
	t, err := a.resolve(cmd.Context(), false)
	if err != nil {
		return t, services.Teams{}, services.Team{}, err
	}
	service := services.Teams{Client: t.client}
	team, err := ref.find(cmd.Context(), service)
	return t, service, team, err
}

func teamURL(t target, team services.Team) string {
	return webURL(t, "org", team.Organization, "teams", strings.ToLower(team.Name))
}

// completeTeamThen completes a team, then the values that next lists for that team.
func (a *application) completeTeamThen(next func(ctx context.Context, t target, team services.Team) ([]cobra.Completion, error)) cobra.CompletionFunc {
	teams := a.completeTeams()
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return teams(cmd, args, toComplete)
		}
		return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			ref, err := parseTeam(args[0])
			if err != nil {
				return nil, err
			}
			team, err := ref.find(ctx, services.Teams{Client: t.client})
			if err != nil {
				return nil, err
			}
			return next(ctx, t, team)
		})(cmd, args, toComplete)
	}
}

func (a *application) teamCommand() *cobra.Command {
	group := &cobra.Command{Use: "team", Short: "Manage organization teams and access", Long: `Manage the teams of an organization: what they may do in its repositories, who belongs to
them, and which repositories they can reach.`}
	group.AddCommand(a.teamListCommand(), a.teamViewCommand(), a.teamCreateCommand(), a.teamEditCommand(), a.teamDeleteCommand(), a.teamMemberCommand(), a.teamRepoCommand())
	return group
}

func (a *application) teamListCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use: "list <org>", Short: "List organization teams", Args: argsExact(1), ValidArgsFunction: a.completeOrgs(),
		Long: "List the teams of an organization with their access to its repositories and their sizes.",
		Example: `$ gt team list acme
# print the team names
$ gt team list acme --jq '.[].name'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			org := args[0]
			service := services.Teams{Client: target.client}
			teams, err := service.List(cmd.Context(), org, limit, page)
			if err != nil {
				return err
			}
			sizes := map[int64]services.TeamSize{}
			if a.options.json == "" {
				counted, err := service.Sizes(cmd.Context(), teams.Items)
				if err != nil {
					return err
				}
				for i, team := range teams.Items {
					sizes[team.ID] = counted[i]
				}
			}
			return writeTable(a, cmd, teams.Items, listSpec[services.Team]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Name"}, {Header: "Permission", Flex: true}, {Header: "Members"}, {Header: "Repos"}},
				Row: func(team services.Team) []terminal.Cell {
					size, repos := sizes[team.ID], "all"
					if !team.AllRepositories {
						repos = countText(size.Repositories)
					}
					return []terminal.Cell{{Text: strconv.FormatInt(team.ID, 10), Link: teamURL(target, team)}, {Text: team.Name, Bold: true}, {Text: team.Access()}, {Text: countText(size.Members)}, {Text: repos}}
				},
				Context: "teams in " + org, Empty: "no teams in " + org,
			}, servicePage(teams, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return cmd
}

// countText shows a count, or nothing when it is unknown.
func countText(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

// listedNames shows the first names of a listing and how many more there are.
func listedNames[T any](page services.Page[T], name func(T) string) string {
	names := make([]string, len(page.Items))
	for i, item := range page.Items {
		names[i] = name(item)
	}
	text := strings.Join(names, ", ")
	switch {
	case len(names) == 0:
		text = "none"
	case page.Counted && page.Total > len(names):
		text += fmt.Sprintf(" and %d more", page.Total-len(names))
	case page.More:
		text += " and more"
	}
	return text
}

// teamViewNames is how many members and repositories a team view lists.
const teamViewNames = 20

func (a *application) teamViewCommand() *cobra.Command {
	return &cobra.Command{
		Use: "view <team>", Short: "View a team", Args: argsExact(1), ValidArgsFunction: a.completeTeams(),
		Long: "View a team: its access to each repository unit, its members and its repositories.",
		Example: `$ gt team view acme/developers
$ gt team view 2 --json units`,
		Annotations: map[string]string{"gt.arguments": teamArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, team)
			}
			// Gitea shows a public team to non-members of its organization, but not its members.
			members, err := service.Members(cmd.Context(), team.ID, teamViewNames, 1)
			membersHidden := services.IsForbidden(err)
			if err != nil && !membersHidden {
				return err
			}
			repos, err := service.Repositories(cmd.Context(), team.ID, teamViewNames, 1)
			if err != nil {
				return err
			}
			return writeView(a, cmd, team, func(team services.Team) terminal.View {
				var units []string
				for _, unit := range slices.Concat(services.TeamUnits, services.ExternalTeamUnits) {
					if access := team.UnitLevel(unit); access != "none" {
						units = append(units, strings.TrimPrefix(unit, "repo.")+": "+access)
					}
				}
				memberNames := ""
				if !membersHidden {
					memberNames = listedNames(members, func(member services.TeamMember) string { return member.Username })
				}
				repositories := listedNames(repos, func(repo services.Repository) string { return repo.FullName })
				if team.AllRepositories {
					repositories = "all repositories of " + team.Organization
				}
				return terminal.View{
					Title: team.Slug(), ID: fmt.Sprintf("(ID %d)", team.ID), Badges: []terminal.Cell{{Text: team.Access(), Bold: true}},
					Fields: []terminal.Field{
						{Label: "ID", Value: strconv.FormatInt(team.ID, 10), PipedOnly: true},
						{Label: "Access", Value: team.Access(), PipedOnly: true},
						{Label: "Units", Value: strings.Join(units, ", ")},
						{Label: "Members", Value: memberNames},
						{Label: "Repositories", Value: repositories},
						{Label: "Can create repos", Value: yesNo(team.CanCreateRepo)},
					},
					Body: team.Description, Kind: "team", URL: teamURL(target, team),
				}
			})
		},
	}
}

var teamPermissions = []string{"read", "write", "admin"}

// teamFlags are the fields that team create and edit set.
type teamFlags struct {
	name, description, permission string
	units                         map[string]string
	createRepo, allRepos          bool
}

// register adds the flags to cmd. Edit flags have no defaults, so that only what is passed changes.
func (f *teamFlags) register(cmd *cobra.Command, create bool) {
	cmd.Flags().StringVar(&f.name, "name", "", "Team name")
	cmd.Flags().StringVarP(&f.description, "description", "d", "", "Team description")
	initial, permissionUsage := "", "Access to every repository unit; admin also manages repositories"
	if create {
		initial = "read"
	}
	enumFlag(cmd, &f.permission, "permission", "", initial, permissionUsage, teamPermissions...)
	unitUsage := "Unit access, such as repo.code=write,repo.wiki=read; overrides --permission per unit"
	if !create {
		unitUsage = "Unit access to change, such as repo.wiki=write; merged into the current policy"
	}
	cmd.Flags().StringToStringVar(&f.units, "unit", nil, unitUsage)
	_ = cmd.RegisterFlagCompletionFunc("unit", completeTeamUnits)
	cmd.Flags().BoolVar(&f.createRepo, "can-create-repo", false, "Allow creating organization repositories (use =false to revoke)")
	cmd.Flags().BoolVar(&f.allRepos, "all-repositories", false, "Access every organization repository (use =false to revoke)")
}

// unitPolicy validates --unit and returns the unit policy to send: all units at the --permission
// level when it is passed (or, on create, without --unit), with --unit overriding single units.
// Administrator teams get no policy, since they can do everything.
func (f *teamFlags) unitPolicy(cmd *cobra.Command, create bool) (map[string]string, error) {
	changed := cmd.Flags().Changed("unit")
	if changed && len(f.units) == 0 {
		return nil, clierr.Wrap(clierr.Usage, errors.New("--unit needs at least one unit, such as repo.code=write"))
	}
	if changed && f.permission == "admin" {
		return nil, clierr.Wrap(clierr.Usage, errors.New("--unit cannot restrict administrator access"))
	}
	for unit, access := range f.units {
		levels := services.UnitAccess(unit)
		if levels == nil {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("unknown unit %q; use one of %s", unit, strings.Join(slices.Concat(services.TeamUnits, services.ExternalTeamUnits), ", ")))
		}
		if !slices.Contains(levels, access) {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("--unit %s=%s: access must be %s", unit, access, strings.Join(levels, ", ")))
		}
	}
	var policy map[string]string
	switch {
	case f.permission == "admin":
		return nil, nil
	case cmd.Flags().Changed("permission") || (create && !changed):
		policy = services.UnitPolicy(f.permission)
	case changed:
		policy = map[string]string{}
	default:
		return nil, nil
	}
	maps.Copy(policy, f.units)
	return policy, nil
}

func (f *teamFlags) options(cmd *cobra.Command) services.TeamOptions {
	opts := services.TeamOptions{}
	for _, field := range []struct {
		flag   string
		value  *string
		option **string
	}{{"description", &f.description, &opts.Description}, {"permission", &f.permission, &opts.Permission}} {
		if cmd.Flags().Changed(field.flag) {
			*field.option = field.value
		}
	}
	for _, field := range []struct {
		flag   string
		value  *bool
		option **bool
	}{{"can-create-repo", &f.createRepo, &opts.CanCreateRepo}, {"all-repositories", &f.allRepos, &opts.AllRepositories}} {
		if cmd.Flags().Changed(field.flag) {
			*field.option = field.value
		}
	}
	return opts
}

// completeTeamUnits completes --unit as comma-separated UNIT=ACCESS entries.
var completeTeamUnits = commaSeparated(func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if unit, _, found := strings.Cut(toComplete, "="); found {
		levels := services.UnitAccess(unit)
		values := make([]string, len(levels))
		for i, level := range levels {
			values[i] = unit + "=" + level
		}
		return matching(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	units := slices.Concat(services.TeamUnits, services.ExternalTeamUnits)
	for i, unit := range units {
		units[i] = unit + "="
	}
	return matching(units, toComplete), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
})

func (a *application) teamCreateCommand() *cobra.Command {
	var flags teamFlags
	cmd := &cobra.Command{
		Use: "create <org> [<name>]", Short: "Create a team", Args: usageArgs(cobra.RangeArgs(1, 2)),
		Long: `Create a team in an organization. Name it as the second argument, as ORG/NAME, or with --name.

--permission grants read or write access to every repository unit, or admin access. Use --unit
to set single units instead, or to override some of them.`,
		Example: `$ gt team create acme developers --permission write
$ gt team create acme/reviewers --unit repo.code=read,repo.pulls=write
# give the team access to every repository of the organization
$ gt team create acme ops --permission admin --all-repositories`,
		ValidArgsFunction: firstArg(a.completeOrgs()),
		RunE: func(cmd *cobra.Command, args []string) error {
			org, name, scoped := strings.Cut(args[0], "/")
			names := 0
			for _, given := range []bool{scoped, len(args) > 1, cmd.Flags().Changed("name")} {
				if given {
					names++
				}
			}
			if names > 1 {
				return clierr.Wrap(clierr.Usage, errors.New("supply the team name once: as ORG/NAME, as an argument, or with --name"))
			}
			if len(args) > 1 {
				name = args[1]
			}
			if cmd.Flags().Changed("name") {
				name = flags.name
			}
			units, err := flags.unitPolicy(cmd, true)
			if err != nil {
				return err
			}
			if _, err := a.requireInput(cmd, &name, "name", "Team name", nil); err != nil {
				return err
			}
			opts := flags.options(cmd)
			opts.Name, opts.Permission, opts.CanCreateRepo, opts.AllRepositories, opts.Units = &name, &flags.permission, &flags.createRepo, &flags.allRepos, units
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			team, err := (services.Teams{Client: target.client}).Create(cmd.Context(), org, opts)
			if err != nil {
				return err
			}
			a.done(cmd, "Created team %s (ID %d) with %s access", team.Slug(), team.ID, team.Access())
			return a.created(cmd, team, teamURL(target, team))
		},
	}
	flags.register(cmd, true)
	return cmd
}

func (a *application) teamEditCommand() *cobra.Command {
	var flags teamFlags
	var replaceUnits bool
	cmd := &cobra.Command{
		Use: "edit <team>", Short: "Edit a team", Args: argsExact(1), ValidArgsFunction: a.completeTeams(),
		Long: `Edit a team. --permission sets every repository unit to read or write, or grants admin
access. --unit changes single units and keeps the others, unless --unit-replace is passed.`,
		Example: `$ gt team edit acme/developers --permission write
# also let the team edit the wiki
$ gt team edit acme/developers --unit repo.wiki=write
$ gt team edit acme/developers --name devs --description "Core developers"`,
		Annotations: map[string]string{"gt.arguments": teamArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := parseTeam(args[0]); err != nil {
				return err
			}
			if replaceUnits && !cmd.Flags().Changed("unit") {
				return clierr.Wrap(clierr.Usage, errors.New("--unit-replace requires --unit"))
			}
			units, err := flags.unitPolicy(cmd, false)
			if err != nil {
				return err
			}
			opts := flags.options(cmd)
			if cmd.Flags().Changed("name") {
				if strings.TrimSpace(flags.name) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("team name cannot be empty"))
				}
				opts.Name = &flags.name
			}
			opts.Units, opts.ReplaceUnits = units, replaceUnits || cmd.Flags().Changed("permission")
			if opts.Name == nil && opts.Description == nil && opts.Permission == nil && opts.Units == nil && opts.CanCreateRepo == nil && opts.AllRepositories == nil {
				return clierr.Wrap(clierr.Usage, errors.New("supply team fields to edit"))
			}
			_, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			if opts.Units != nil && !opts.ReplaceUnits && (team.Permission == "admin" || team.Permission == "owner") {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--unit cannot restrict the %s access of team %s; pass --permission read or write, or --unit-replace", team.Permission, team.Slug()))
			}
			// Gitea finds the owner team by its name, so renaming it leaves the organization without owners.
			if team.Permission == "owner" && opts.Name != nil && !strings.EqualFold(*opts.Name, team.Name) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("team %s is the organization's owner team and cannot be renamed", team.Slug()))
			}
			edited, err := service.Edit(cmd.Context(), team.ID, opts)
			if err != nil {
				return err
			}
			edited.Organization = team.Organization
			// Some Gitea versions keep an admin team admin when permission and units_map are sent together.
			if opts.Permission != nil && edited.Permission != "" && edited.Permission != *opts.Permission {
				return fmt.Errorf("team %s was edited, but its permission is %s instead of the requested %s; other changes were applied", edited.Slug(), edited.Permission, *opts.Permission)
			}
			a.done(cmd, "Edited team %s (ID %d)", edited.Slug(), edited.ID)
			return a.jsonResult(cmd, edited)
		},
	}
	flags.register(cmd, false)
	cmd.Flags().BoolVar(&replaceUnits, "unit-replace", false, "Replace the whole unit policy with --unit; omitted units lose access")
	return cmd
}

func (a *application) teamDeleteCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use: "delete <team>", Short: "Delete a team", Args: argsExact(1), ValidArgsFunction: a.completeTeams(),
		Long: "Delete a team. Deleting an organization's owner team needs --force.",
		Example: `$ gt team delete acme/reviewers
$ gt team delete 7 --yes`,
		Annotations: map[string]string{"gt.text": "team deletion does not return a resource", "gt.confirm": "true", "gt.arguments": teamArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			subject := fmt.Sprintf("team %q (ID %d)", team.Name, team.ID)
			if team.Organization != "" {
				subject += " from " + team.Organization
			}
			confirmation := terminal.Confirmation{Prompt: "Delete " + subject + "?"}
			if team.Permission == "owner" {
				if !force {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is the organization's owner team and its members would lose owner access; pass --force to delete it", subject))
				}
				confirmation = terminal.Confirmation{Prompt: "Delete owner " + subject + "? Its members lose owner access.", Typed: team.Slug()}
			}
			if err := a.confirmAction(cmd, confirmation); err != nil {
				return err
			}
			if err := service.Delete(cmd.Context(), team.ID); err != nil {
				return err
			}
			a.done(cmd, "Deleted %s", subject)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Allow deleting the organization's owner team")
	return cmd
}

// joinNames lists names in a sentence: "bob", "bob and carol", "bob, carol and dave".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func (a *application) teamMemberCommand() *cobra.Command {
	group := &cobra.Command{Use: "member", Short: "Manage team members", Long: `List, add and remove the members of a team. Adding a user to a team of an organization
also makes them a member of the organization.`}
	var limit, page int
	list := &cobra.Command{
		Use: "list <team>", Short: "List team members", Args: argsExact(1), ValidArgsFunction: a.completeTeams(),
		Example:     "$ gt team member list acme/developers",
		Annotations: map[string]string{"gt.arguments": teamArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			members, err := service.Members(cmd.Context(), team.ID, limit, page)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, members.Items, listSpec[services.TeamMember]{
				Columns: []terminal.Column{{Header: "Username"}, {Header: "Name", Flex: true}},
				Row: func(member services.TeamMember) []terminal.Cell {
					return []terminal.Cell{{Text: member.Username, Link: webURL(target, member.Username), Bold: true}, {Text: member.FullName}}
				},
				Context: "members of team " + team.Slug(), Empty: "no members in team " + team.Slug(),
			}, servicePage(members, page))
		},
	}
	pageFlags(list, &limit, &page)
	add := &cobra.Command{
		Use: "add <team> <username>...", Short: "Add users to a team", Args: usageArgs(cobra.MinimumNArgs(2)),
		Example: `$ gt team member add acme/developers bob
$ gt team member add 2 bob carol`,
		Annotations: map[string]string{"gt.text": "team membership changes do not return a resource", "gt.arguments": teamArguments},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return a.completeTeams()(cmd, args, toComplete)
			}
			return a.completeInstanceUsers()(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			for _, username := range args[1:] {
				if err := service.SetMember(cmd.Context(), team.ID, username, true); err != nil {
					return err
				}
				a.done(cmd, "Added %s to team %s", username, team.Slug())
			}
			return nil
		},
	}
	remove := &cobra.Command{
		Use: "remove <team> <username>...", Short: "Remove users from a team", Args: usageArgs(cobra.MinimumNArgs(2)),
		Example:     "$ gt team member remove acme/developers carol",
		Annotations: map[string]string{"gt.text": "team membership changes do not return a resource", "gt.confirm": "true", "gt.arguments": teamArguments},
		ValidArgsFunction: a.completeTeamThen(func(ctx context.Context, t target, team services.Team) ([]cobra.Completion, error) {
			members, err := services.Teams{Client: t.client}.Members(ctx, team.ID, completionLimit, 1)
			return describe(members.Items, func(member services.TeamMember) (string, string) { return member.Username, member.FullName }), err
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			members := args[1:]
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Remove %s from team %s?", joinNames(members), team.Slug())}); err != nil {
				return err
			}
			for _, username := range members {
				if err := service.SetMember(cmd.Context(), team.ID, username, false); err != nil {
					return err
				}
				a.done(cmd, "Removed %s from team %s", username, team.Slug())
			}
			return nil
		},
	}
	group.AddCommand(list, add, remove)
	return group
}

// teamRepository resolves a repository argument of team repo add and remove: a name inside the
// team's organization, or an OWNER/NAME that the organization owns.
func teamRepository(team services.Team, raw string) (string, error) {
	owner, name, scoped := strings.Cut(strings.TrimSpace(raw), "/")
	if !scoped {
		owner, name = team.Organization, owner
	}
	if owner == "" || name == "" || strings.Contains(name, "/") {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("invalid repository %q: use NAME or %s/NAME", raw, team.Organization))
	}
	if !strings.EqualFold(owner, team.Organization) {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("%s/%s is not owned by organization %s; teams can only be granted repositories of their organization", owner, name, team.Organization))
	}
	return team.Organization + "/" + name, nil
}

func (a *application) teamRepoCommand() *cobra.Command {
	group := &cobra.Command{Use: "repo", Short: "Manage team repository access", Long: `List the repositories a team can reach, and grant or revoke its access to repositories of
its organization. Repositories are named alone, such as service, or as ORG/NAME.`}
	var limit, page int
	list := &cobra.Command{
		Use: "list <team>", Short: "List team repositories", Args: argsExact(1), ValidArgsFunction: a.completeTeams(),
		Example:     "$ gt team repo list acme/developers",
		Annotations: map[string]string{"gt.arguments": teamArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			repos, err := service.Repositories(cmd.Context(), team.ID, limit, page)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, repos.Items, listSpec[services.Repository]{
				Columns: []terminal.Column{{Header: "Name"}, {Header: "Description", Flex: true}},
				Row: func(repo services.Repository) []terminal.Cell {
					return []terminal.Cell{{Text: repo.FullName, Link: repo.URL, Bold: true}, {Text: repo.Description}}
				},
				Context: "repositories of team " + team.Slug(), Empty: "team " + team.Slug() + " has no repositories",
			}, servicePage(repos, page))
		},
	}
	pageFlags(list, &limit, &page)
	group.AddCommand(list, a.teamRepoAccessCommand(true), a.teamRepoAccessCommand(false))
	return group
}

func (a *application) teamRepoAccessCommand(grant bool) *cobra.Command {
	verb, short, example := "remove", "Revoke team access to repositories", "$ gt team repo remove acme/developers legacy"
	annotations := map[string]string{"gt.text": "team access changes do not return a resource", "gt.arguments": teamArguments, "gt.confirm": "true"}
	if grant {
		verb, short, example = "add", "Grant a team access to repositories", "$ gt team repo add acme/developers service\n$ gt team repo add 2 acme/service acme/website"
		delete(annotations, "gt.confirm")
	}
	return &cobra.Command{
		Use: verb + " <team> <repository>...", Short: short, Args: usageArgs(cobra.MinimumNArgs(2)), Example: example, Annotations: annotations,
		Long: short + ` of its organization. Name the repositories alone, such as service, or as ORG/NAME;
repositories of other owners cannot be granted to a team.`,
		ValidArgsFunction: a.completeTeamThen(func(ctx context.Context, t target, team services.Team) ([]cobra.Completion, error) {
			if grant {
				repos, _, err := services.Repositories{Client: t.client}.List(ctx, services.RepositoryListOptions{Owner: team.Organization, Limit: completionLimit, Page: 1})
				return describe(repos, func(repo services.Repository) (string, string) { return repo.Name, repo.Description }), err
			}
			repos, err := services.Teams{Client: t.client}.Repositories(ctx, team.ID, completionLimit, 1)
			return describe(repos.Items, func(repo services.Repository) (string, string) { return repo.Name, repo.Description }), err
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("repo") {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--repo is not used by %s; name the repositories as arguments: %s <team> <repository>", cmd.CommandPath(), cmd.CommandPath()))
			}
			_, service, team, err := a.team(cmd, args[0])
			if err != nil {
				return err
			}
			var repos []string
			for _, raw := range args[1:] {
				repo, err := teamRepository(team, raw)
				if err != nil {
					return err
				}
				repos = append(repos, repo)
			}
			if !grant {
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Revoke team %s access to %s?", team.Slug(), joinNames(repos))}); err != nil {
					return err
				}
			}
			for _, repo := range repos {
				if err := service.SetRepository(cmd.Context(), team.ID, repo, grant); err != nil {
					return err
				}
				if grant {
					a.done(cmd, "Granted team %s access to %s", team.Slug(), repo)
				} else {
					a.done(cmd, "Revoked team %s access to %s", team.Slug(), repo)
				}
			}
			return nil
		},
	}
}
