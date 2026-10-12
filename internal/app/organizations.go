// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

var visibilities = []string{"public", "limited", "private"}

// visibilityColors color the visibility of organizations and accounts.
var visibilityColors = map[string]string{"public": "green", "limited": "yellow", "private": "red"}

// timeField shows an API timestamp relative to now on a TTY, and as RFC3339 for scripts.
func timeField(label, raw string, d terminal.Display) terminal.Field {
	t := terminal.ParseTime(raw)
	if t.IsZero() {
		return terminal.Field{Label: label}
	}
	if !d.TTY {
		return terminal.Field{Label: label, Value: terminal.Timestamp(t)}
	}
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	return terminal.Field{Label: label, Value: terminal.RelativeTime(now, t)}
}

// countOf words a count, such as "1 member" or "2 members".
func countOf(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

func (a *application) organizationCommand() *cobra.Command {
	group := &cobra.Command{Use: "org", Short: "Manage organizations", Long: `Work with Gitea organizations: list the ones you belong to, view, create, edit and delete
them, and manage their members.

Teams grant members access to an organization's repositories; see gt team.`}
	group.AddCommand(a.organizationListCommand(), a.organizationViewCommand(), a.organizationCreateCommand(), a.organizationEditCommand(), a.organizationDeleteCommand(), a.organizationMemberCommand())
	return group
}

func (a *application) organizationListCommand() *cobra.Command {
	var all bool
	var username string
	var limit, page int
	cmd := &cobra.Command{
		Use: "list", Short: "List organizations", Args: argsExact(0),
		Long: "List the organizations you belong to, those of another user, or every organization you can see.",
		Example: `# list your organizations
$ gt org list
# list the organizations of another user
$ gt org list --username bob
# list every organization on the instance
$ gt org list --all`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all && username != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--all and --username are mutually exclusive"))
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			orgs, err := (services.Organizations{Client: target.client}).List(cmd.Context(), !all && username == "", username, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.Organization]{
				Columns: []terminal.Column{{Header: "Name"}, {Header: "Full name", Flex: true}, {Header: "Visibility"}},
				Context: "organizations you belong to", Empty: "you don't belong to any organization",
				Row: func(org services.Organization) []terminal.Cell {
					return []terminal.Cell{{Text: org.Name, Link: webURL(target, org.Name), Bold: true}, {Text: org.FullName}, {Text: org.Visibility, Color: visibilityColors[org.Visibility]}}
				},
			}
			switch {
			case all:
				spec.Context, spec.Empty = "organizations on "+target.Host, "no organizations on "+target.Host
			case username != "":
				spec.Context, spec.Empty = "organizations of "+username, "no organizations of "+username
			}
			return writeTable(a, cmd, orgs.Items, spec, servicePage(orgs, page))
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "List every organization you can see instead of your memberships")
	cmd.Flags().StringVar(&username, "username", "", "List the organizations of this user")
	_ = cmd.RegisterFlagCompletionFunc("username", a.completeInstanceUsers())
	pageFlags(cmd, &limit, &page)
	return cmd
}

func (a *application) organizationViewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "view <org>", Short: "View an organization", Args: argsExact(1),
		Example: `$ gt org view acme
# print the description only
$ gt org view acme --jq .description`,
		ValidArgsFunction: a.completeOrgs(),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Organizations{Client: target.client}
			org, err := service.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			var size services.OrganizationSize
			if a.options.json == "" {
				if size, err = service.Size(cmd.Context(), org.Name); err != nil {
					return err
				}
			}
			return writeView(a, cmd, org, func(org services.Organization) terminal.View {
				return organizationView(org, size, webURL(target, org.Name))
			})
		},
	}
	return cmd
}

