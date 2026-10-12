// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func validateWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return clierr.Wrap(clierr.Usage, errors.New("webhook URL must be an absolute HTTP(S) URL without user information or fragment"))
	}
	return nil
}

// webhookEventGroups are the events that issues and pull_request select; tables show a complete
// group as one entry.
var webhookEventGroups = map[string][]string{
	"issues":       {"issue_assign", "issue_comment", "issue_label", "issue_milestone", "issues"},
	"pull_request": {"pull_request", "pull_request_assign", "pull_request_comment", "pull_request_label", "pull_request_milestone", "pull_request_review", "pull_request_review_request", "pull_request_sync"},
}

// collapsedEvents lists events with each complete group as one entry, such as "pull_request (8)".
func collapsedEvents(events []string) string {
	grouped := map[string]bool{}
	for _, group := range webhookEventGroups {
		if !slices.ContainsFunc(group, func(event string) bool { return !slices.Contains(events, event) }) {
			for _, event := range group {
				grouped[event] = true
			}
		}
	}
	var shown []string
	for _, event := range events {
		switch group, head := webhookEventGroups[event]; {
		case head && grouped[event]:
			shown = append(shown, fmt.Sprintf("%s (%d)", event, len(group)))
		case !grouped[event]:
			shown = append(shown, event)
		}
	}
	return strings.Join(shown, ", ")
}

// webhookScope holds the scope flags of the webhook commands.
type webhookScope struct {
	org             string
	account, global bool
}

// service resolves the target and the webhooks of the selected scope.
func (s *webhookScope) service(a *application, cmd *cobra.Command) (target, services.Webhooks, error) {
	scopes := 0
	for _, selected := range []bool{s.org != "", s.account, s.global, a.options.repo != ""} {
		if selected {
			scopes++
		}
	}
	if scopes > 1 {
		return target{}, services.Webhooks{}, clierr.Wrap(clierr.Usage, errors.New("--org, --account, --global, and --repo select mutually exclusive scopes"))
	}
	t, err := a.resolve(cmd.Context(), s.org == "" && !s.account && !s.global)
	return t, services.Webhooks{Client: t.client, Scope: services.WebhookScope{Repository: t.Repo, Organization: s.org, Account: s.account, Global: s.global}}, err
}

// webhookScopeName names a scope in messages: alice/x, organization acme, your account, the instance.
func webhookScopeName(scope services.WebhookScope) string {
	switch {
	case scope.Global:
		return "the instance"
	case scope.Account:
		return "your account"
	case scope.Organization != "":
		return "organization " + scope.Organization
	}
	return scope.Repository
}

// webhookSettingsURL is the settings page of a webhook.
func webhookSettingsURL(t target, scope services.WebhookScope, id int64) string {
	hook := strconv.FormatInt(id, 10)
	switch {
	case scope.Global:
		return webURL(t, "-", "admin", "hooks", hook)
	case scope.Account:
		return webURL(t, "user", "settings", "hooks", hook)
	case scope.Organization != "":
		return webURL(t, "org", scope.Organization, "settings", "hooks", hook)
	}
	owner, name, _ := strings.Cut(scope.Repository, "/")
	return webURL(t, owner, name, "settings", "hooks", hook)
}

// webhookLabel names a webhook in messages: "webhook 3 (https://hooks.example.com/…)".
func webhookLabel(hook services.Webhook) string {
	return fmt.Sprintf("webhook %d (%s)", hook.ID, hook.URL)
}

func (a *application) webhookCommand() *cobra.Command {
	group := &cobra.Command{Use: "webhook", Short: "Manage webhooks", Long: `Manage the webhooks of a repository, of an organization with --org, of your account with
--account, or of the instance with --global (administrators). Without these flags, webhooks
belong to the repository of the current directory or of --repo.

Delivery URLs often carry credentials, so gt shows them without user information and query,
and without the path for chat services; gt webhook view --reveal-url shows a URL whole.
Signing secrets and authorization headers are never shown.`}
	var scope webhookScope
	group.PersistentFlags().StringVar(&scope.org, "org", "", "Manage the webhooks of an organization")
	group.PersistentFlags().BoolVar(&scope.account, "account", false, "Manage the webhooks of your account")
	group.PersistentFlags().BoolVar(&scope.global, "global", false, "Manage the system webhooks of the instance (administrators)")
	_ = group.RegisterFlagCompletionFunc("org", a.completeOrgs())
	group.AddCommand(a.webhookListCommand(&scope), a.webhookViewCommand(&scope), a.webhookChangeCommand(&scope, true), a.webhookChangeCommand(&scope, false), a.webhookDeleteCommand(&scope))
	return group
}

