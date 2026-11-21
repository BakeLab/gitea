// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

func (a *application) adminUserRenameCommand() *cobra.Command {
	return &cobra.Command{
		Use: "rename <username> <new-username>", Short: "Rename an account", Args: nonBlankArgs(cobra.ExactArgs(2), "username", "new username"), ValidArgsFunction: firstArg(a.completeAdminUsers()),
		Example: `$ gt admin user rename bob robert`,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range args {
				if err := validUsername(name); err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.AdminUsers{Client: target.client}
			if err := service.Rename(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			a.done(cmd, "Renamed user %s to %s", args[0], args[1])
			if a.options.json == "" {
				return nil
			}
			user, err := service.View(cmd.Context(), args[1])
			if err != nil {
				return err
			}
			return a.jsonResult(cmd, user)
		},
	}
}

func (a *application) adminUserKeyCommand() *cobra.Command {
	group := &cobra.Command{Use: "key", Short: "Manage the SSH keys of an account", Long: `Add and delete SSH public keys on behalf of another account. These commands need an
administrator account; gt ssh-key manages your own keys.`}
	group.AddCommand(a.adminUserKeyCreateCommand(), a.adminUserKeyDeleteCommand())
	return jsonOutput[services.SSHKey](group)
}

func (a *application) adminUserKeyCreateCommand() *cobra.Command {
	var title string
	cmd := &cobra.Command{
		Use: "create <username> [<file>]", Aliases: []string{"add"}, Short: "Add an SSH public key to an account", Args: nonBlankArgs(cobra.RangeArgs(1, 2), "username", "file"), ValidArgsFunction: pathArgs(1, false, completeFiles, a.completeAdminUsers()),
		Long: `Add an SSH public key to an account. Without a file, or with -, the key is read from stdin.

The title defaults to the key's comment, such as alice@laptop.`,
		Example: `$ gt admin user key create bob ~/bob.pub --title "Bob's laptop"
$ cat bob.pub | gt admin user key create bob`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validUsername(args[0]); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			source := "-"
			if len(args) > 1 {
				source = args[1]
			}
			if len(args) == 1 && a.deps.Streams.InTTY {
				return clierr.Wrap(clierr.Usage, errors.New("supply a public key file or pipe a key to stdin"))
			}
			_, title, content, err := a.readPublicKey(cmd, source, cmd.CommandPath()+" "+args[0], title, "")
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			created, err := (services.AdminUsers{Client: target.client}).CreateKey(cmd.Context(), args[0], title, content)
			if err != nil {
				return titleTaken(err, "SSH key", title)
			}
			a.done(cmd, "Added %s to user %s", sshKeyLabel(created), args[0])
			return a.jsonResult(cmd, created)
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Key title (defaults to the key comment)")
	return cmd
}

func (a *application) adminUserKeyDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete <username> <id>", Short: "Delete an SSH key of an account", Args: nonBlankArgs(cobra.ExactArgs(2), "username", "key ID"), ValidArgsFunction: firstArg(a.completeAdminUsers()),
		Example: `$ gt admin user key delete bob 7
$ gt admin user key delete bob 7 --yes`,
		Annotations: map[string]string{"gt.text": "SSH key deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validUsername(args[0]); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			id, err := number(args[1])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.AdminUsers{Client: target.client}
			return a.deleteConfirmed(cmd, fmt.Sprintf("SSH key %d of user %s", id, args[0]), func() error { return service.DeleteKey(cmd.Context(), args[0], id) })
		},
	}
}

