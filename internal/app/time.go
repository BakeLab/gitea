// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func trackedDuration(seconds int64) time.Duration { return time.Duration(seconds) * time.Second }

func (a *application) timeCommand() *cobra.Command {
	group := &cobra.Command{Use: "time", Short: "Track time spent on issues and pull requests"}
	opts := services.TimeOptions{}
	var tracked timeRange
	var total bool
	list := &cobra.Command{Use: "list", Short: "List repository or account time records", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if opts.Issue < 0 || (cmd.Flags().Changed("issue") && opts.Issue == 0) {
			return clierr.Wrap(clierr.Usage, errors.New("--issue must be positive"))
		}
		if (opts.Mine && (cmd.Flags().Changed("issue") || opts.Username != "")) || (opts.Issue != 0 && opts.Username != "") {
			return clierr.Wrap(clierr.Usage, errors.New("--mine, --issue and --username select mutually exclusive scopes"))
		}
		if total && a.options.json != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--total is a human-output summary; use --jq 'map(.seconds) | add // 0' for JSON"))
		}
		var err error
		if opts.Since, opts.Before, err = tracked.parse(a.deps.Now()); err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), !opts.Mine)
		if err != nil {
			return err
		}
		items, err := (services.Times{Client: target.client, Repository: target.Repo}).List(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if !total {
			return writeList(a, cmd, items, trackedTimeRow)
		}
		var sum int64
		for _, item := range items {
			sum += item.Seconds
		}
		return a.writeHuman(cmd, table(items, trackedTimeRow, a.colorEnabled())+fmt.Sprintf("Total on this page: %s\n", trackedDuration(sum)))
	}}
	list.Flags().BoolVar(&opts.Mine, "mine", false, "List your time across repositories")
	list.Flags().Int64Var(&opts.Issue, "issue", 0, "List time on one issue or pull request")
	list.Flags().StringVar(&opts.Username, "username", "", "Filter repository time by username")
	pageFlags(list, &opts.Limit, &opts.Page)
	tracked.register(list, "from", "Only time tracked since a date, timestamp, or duration ago", "until", "Only time tracked before a date, timestamp, or duration ago")
	list.Flags().BoolVar(&total, "total", false, "Print the time summed across the returned page")
	group.AddCommand(list)
	group.AddCommand(&cobra.Command{Use: "create <number> <duration>", Short: "Record spent time, for example 1h25m", Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		duration, err := time.ParseDuration(args[1])
		if err != nil || duration == 0 || duration%time.Second != 0 {
			return clierr.Wrap(clierr.Usage, errors.New("duration must be nonzero whole seconds, for example 1h25m"))
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Times{Client: target.client, Repository: target.Repo}).Create(cmd.Context(), n, int64(duration/time.Second))
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, trackedTimeRow)
	}})
	for _, verb := range []string{"delete", "reset"} {
		use := verb + " <number>"
		count := 1
		if verb == "delete" {
			use += " <time-id>"
			count = 2
		}
		group.AddCommand(&cobra.Command{Use: use, Short: titleCase(verb) + " tracked time on an issue", Annotations: textOutput("time " + verb + " does not return a resource"), Args: argsExact(count), RunE: func(cmd *cobra.Command, args []string) error {
			n, err := number(args[0])
			if err != nil {
				return err
			}
			var id int64
			if verb == "delete" {
				id, err = number(args[1])
				if err != nil {
					return err
				}
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			subject := target.Repo + " #" + args[0]
			if verb == "delete" {
				subject = "entry " + args[1] + " on " + subject
			}
			if err := a.confirm(cmd, verb+" tracked time "+subject); err != nil {
				return err
			}
			service := services.Times{Client: target.client, Repository: target.Repo}
			if verb == "delete" {
				return service.Delete(cmd.Context(), n, id)
			}
			return service.Reset(cmd.Context(), n)
		}})
	}
	return group
}

func trackedTimeRow(item services.TrackedTime) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), fmt.Sprintf("%s#%d", item.Repository, item.IssueNumber), item.User, trackedDuration(item.Seconds).String(), item.CreatedAt)
}
