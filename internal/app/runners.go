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

func (a *application) runnerService(cmd *cobra.Command, scope *ownerScope) (target, services.Runners, error) {
	t, selected, err := scope.resolve(a, cmd)
	return t, services.Runners{Client: t.client, Scope: selected}, err
}

func runnerSettingsURL(t target, scope services.OwnerScope, id int64) string {
	return ownerSettingsURL(t, scope, "actions", "runners", strconv.FormatInt(id, 10))
}

// runnerLabel names a runner in messages: "runner 3 (builder-1)".
func runnerLabel(runner services.Runner) string {
	return fmt.Sprintf("runner %d (%s)", runner.ID, runner.Name)
}

func (a *application) runnerCommand() *cobra.Command {
	group := &cobra.Command{Use: "runner", Short: "Manage Actions runners", Long: `Manage the Actions runners that run workflow jobs: list them, see what they are doing, stop
them from taking jobs, remove them, and print the token that registers new ones.

Runners belong to a repository, to an organization with --org, to your account with --account, or to
the whole instance with --global (administrators). Without these flags, gt uses the repository of
the working directory or --repo. A list shows only the runners of its scope, not the organization
and instance runners that also take a repository's jobs; with --global it shows every runner.`}
	var scope ownerScope
	group.PersistentFlags().StringVar(&scope.org, "org", "", "Manage the runners of an organization")
	group.PersistentFlags().BoolVar(&scope.account, "account", false, "Manage the runners of your account")
	group.PersistentFlags().BoolVar(&scope.global, "global", false, "Manage the runners of the instance (administrators)")
	_ = group.RegisterFlagCompletionFunc("org", a.completeOrgs())
	group.AddCommand(a.runnerListCommand(&scope), a.runnerViewCommand(&scope), a.runnerStateCommand(&scope, true), a.runnerStateCommand(&scope, false), a.runnerDeleteCommand(&scope), a.runnerTokenCommand(&scope))
	return jsonOutput[services.Runner](group)
}

// completeRunners completes the runner IDs of the scope that the parsed flags select, with their names.
func (a *application) completeRunners(scope *ownerScope) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return a.apiCompletion(scope.inRepository(), func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			runners, err := services.Runners{Client: t.client, Scope: scope.of(t.Repo)}.List(ctx, completionLimit, 1, nil)
			return describe(runners.Items, func(runner services.Runner) (string, string) {
				return strconv.FormatInt(runner.ID, 10), runner.Name + " (" + runner.Status + ")"
			}), err
		})(cmd, args, toComplete)
	}
}

// runnerNotes lists what is unusual about a runner: "busy, disabled".
func runnerNotes(runner services.Runner) string {
	var notes []string
	for _, note := range []struct {
		name string
		set  bool
	}{{"busy", runner.Busy}, {"disabled", runner.Disabled}, {"ephemeral", runner.Ephemeral}} {
		if note.set {
			notes = append(notes, note.name)
		}
	}
	return strings.Join(notes, ", ")
}

