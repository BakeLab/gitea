// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func validateWebhookURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return clierr.Wrap(clierr.Usage, errors.New("webhook URL must be an absolute HTTP(S) URL without user information or fragment"))
	}
	return nil
}

func (a *application) webhookCommand() *cobra.Command {
	group := &cobra.Command{Use: "webhook", Short: "Manage webhook delivery settings"}
	var org string
	var account, global bool
	group.PersistentFlags().StringVar(&org, "org", "", "Organization scope")
	group.PersistentFlags().BoolVar(&account, "account", false, "Current account scope")
	group.PersistentFlags().BoolVar(&global, "global", false, "System webhook scope (administrator)")
	scope := func(cmd *cobra.Command) (services.Webhooks, error) {
		scopes := 0
		for _, selected := range []bool{org != "", account, global, a.options.repo != ""} {
			if selected {
				scopes++
			}
		}
		if scopes > 1 {
			return services.Webhooks{}, clierr.Wrap(clierr.Usage, errors.New("--org, --account, --global, and --repo select mutually exclusive scopes"))
		}
		target, err := a.resolve(cmd.Context(), org == "" && !account && !global)
		return services.Webhooks{Client: target.client, Scope: services.WebhookScope{Repository: target.Repo, Organization: org, Account: account, Global: global}}, err
	}
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List webhook metadata (delivery credentials are omitted)", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		service, err := scope(cmd)
		if err != nil {
			return err
		}
		items, err := service.List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, webhookRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var endpoint, kind, secret, secretFile, authorization, authorizationFile, filter string
		var events []string
		var config map[string]string
		var active, clearAuthorization bool
		command := &cobra.Command{Use: verb + " <id>", Short: titleCase(verb) + " a webhook", Args: argsExact(1)}
		if verb == "create" {
			command.Use = "create [url]"
			command.Args = usageArgs(cobra.MaximumNArgs(1))
		}
		if verb == "create" || verb == "edit" {
			command.Flags().StringVar(&endpoint, "url", "", "Delivery URL")
			command.Flags().StringVar(&secret, "secret", "", "Signing secret (prefer --secret-file)")
			command.Flags().StringVar(&secretFile, "secret-file", "", "Read signing secret from file or -")
			command.Flags().StringSliceVar(&events, "events", []string{"push"}, "Comma-separated trigger events")
			command.Flags().BoolVar(&active, "active", true, "Enable delivery (use --active=false to disable)")
			command.Flags().StringVar(&filter, "branch-filter", "", "Branch glob for push events")
			command.Flags().StringVar(&authorization, "authorization-header", "", "Authorization value (prefer file input)")
			command.Flags().StringVar(&authorizationFile, "authorization-header-file", "", "Read authorization value from file or -")
			command.Flags().BoolVar(&clearAuthorization, "clear-authorization", false, "Explicitly clear delivery authorization")
			command.Flags().StringToStringVar(&config, "config", nil, "Adapter-specific configuration, for example channel=alerts")
			if verb == "create" {
				command.Flags().StringVar(&kind, "type", "gitea", "Webhook adapter type")
			}
		}
		if verb == "delete" {
			command.Annotations = textOutput("webhook deletion does not return a resource")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			var id int64
			var err error
			prompted := false
			opts := services.WebhookOptions{}
			if verb != "create" {
				id, err = number(args[0])
				if err != nil {
					return err
				}
			}
			if verb == "create" || verb == "edit" {
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
				if verb == "edit" && (cmd.Flags().Changed("secret") || cmd.Flags().Changed("secret-file")) {
					return clierr.Wrap(clierr.Usage, &services.CapabilityError{Feature: "webhook secret editing", Reason: "Gitea does not update signing secrets through its edit API; recreate the hook to rotate it"})
				}
				if verb == "edit" && authSources == 0 {
					return clierr.Wrap(clierr.Usage, errors.New("editing requires --authorization-header, --authorization-header-file, or --clear-authorization because Gitea replaces authorization"))
				}
				if len(args) > 0 && verb == "create" {
					if cmd.Flags().Changed("url") {
						return clierr.Wrap(clierr.Usage, errors.New("supply the URL as an argument or --url, not both"))
					}
					endpoint = args[0]
				}
				if verb == "create" {
					prompted, err = a.require(cmd, &endpoint, "url")
					if err != nil {
						return err
					}
				}
				if endpoint != "" || cmd.Flags().Changed("url") {
					if err := validateWebhookURL(endpoint); err != nil {
						return err
					}
				}
				if verb == "create" || cmd.Flags().Changed("events") {
					if len(events) == 0 {
						return clierr.Wrap(clierr.Usage, errors.New("--events needs at least one trigger"))
					}
					for i, event := range events {
						events[i] = strings.TrimSpace(event)
						if events[i] == "" {
							return clierr.Wrap(clierr.Usage, errors.New("--events cannot contain empty triggers"))
						}
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
				secret, err = a.readSecret(cmd, secret, secretFile, "secret", "secret-file")
				if err != nil {
					return err
				}
				authorization, err = a.readSecret(cmd, authorization, authorizationFile, "authorization-header", "authorization-header-file")
				if err != nil {
					return err
				}
				if strings.ContainsAny(authorization, "\r\n") {
					return clierr.Wrap(clierr.Usage, errors.New("authorization header must be a single line"))
				}
				if endpoint != "" {
					opts.Config["url"] = endpoint
				}
				if verb == "create" {
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
			}
			service, err := scope(cmd)
			if err != nil {
				return err
			}
			if verb == "delete" || prompted {
				subject := endpoint
				if verb == "delete" {
					subject = args[0]
				}
				if err := a.confirm(cmd, verb+" webhook "+subject); err != nil {
					return err
				}
			}
			var item services.Webhook
			switch verb {
			case "view":
				item, err = service.View(cmd.Context(), id)
			case "create":
				item, err = service.Create(cmd.Context(), opts)
			case "edit":
				item, err = service.Edit(cmd.Context(), id, opts)
			case "delete":
				return service.Delete(cmd.Context(), id)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, webhookRow)
		}
		group.AddCommand(command)
	}
	return group
}

func webhookRow(item services.Webhook) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Type, strconv.FormatBool(item.Active), strings.Join(item.Events, ","))
}