func organizationView(org services.Organization, size services.OrganizationSize, address string) terminal.View {
	view := terminal.View{
		Title: org.Name, Badges: []terminal.Cell{{Text: org.Visibility, Color: visibilityColors[org.Visibility]}},
		Body: org.Description, Kind: "organization", URL: address,
	}
	if org.FullName != "" && org.FullName != org.Name {
		view.Title, view.ID = org.FullName, "("+org.Name+")"
	}
	var counts []string
	for _, count := range []struct {
		label            string
		n                *int
		singular, plural string
	}{{"Members", size.Members, "member", "members"}, {"Teams", size.Teams, "team", "teams"}, {"Repositories", size.Repositories, "repository", "repositories"}} {
		if count.n != nil {
			counts = append(counts, countOf(*count.n, count.singular, count.plural))
			view.Fields = append(view.Fields, terminal.Field{Label: count.label, Value: strconv.Itoa(*count.n), PipedOnly: true})
		}
	}
	view.Meta = strings.Join(counts, " • ")
	view.Fields = append([]terminal.Field{
		{Label: "Name", Value: org.Name, PipedOnly: true},
		{Label: "Visibility", Value: org.Visibility, PipedOnly: true},
		{Label: "Website", Value: org.Website},
		{Label: "Location", Value: org.Location},
		{Label: "Repo admins manage team access", Value: yesNo(org.RepoAdminChangeTeamAccess)},
	}, view.Fields...)
	return view
}

// organizationFlags registers the fields that create and edit set and returns the options for the
// supplied ones. Edit flags have no defaults, so that only what is passed changes.
func organizationFlags(cmd *cobra.Command, create bool) func() services.OrganizationOptions {
	var fullName, description, website, location, visibility string
	var adminAccess bool
	cmd.Flags().StringVar(&fullName, "full-name", "", "Display name")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Organization description")
	cmd.Flags().StringVar(&website, "website", "", "Website URL")
	cmd.Flags().StringVar(&location, "location", "", "Location")
	initial := ""
	if create {
		initial = "public"
	}
	enumFlag(cmd, &visibility, "visibility", "", initial, "Who can see the organization", visibilities...)
	cmd.Flags().BoolVar(&adminAccess, "repo-admins-can-change-team-access", false, "Allow repository administrators to change team access (use =false to revoke)")
	return func() services.OrganizationOptions {
		opts := services.OrganizationOptions{}
		for _, field := range []struct {
			flag   string
			value  *string
			option **string
		}{{"full-name", &fullName, &opts.FullName}, {"description", &description, &opts.Description}, {"website", &website, &opts.Website}, {"location", &location, &opts.Location}, {"visibility", &visibility, &opts.Visibility}} {
			if cmd.Flags().Changed(field.flag) {
				*field.option = field.value
			}
		}
		if cmd.Flags().Changed("repo-admins-can-change-team-access") {
			opts.RepoAdminChangeTeamAccess = &adminAccess
		}
		if create {
			opts.Visibility, opts.RepoAdminChangeTeamAccess = &visibility, &adminAccess
		}
		return opts
	}
}

// organizationName rejects an empty organization argument.
func organizationName(raw string) (string, error) {
	if name := strings.TrimSpace(raw); name != "" {
		return name, nil
	}
	return "", clierr.Wrap(clierr.Usage, errors.New("organization name cannot be empty"))
}

func (a *application) organizationCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "create <name>", Short: "Create an organization", Args: argsExact(1),
		Example: `$ gt org create acme --full-name "ACME Corp"
# create a private organization
$ gt org create acme-internal --visibility private`,
	}
	options := organizationFlags(cmd, true)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name, err := organizationName(args[0])
		if err != nil {
			return err
		}
		opts := options()
		opts.Name = &name
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		org, err := (services.Organizations{Client: target.client}).Create(cmd.Context(), opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Created organization %s", org.Name)
		return a.created(cmd, org, webURL(target, org.Name))
	}
	return cmd
}