func (a *application) runnerListCommand(scope *ownerScope) *cobra.Command {
	var limit, page int
	var disabled, enabled bool
	cmd := &cobra.Command{
		Use: "list", Short: "List runners", Args: argsExact(0),
		Example: `$ gt runner list
$ gt runner list --org acme --disabled
# runners that are online, with their labels
$ gt runner list --json name,status,labels --jq '.[] | select(.status == "online")'`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if disabled && enabled {
				return clierr.Wrap(clierr.Usage, errors.New("--disabled cannot be combined with --enabled"))
			}
			target, service, err := a.runnerService(cmd, scope)
			if err != nil {
				return err
			}
			var filter *bool
			if disabled || enabled {
				filter = &disabled
			}
			runners, err := service.List(cmd.Context(), limit, page, filter)
			if err != nil {
				return err
			}
			where := service.Scope.String()
			return writeTable(a, cmd, runners.Items, listSpec[services.Runner]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Name"}, {Header: "Status"}, {Header: "Labels", Flex: true}, {Header: "Notes"}},
				Row: func(runner services.Runner) []terminal.Cell {
					return []terminal.Cell{
						{Text: strconv.FormatInt(runner.ID, 10), Link: runnerSettingsURL(target, service.Scope, runner.ID)},
						{Text: terminal.SafeLine(runner.Name), Bold: true},
						{Text: terminal.SafeLine(runner.Status)},
						{Text: terminal.SafeLine(strings.Join(runner.Labels, ", "))},
						{Text: runnerNotes(runner), Muted: true},
					}
				},
				Context: "runners of " + where, Empty: "no runners for " + where,
			}, servicePage(runners, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Only runners that are disabled")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "Only runners that are enabled")
	return cmd
}

func (a *application) runnerViewCommand(scope *ownerScope) *cobra.Command {
	return &cobra.Command{
		Use: "view <id>", Short: "View a runner", Args: argsExact(1), ValidArgsFunction: a.completeRunners(scope),
		Example: `$ gt runner view 3
$ gt runner view 3 --org acme --json labels,status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, service, err := a.runnerService(cmd, scope)
			if err != nil {
				return err
			}
			runner, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return writeView(a, cmd, runner, func(runner services.Runner) terminal.View {
				badges := []terminal.Cell{{Text: runner.Status}}
				if runner.Disabled {
					badges = append(badges, terminal.Cell{Text: "disabled", Muted: true})
				}
				return terminal.View{
					Title: runner.Name, ID: fmt.Sprintf("(runner %d)", runner.ID), Badges: badges,
					Fields: []terminal.Field{
						{Label: "Status", Value: runner.Status, PipedOnly: true},
						{Label: "Scope", Value: service.Scope.String()},
						{Label: "Busy", Value: yesNo(runner.Busy)},
						{Label: "Disabled", Value: yesNo(runner.Disabled)},
						{Label: "Ephemeral", Value: yesNo(runner.Ephemeral)},
						{Label: "Labels", Value: strings.Join(runner.Labels, ", ")},
					},
					Bodyless: true, Kind: "runner", URL: runnerSettingsURL(target, service.Scope, runner.ID),
				}
			})
		},
	}
}

// runnerStateCommand disables a runner so that it takes no new jobs, or enables it again.
func (a *application) runnerStateCommand(scope *ownerScope, disable bool) *cobra.Command {
	verb, effect := "enable", "lets it take jobs again"
	if disable {
		verb, effect = "disable", "stops it from taking new jobs; a job it already runs finishes"
	}
	past := titleCase(verb) + "d"
	return &cobra.Command{
		Use: verb + " <id>", Short: titleCase(verb) + " a runner", Args: argsExact(1), ValidArgsFunction: a.completeRunners(scope),
		Long:    "Mark a runner as " + verb + "d, which " + effect + ".",
		Example: fmt.Sprintf("$ gt runner %s 3\n$ gt runner %s 3 --org acme", verb, verb),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			_, service, err := a.runnerService(cmd, scope)
			if err != nil {
				return err
			}
			runner, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			where := runnerLabel(runner) + " of " + service.Scope.String()
			if runner.Disabled == disable {
				a.warn(cmd, "%s is already %sd", where, verb)
				return a.jsonResult(cmd, runner)
			}
			if runner, err = service.SetDisabled(cmd.Context(), id, disable); err != nil {
				return err
			}
			a.done(cmd, "%s %s", past, where)
			return a.jsonResult(cmd, runner)
		},
	}
}

func (a *application) runnerDeleteCommand(scope *ownerScope) *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete a runner", Args: argsExact(1), ValidArgsFunction: a.completeRunners(scope),
		Long: `Remove a runner from Gitea. The runner program keeps running but can no longer take jobs
until it registers again with a registration token.`,
		Example: `$ gt runner delete 3
$ gt runner delete 4 --org acme --yes`,
		Annotations: map[string]string{"gt.text": "runner deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			_, service, err := a.runnerService(cmd, scope)
			if err != nil {
				return err
			}
			runner, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.deleteConfirmed(cmd, runnerLabel(runner)+" from "+service.Scope.String(), func() error { return service.Delete(cmd.Context(), id) })
		},
	}
}

func (a *application) runnerTokenCommand(scope *ownerScope) *cobra.Command {
	cmd := &cobra.Command{
		Use: "token", Short: "Print the runner registration token", Args: argsExact(0),
		Long: `Print the token that registers new runners with the repository, organization, account or
instance. Gitea creates it on first use and returns the same one until it is reset in the web
interface. Anyone with the token can register a runner there, and runners receive the jobs and
secrets of that scope, so treat it as a secret.`,
		Example: `$ gt runner token
$ gitea-runner register --no-interactive --instance https://gitea.example.com --token "$(gt runner token --org acme)"`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, service, err := a.runnerService(cmd, scope)
			if err != nil {
				return err
			}
			token, err := service.RegistrationToken(cmd.Context())
			if err != nil {
				return err
			}
			return a.created(cmd, token, token.Token)
		},
	}
	return jsonOutput[services.RegistrationToken](cmd)
}