// completeWebhooks completes the webhook IDs of the scope that the parsed flags select, with their
// URLs and events.
func (a *application) completeWebhooks(scope *webhookScope) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		inRepository := scope.org == "" && !scope.account && !scope.global
		return a.apiCompletion(inRepository, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			hooks, err := services.Webhooks{Client: t.client, Scope: services.WebhookScope{Repository: t.Repo, Organization: scope.org, Account: scope.account, Global: scope.global}}.List(ctx, completionLimit, 1)
			return describe(hooks.Items, func(hook services.Webhook) (string, string) {
				return strconv.FormatInt(hook.ID, 10), hook.URL + " (" + collapsedEvents(hook.Events) + ")"
			}), err
		})(cmd, args, toComplete)
	}
}

func (a *application) webhookListCommand(scope *webhookScope) *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use: "list", Short: "List webhooks", Args: argsExact(0),
		Example: `$ gt webhook list
$ gt webhook list --org acme
# print the delivery URLs
$ gt webhook list --jq '.[].url'`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, service, err := scope.service(a, cmd)
			if err != nil {
				return err
			}
			hooks, err := service.List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			where := webhookScopeName(service.Scope)
			return writeTable(a, cmd, hooks.Items, listSpec[services.Webhook]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Type"}, {Header: "Active"}, {Header: "URL", Flex: true}, {Header: "Events", Flex: true, TTYOnly: true}, {Header: "Events", PipedOnly: true}},
				Row: func(hook services.Webhook) []terminal.Cell {
					active := terminal.Cell{Text: yesNo(hook.Active), Color: "green"}
					if !hook.Active {
						active = terminal.Cell{Text: "no", Muted: true}
					}
					return []terminal.Cell{
						{Text: strconv.FormatInt(hook.ID, 10), Link: webhookSettingsURL(target, service.Scope, hook.ID)},
						{Text: hook.Type},
						active,
						{Text: hook.URL},
						{Text: collapsedEvents(hook.Events)},
						{Text: strings.Join(hook.Events, ",")},
					}
				},
				Context: "webhooks of " + where, Empty: "no webhooks for " + where,
			}, servicePage(hooks, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return cmd
}

func (a *application) webhookViewCommand(scope *webhookScope) *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use: "view <id>", Short: "View a webhook", Args: argsExact(1), ValidArgsFunction: a.completeWebhooks(scope),
		Example: `$ gt webhook view 3
# show the delivery URL with the credentials it carries
$ gt webhook view 3 --org acme --reveal-url`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, service, err := scope.service(a, cmd)
			if err != nil {
				return err
			}
			service.RevealURL = reveal
			hook, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			d := a.display()
			return writeView(a, cmd, hook, func(hook services.Webhook) terminal.View {
				active := terminal.Cell{Text: "active", Color: "green"}
				if !hook.Active {
					active = terminal.Cell{Text: "inactive", Muted: true}
				}
				updated := timeField("Updated", hook.UpdatedAt, d)
				meta := ""
				if updated.Value != "" {
					meta = "updated " + updated.Value
				}
				updated.PipedOnly = true
				return terminal.View{
					Title: hook.URL, ID: fmt.Sprintf("(webhook %d)", hook.ID), Badges: []terminal.Cell{active, {Text: hook.Type}}, Meta: meta,
					Fields: []terminal.Field{
						{Label: "Active", Value: strconv.FormatBool(hook.Active), PipedOnly: true},
						{Label: "Type", Value: hook.Type, PipedOnly: true},
						{Label: "Scope", Value: webhookScopeName(service.Scope)},
						{Label: "Events", Value: strings.Join(hook.Events, ", ")},
						{Label: "Content type", Value: hook.ContentType},
						{Label: "Branch filter", Value: hook.BranchFilter},
						timeField("Created", hook.CreatedAt, d), updated,
					},
					Bodyless: true, Kind: "webhook", URL: webhookSettingsURL(target, service.Scope, hook.ID),
				}
			})
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal-url", false, "Show the delivery URL whole, with any credentials it carries")
	return cmd
}