func (a *application) organizationEditCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "edit <org>", Short: "Edit an organization", Args: argsExact(1), ValidArgsFunction: a.completeOrgs(),
		Example: `$ gt org edit acme --description "Tools for everyone" --website https://acme.example
$ gt org edit acme --visibility private`,
	}
	options := organizationFlags(cmd, false)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		name, err := organizationName(args[0])
		if err != nil {
			return err
		}
		opts := options()
		if opts == (services.OrganizationOptions{}) {
			return clierr.Wrap(clierr.Usage, errors.New("supply organization fields to edit"))
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		org, err := (services.Organizations{Client: target.client}).Edit(cmd.Context(), name, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Edited organization %s", org.Name)
		return a.jsonResult(cmd, org)
	}
	return cmd
}

func (a *application) organizationDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete <org>", Short: "Delete an organization", Args: argsExact(1), ValidArgsFunction: a.completeOrgs(),
		Long: `Delete an organization with its teams. Gitea refuses while the organization owns
repositories: transfer or delete them first.

The deletion cannot be undone, so a terminal asks you to type the organization's name; --yes
skips the question.`,
		Example: `$ gt org delete acme-old
$ gt org delete acme-old --yes`,
		Annotations: map[string]string{"gt.text": "organization deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := organizationName(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Organizations{Client: target.client}
			org, err := service.View(cmd.Context(), name)
			if err != nil {
				return err
			}
			repos, err := service.RepositoryCount(cmd.Context(), org.Name)
			if err != nil {
				return err
			}
			if repos != nil && *repos > 0 {
				return &services.ConflictError{Reason: services.ReasonOwnsRepos, Kind: "organization", Key: org.Name, Hint: "transfer or delete them first (see gt repo list " + org.Name + ")"}
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{
				Prompt: fmt.Sprintf("Delete organization %s and its teams? This cannot be undone.", org.Name),
				Typed:  org.Name, NonTTY: "delete organization " + org.Name,
			}); err != nil {
				return err
			}
			if err := service.Delete(cmd.Context(), org.Name); err != nil {
				return err
			}
			a.done(cmd, "Deleted organization %s", org.Name)
			return nil
		},
	}
}

func (a *application) organizationMemberCommand() *cobra.Command {
	group := &cobra.Command{Use: "member", Short: "Manage organization membership", Long: `List the members of an organization, remove them, and choose whether a membership is public.

Gitea adds members through teams: add someone to an organization with
gt team member add ORG/TEAM USER.`}
	var public bool
	var limit, page int
	list := &cobra.Command{
		Use: "list <org>", Short: "List organization members", Args: argsExact(1), ValidArgsFunction: a.completeOrgs(),
		Long: "List the members of an organization and whether each membership is public or concealed.",
		Example: `$ gt org member list acme
# list only the members everyone can see
$ gt org member list acme --public`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			org := args[0]
			members, err := (services.Organizations{Client: target.client}).Members(cmd.Context(), org, services.MemberListOptions{Public: public, Visibility: true, Limit: limit, Page: page})
			if err != nil {
				return err
			}
			what := "members"
			if public {
				what = "public members"
			}
			return writeTable(a, cmd, members.Items, listSpec[services.OrganizationMember]{
				Columns: []terminal.Column{{Header: "Username"}, {Header: "Name", Flex: true}, {Header: "Visibility"}},
				Row: func(member services.OrganizationMember) []terminal.Cell {
					visibility := terminal.Cell{Text: member.Visibility, Muted: member.Visibility == services.MembershipConcealed}
					return []terminal.Cell{{Text: member.Username, Link: webURL(target, member.Username), Bold: true}, {Text: member.FullName}, visibility}
				},
				Context: what + " of " + org, Empty: "no " + what + " in " + org,
			}, servicePage(members, page))
		},
	}
	list.Flags().BoolVar(&public, "public", false, "List only public memberships")
	pageFlags(list, &limit, &page)
	group.AddCommand(list, a.organizationVisibilityCommand(true), a.organizationVisibilityCommand(false), a.organizationMemberRemoveCommand())
	return group
}