func (a *application) adminUserBadgeCommand() *cobra.Command {
	group := &cobra.Command{Use: "badge", Short: "Manage the badges of an account", Long: `List the badges of an account, and award or take them away by slug. These commands need an
administrator account.`}
	list := &cobra.Command{
		Use: "list <username>", Short: "List the badges of an account", Args: nonBlankArgs(cobra.ExactArgs(1), "username"), ValidArgsFunction: firstArg(a.completeAdminUsers()),
		Example: `$ gt admin user badge list bob
$ gt admin user badge list bob --jq '.[].slug'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validUsername(args[0]); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			badges, err := (services.AdminUsers{Client: target.client}).Badges(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeTable(a, cmd, badges, listSpec[services.AdminBadge]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Slug"}, {Header: "Description", Flex: true}},
				Row: func(badge services.AdminBadge) []terminal.Cell {
					return []terminal.Cell{{Text: strconv.FormatInt(badge.ID, 10)}, {Text: badge.Slug, Bold: true}, {Text: badge.Description}}
				},
				Context: "badges of " + args[0], Empty: args[0] + " has no badges",
			}, listPage{Number: 1})
		},
	}
	group.AddCommand(list, a.adminUserBadgeChangeCommand(true), a.adminUserBadgeChangeCommand(false))
	return jsonOutput[services.AdminBadge](group)
}

// adminUserBadgeChangeCommand awards badges when add is set and takes them away otherwise.
func (a *application) adminUserBadgeChangeCommand(add bool) *cobra.Command {
	verb, short, past, preposition := "delete", "Take badges away from an account", "Took", "from"
	example := "$ gt admin user badge delete bob contributor"
	change := services.AdminUsers.RemoveBadges
	if add {
		change = services.AdminUsers.AddBadges
		verb, short, past, preposition = "add", "Award badges to an account", "Awarded", "to"
		example = "$ gt admin user badge add bob contributor early-adopter"
	}
	return &cobra.Command{
		Use: verb + " <username> <slug>...", Short: short, Args: nonBlankArgs(cobra.MinimumNArgs(2), "username", "badge slug"), ValidArgsFunction: firstArg(a.completeAdminUsers()),
		Example:     example,
		Annotations: map[string]string{"gt.text": "badge changes do not return a resource"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validUsername(args[0]); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			if err := change(services.AdminUsers{Client: target.client}, cmd.Context(), args[0], args[1:]); err != nil {
				return err
			}
			a.done(cmd, "%s badges %s %s %s", past, strings.Join(args[1:], ", "), preposition, args[0])
			return nil
		},
	}
}

func (a *application) adminOrganizationCommand() *cobra.Command {
	group := &cobra.Command{Use: "org", Short: "Manage the organizations of the instance", Long: `List every organization of the instance, private ones included, and create one on
behalf of a user. These commands need an administrator account; gt org manages organizations
you belong to.`}
	var limit, page int
	list := &cobra.Command{
		Use: "list", Short: "List every organization", Args: argsExact(0),
		Example: `$ gt admin org list
$ gt admin org list --json name,visibility`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			orgs, err := (services.AdminOrganizations{Client: target.client}).List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			spec := organizationTable(target)
			spec.Context, spec.Empty = "organizations on "+target.Host, "no organizations on "+target.Host
			return writeTable(a, cmd, orgs.Items, spec, servicePage(orgs, page))
		},
	}
	pageFlags(list, &limit, &page)

	var owner string
	create := &cobra.Command{
		Use: "create <name>", Short: "Create an organization for a user", Args: nonBlankArgs(cobra.ExactArgs(1), "organization name"),
		Example: `$ gt admin org create acme --owner bob --full-name "ACME Corp"`,
	}
	create.Flags().StringVar(&owner, "owner", "", "User who owns the organization (required)")
	_ = create.RegisterFlagCompletionFunc("owner", a.completeInstanceUsers())
	options := organizationFlags(create, true)
	create.RunE = func(cmd *cobra.Command, args []string) error {
		if _, err := a.requireInput(cmd, &owner, "owner", "Owner", validUsername); err != nil {
			return err
		}
		if err := validUsername(owner); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
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
		org, err := (services.AdminOrganizations{Client: target.client}).Create(cmd.Context(), owner, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Created organization %s for %s", org.Name, owner)
		return a.created(cmd, org, webURL(target, org.Name))
	}
	group.AddCommand(list, create)
	return jsonOutput[services.Organization](group)
}

// completeCronTasks completes the names of the scheduled tasks.
func (a *application) completeCronTasks() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		tasks, err := (services.AdminCron{Client: t.client}).List(ctx, completionLimit, 1)
		return describe(tasks.Items, func(task services.CronTask) (string, string) { return task.Name, task.Schedule }), err
	})
}

func (a *application) adminCronCommand() *cobra.Command {
	group := &cobra.Command{Use: "cron", Short: "Manage the scheduled tasks of the instance", Long: `List the scheduled background tasks of the instance, and start one right away. These
commands need an administrator account.`}
	var limit, page int
	list := &cobra.Command{
		Use: "list", Short: "List scheduled tasks", Args: argsExact(0),
		Example: `$ gt admin cron list
$ gt admin cron list --jq '.[].name'`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			tasks, err := (services.AdminCron{Client: target.client}).List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, tasks.Items, listSpec[services.CronTask]{
				Columns: []terminal.Column{{Header: "Name", Flex: true}, {Header: "Schedule"}, {Header: "Next"}, {Header: "Last"}, {Header: "Runs"}},
				Row: func(task services.CronTask) []terminal.Cell {
					return []terminal.Cell{{Text: task.Name, Bold: true}, {Text: task.Schedule}, {Time: terminal.ParseTime(task.Next)}, {Time: terminal.ParseTime(task.Prev)}, {Text: strconv.FormatInt(task.ExecTimes, 10)}}
				},
				Context: "scheduled tasks on " + target.Host, Empty: "no scheduled tasks on " + target.Host,
			}, servicePage(tasks, page))
		},
	}
	pageFlags(list, &limit, &page)
	run := &cobra.Command{
		Use: "run <task>", Short: "Start a scheduled task now", Args: nonBlankArgs(cobra.ExactArgs(1), "task name"), ValidArgsFunction: firstArg(a.completeCronTasks()),
		Example:     `$ gt admin cron run update_mirrors`,
		Annotations: map[string]string{"gt.text": "starting a task does not return a resource"},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			if err := (services.AdminCron{Client: target.client}).Run(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.done(cmd, "Started task %s", args[0])
			return nil
		},
	}
	group.AddCommand(list, run)
	return jsonOutput[services.CronTask](group)
}

func (a *application) adminEmailCommand() *cobra.Command {
	group := &cobra.Command{Use: "email", Short: "List the email addresses of all accounts", Long: `List the email addresses registered on the instance and the accounts that hold them.
These commands need an administrator account.`}
	var query string
	var limit, page int
	list := &cobra.Command{
		Use: "list", Short: "List email addresses", Args: argsExact(0),
		Example: `$ gt admin email list
# find addresses by address, username or full name
$ gt admin email list --query example.com`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			emails, err := (services.AdminEmails{Client: target.client}).List(cmd.Context(), query, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.AdminEmail]{
				Columns: []terminal.Column{{Header: "Email", Flex: true}, {Header: "Username"}, {Header: "Primary"}, {Header: "Verified"}},
				Row: func(email services.AdminEmail) []terminal.Cell {
					return []terminal.Cell{{Text: email.Email, Bold: true}, {Text: email.Username}, {Text: yesNo(email.Primary)}, {Text: yesNo(email.Verified)}}
				},
				Context: "email addresses on " + target.Host, Empty: "no email addresses on " + target.Host,
			}
			if query != "" {
				spec.Context, spec.Empty = fmt.Sprintf("email addresses matching %q", query), fmt.Sprintf("no email addresses match %q", query)
			}
			return writeTable(a, cmd, emails.Items, spec, servicePage(emails, page))
		},
	}
	list.Flags().StringVar(&query, "query", "", "Only list addresses whose address, username or full name contains this text")
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	return jsonOutput[services.AdminEmail](group)
}