// webhookChangeCommand creates or edits a webhook.
func (a *application) webhookChangeCommand(scope *webhookScope, create bool) *cobra.Command {
	var endpoint, kind, secret, secretFile, authorization, authorizationFile, filter string
	var events []string
	var config map[string]string
	var active, clearAuthorization bool
	command := &cobra.Command{
		Use: "edit <id>", Short: "Edit a webhook", Args: argsExact(1), ValidArgsFunction: a.completeWebhooks(scope),
		Long: `Edit a webhook. Only the settings you pass change.

Gitea drops the stored authorization header of a webhook on every edit and never shows it, so
pass it again with --authorization-header-file. On a terminal, gt asks for it instead; leave
the answer empty for a webhook without one. Signing secrets cannot be changed: recreate the
webhook to rotate one.`,
		Example: `# pause deliveries of a webhook without an authorization header
$ gt webhook edit 3 --active=false --clear-authorization
# keep the authorization header while changing the events
$ gt webhook edit 3 --events push,release --authorization-header-file header.txt`,
	}
	eventsUsage := "Events that trigger the webhook, comma-separated"
	if create {
		command.Use, command.Short, command.ValidArgsFunction = "create [<url>]", "Create a webhook", nil
		command.Args = usageArgs(cobra.MaximumNArgs(1))
		command.Long = `Create a webhook that delivers events to a URL, by default as JSON for push events.

Events: ` + strings.Join(services.WebhookEvents, ", ") + `; issues and pull_request
include every issue or pull request event.

Types: ` + strings.Join(services.WebhookTypes, ", ") + `. Slack webhooks need
--config channel=#name; other types take their settings through --config too.`
		command.Example = `$ gt webhook create https://ci.example.com/hooks/gitea --events push,pull_request
# sign deliveries with a secret read from a file
$ gt webhook create https://ci.example.com/hooks/gitea --secret-file secret.txt
# notify a Slack channel about releases of an organization's repositories
$ gt webhook create https://hooks.slack.com/services/T0/B0/X --org acme --type slack --config channel=#releases --events release`
		enumFlag(command, &kind, "type", "", "gitea", "Webhook adapter type", services.WebhookTypes...)
		command.Flags().StringSliceVar(&events, "events", []string{"push"}, eventsUsage)
		command.Flags().BoolVar(&active, "active", true, "Deliver events (use --active=false to create the webhook paused)")
	} else {
		command.Flags().StringSliceVar(&events, "events", nil, eventsUsage+", replacing the current ones (see gt webhook create --help)")
		command.Flags().BoolVar(&active, "active", false, "Deliver events, or pause the webhook with --active=false")
	}
	enumValues(command, "events", services.WebhookEvents...)
	command.Flags().StringVar(&endpoint, "url", "", "Delivery URL")
	command.Flags().StringVar(&secret, "secret", "", "Signing secret (prefer --secret-file)")
	command.Flags().StringVar(&secretFile, "secret-file", "", "Read signing secret from file or -")
	command.Flags().StringVar(&filter, "branch-filter", "", "Branch glob for push events")
	command.Flags().StringVar(&authorization, "authorization-header", "", "Authorization value (prefer file input)")
	command.Flags().StringVar(&authorizationFile, "authorization-header-file", "", "Read authorization value from file or -")
	command.Flags().BoolVar(&clearAuthorization, "clear-authorization", false, "Send no authorization header; on edit, this removes the stored one")
	command.Flags().StringToStringVar(&config, "config", nil, "Adapter-specific configuration, for example channel=alerts")
	if !create {
		// Gitea cannot change signing secrets; the flags stay to explain that.
		_ = command.Flags().MarkHidden("secret")
		_ = command.Flags().MarkHidden("secret-file")
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		var id int64
		var err error
		opts := services.WebhookOptions{}
		if !create {
			if id, err = number(args[0]); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("secret") && cmd.Flags().Changed("secret-file") {
			return clierr.Wrap(clierr.Usage, errors.New("--secret and --secret-file are mutually exclusive"))
		}
		authSources := 0
		for _, set := range []bool{cmd.Flags().Changed("authorization-header"), cmd.Flags().Changed("authorization-header-file"), clearAuthorization} {
			if set {
				authSources++
			}
		}
		if authSources > 1 {
			return clierr.Wrap(clierr.Usage, errors.New("choose one authorization header source or --clear-authorization"))
		}
		if secretFile == "-" && authorizationFile == "-" {
			return clierr.Wrap(clierr.Usage, errors.New("stdin can supply only one webhook credential"))
		}
		if !create && (cmd.Flags().Changed("secret") || cmd.Flags().Changed("secret-file")) {
			return clierr.Wrap(clierr.Usage, &services.CapabilityError{Feature: "webhook secret editing", Reason: "Gitea does not update signing secrets through its edit API; recreate the hook to rotate it"})
		}
		if len(args) > 0 && create {
			if cmd.Flags().Changed("url") {
				return clierr.Wrap(clierr.Usage, errors.New("supply the URL as an argument or --url, not both"))
			}
			endpoint = args[0]
		}
		if create || cmd.Flags().Changed("events") {
			trimNames(events)
			if len(events) == 0 || slices.Contains(events, "") {
				return clierr.Wrap(clierr.Usage, errors.New("--events needs at least one trigger"))
			}
			opts.Events = &events
		}
		opts.Config = make(map[string]string, len(config)+4)
		for key, value := range config {
			if key == "url" || key == "secret" || key == "is_system_webhook" {
				return clierr.Wrap(clierr.Usage, errors.New("--config cannot override the URL, secret, or webhook scope"))
			}
			if key == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--config keys cannot be empty"))
			}
			opts.Config[key] = value
		}
		if create && kind == "slack" && config["channel"] == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--type slack needs a channel: --config channel=#name"))
		}
		if endpoint != "" || cmd.Flags().Changed("url") {
			if err := validateWebhookURL(endpoint); err != nil {
				return err
			}
		}
		if create {
			if _, err := a.requireInput(cmd, &endpoint, "url", "Payload URL", validateWebhookURL); err != nil {
				return err
			}
		}
		if secret, err = a.readSecret(cmd, secret, secretFile, "secret", "secret-file"); err != nil {
			return err
		}
		if authorization, err = a.readSecret(cmd, authorization, authorizationFile, "authorization-header", "authorization-header-file"); err != nil {
			return err
		}
		askAuthorization := !create && authSources == 0
		if askAuthorization {
			if !a.canPrompt() {
				return clierr.Wrap(clierr.Usage, errors.New("editing a webhook drops its stored authorization header unless it is sent again, and Gitea never returns it: pass --authorization-header-file or --authorization-header (use --clear-authorization only for a hook without one)"))
			}
		}
		if strings.ContainsAny(authorization, "\r\n") {
			return clierr.Wrap(clierr.Usage, errors.New("authorization header must be a single line"))
		}
		if endpoint != "" {
			opts.Config["url"] = endpoint
		}
		if create {
			opts.Type = kind
			opts.Active = &active
			opts.BranchFilter = &filter
			opts.Config["http_method"] = "post"
			if _, ok := opts.Config["content_type"]; !ok {
				opts.Config["content_type"] = "json"
			}
		}
		if cmd.Flags().Changed("secret") || cmd.Flags().Changed("secret-file") {
			opts.Config["secret"] = secret
		}
		if cmd.Flags().Changed("active") {
			opts.Active = &active
		}
		if cmd.Flags().Changed("branch-filter") {
			opts.BranchFilter = &filter
		}
		if authSources > 0 {
			opts.Authorization = &authorization
		}
		if len(opts.Config) == 0 {
			opts.Config = nil
		}
		target, service, err := scope.service(a, cmd)
		if err != nil {
			return err
		}
		if askAuthorization {
			hook, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			title := fmt.Sprintf("Re-enter the authorization header of webhook %d (empty if it has none)", hook.ID)
			if authorization, err = a.deps.Prompter.SecretInput(cmd.Context(), title); err != nil {
				return err
			}
			opts.Authorization = &authorization
		}
		if !create {
			hook, err := service.Edit(cmd.Context(), id, opts)
			if err != nil {
				return err
			}
			a.done(cmd, "Edited %s in %s", webhookLabel(hook), webhookScopeName(service.Scope))
			return a.jsonResult(cmd, hook)
		}
		hook, err := service.Create(cmd.Context(), opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Created %s in %s for %s", webhookLabel(hook), webhookScopeName(service.Scope), collapsedEvents(hook.Events))
		return a.created(cmd, hook, webhookSettingsURL(target, service.Scope, hook.ID))
	}
	return command
}

func (a *application) webhookDeleteCommand(scope *webhookScope) *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete a webhook", Args: argsExact(1), ValidArgsFunction: a.completeWebhooks(scope),
		Example: `$ gt webhook delete 3
$ gt webhook delete 4 --org acme --yes`,
		Annotations: map[string]string{"gt.text": "webhook deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			_, service, err := scope.service(a, cmd)
			if err != nil {
				return err
			}
			hook, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.deleteConfirmed(cmd, webhookLabel(hook)+" from "+webhookScopeName(service.Scope), func() error { return service.Delete(cmd.Context(), id) })
		},
	}
}