// completeOrganizationMembers completes an organization, then the members of that organization.
func (a *application) completeOrganizationMembers() cobra.CompletionFunc {
	orgs := a.completeOrgs()
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return orgs(cmd, args, toComplete)
		}
		return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			members, err := (services.Organizations{Client: t.client}).Members(ctx, args[0], services.MemberListOptions{Limit: completionLimit, Page: 1})
			return describe(members.Items, func(member services.OrganizationMember) (string, string) { return member.Username, member.FullName }), err
		})(cmd, args, toComplete)
	}
}

// organizationVisibilityCommand publicizes or conceals an organization membership.
func (a *application) organizationVisibilityCommand(public bool) *cobra.Command {
	verb, alias, short, state := "conceal", "private", "Hide a membership", "concealed"
	if public {
		verb, alias, short, state = "publicize", "public", "Make a membership public", "public"
	}
	return &cobra.Command{
		Use: verb + " <org> <username>", Aliases: []string{alias}, Short: short, Args: argsExact(2),
		Long:              fmt.Sprintf("Make the membership of a user %s. A public membership shows on the organization's profile.", state),
		Example:           fmt.Sprintf("$ gt org member %s acme bob", verb),
		Annotations:       textOutput("membership changes do not return a resource"),
		ValidArgsFunction: a.completeOrganizationMembers(),
		RunE: func(cmd *cobra.Command, args []string) error {
			org, username := args[0], args[1]
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Organizations{Client: target.client}
			member, err := service.IsMember(cmd.Context(), org, username)
			if err != nil {
				return err
			}
			if !member {
				return fmt.Errorf("%s is not a member of %s", username, org)
			}
			current, err := service.IsPublicMember(cmd.Context(), org, username)
			if err != nil {
				return err
			}
			if current == public {
				a.warn(cmd, "%s's membership of %s is already %s", username, org, state)
				return nil
			}
			if err := service.SetMemberPublic(cmd.Context(), org, username, public); err != nil {
				return err
			}
			if public {
				a.done(cmd, "Made %s's membership of %s public", username, org)
			} else {
				a.done(cmd, "Concealed %s's membership of %s", username, org)
			}
			return nil
		},
	}
}

func (a *application) organizationMemberRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use: "remove <org> <username>", Short: "Remove a member from an organization", Args: argsExact(2),
		Long:              "Remove a user from an organization and from all of its teams.",
		Example:           "$ gt org member remove acme bob",
		Annotations:       map[string]string{"gt.text": "membership changes do not return a resource", "gt.confirm": "true"},
		ValidArgsFunction: a.completeOrganizationMembers(),
		RunE: func(cmd *cobra.Command, args []string) error {
			org, username := args[0], args[1]
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Organizations{Client: target.client}
			member, err := service.IsMember(cmd.Context(), org, username)
			if err != nil {
				return err
			}
			if !member {
				a.warn(cmd, "%s is not a member of %s", username, org)
				return nil
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{
				Prompt: fmt.Sprintf("Remove %s from organization %s and its teams?", username, org),
				NonTTY: fmt.Sprintf("remove %s from organization %s", username, org),
			}); err != nil {
				return err
			}
			if err := service.RemoveMember(cmd.Context(), org, username); err != nil {
				return err
			}
			a.done(cmd, "Removed %s from organization %s", username, org)
			return nil
		},
	}
}

// completeInstanceUsers completes the usernames of the instance that contain the typed text.
func (a *application) completeInstanceUsers() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, toComplete string) ([]cobra.Completion, error) {
		users, err := (services.AdminUsers{Client: t.client}).Search(ctx, toComplete, completionLimit)
		return describe(users, func(user services.AdminUser) (string, string) { return user.Username, user.FullName }), err
	})
}
