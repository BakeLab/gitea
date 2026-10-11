// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) notificationCommand() *cobra.Command {
	group := &cobra.Command{Use: "notification", Short: "Manage account notifications"}
	var mine bool
	group.PersistentFlags().BoolVar(&mine, "mine", false, "Use all your notifications instead of repository notifications")
	service := func(cmd *cobra.Command, requireRepo bool) (services.Notifications, error) {
		if mine && a.options.repo != "" {
			return services.Notifications{}, clierr.Wrap(clierr.Usage, errors.New("--mine and --repo are mutually exclusive"))
		}
		target, err := a.resolve(cmd.Context(), requireRepo && !mine)
		if err != nil {
			return services.Notifications{}, err
		}
		repository := ""
		if (requireRepo || a.options.repo != "") && !mine {
			repository = target.Repo
		}
		return services.Notifications{Client: target.client, Repository: repository}, nil
	}
	for _, verb := range []string{"list", "watch"} {
		opts := services.NotificationOptions{}
		var updated timeRange
		var interval, timeout time.Duration
		command := &cobra.Command{Use: verb, Short: titleCase(verb) + " notifications", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateNotificationStatuses(opts.Statuses); err != nil {
				return err
			}
			for i, kind := range opts.Types {
				kind = strings.ToLower(kind)
				if !slices.Contains([]string{"issue", "pull", "commit", "repository"}, kind) {
					return clierr.Wrap(clierr.Usage, errors.New("--type must contain issue, pull, commit, or repository"))
				}
				opts.Types[i] = titleCase(kind)
			}
			var err error
			if opts.Since, opts.Before, err = updated.parse(a.deps.Now()); err != nil {
				return err
			}
			if verb == "watch" && (interval <= 0 || timeout <= 0) {
				return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
			}
			s, err := service(cmd, true)
			if err != nil {
				return err
			}
			if verb == "list" {
				items, err := s.List(cmd.Context(), opts)
				if err != nil {
					return err
				}
				return writeList(a, cmd, items, notificationRow)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			results := []services.Notification{}
			color := a.colorEnabled()
			err = s.Watch(ctx, opts, interval, func(items []services.Notification) error {
				if a.options.json != "" {
					results = append(results, items...)
					return nil
				}
				_, err := io.WriteString(cmd.OutOrStdout(), table(items, notificationRow, color))
				return err
			})
			if errors.Is(err, context.DeadlineExceeded) && cmd.Context().Err() == nil {
				err = nil
			}
			if a.options.json != "" {
				if outputErr := a.writeJSON(cmd, results); outputErr != nil {
					return outputErr
				}
			}
			return err
		}}
		pageFlags(command, &opts.Limit, &opts.Page)
		command.Flags().StringSliceVar(&opts.Statuses, "status", []string{"unread", "pinned"}, "Filter unread, read, or pinned")
		command.Flags().StringSliceVar(&opts.Types, "type", nil, "Filter issue, pull, commit, or repository")
		updated.register(command, "since", "Updated since a date, timestamp, or duration such as 24h", "before", "Updated before a date, timestamp, or duration such as 1h")
		if verb == "watch" {
			command.Flags().DurationVar(&interval, "interval", 5*time.Second, "Polling interval")
			command.Flags().DurationVar(&timeout, "timeout", time.Minute, "Maximum watch duration")
		}
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{Use: "view <id>", Short: "View a notification thread", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		s, err := service(cmd, false)
		if err != nil {
			return err
		}
		item, err := s.View(cmd.Context(), id)
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, notificationRow)
	}})
	for _, mark := range []struct{ verb, status, filter string }{
		{"mark-read", "read", "unread"}, {"mark-unread", "unread", "read"}, {"pin", "pinned", "unread"}, {"unpin", "read", "pinned"},
	} {
		verb := mark.verb
		var all bool
		var filters []string
		var before string
		command := &cobra.Command{Use: verb + " [id]...", Short: titleCase(verb) + " selected or filtered notifications", Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return clierr.Wrap(clierr.Usage, errors.New("supply notification IDs or --all"))
			}
			if len(args) > 0 && all {
				return clierr.Wrap(clierr.Usage, errors.New("notification IDs and --all are mutually exclusive"))
			}
			return nil
		}, RunE: func(cmd *cobra.Command, args []string) error {
			numbers, err := resourceNumbers(args)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("status") {
				filters = []string{mark.filter}
			}
			if err := validateNotificationStatuses(filters); err != nil {
				return err
			}
			var until time.Time
			if cmd.Flags().Changed("before") {
				parsed, err := parseRunTime(before, a.deps.Now())
				if err != nil {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --before: %w", err))
				}
				until = *parsed
			}
			if !all && (cmd.Flags().Changed("status") || cmd.Flags().Changed("before")) {
				return clierr.Wrap(clierr.Usage, errors.New("--status and --before require --all"))
			}
			s, err := service(cmd, all)
			if err != nil {
				return err
			}
			if !all {
				return batch(a, cmd, numbers, func(id int64) string { return fmt.Sprintf("notification %d", id) }, func(id int64) (services.Notification, error) {
					return s.Mark(cmd.Context(), id, mark.status)
				}, notificationRow)
			}
			results, err := s.MarkAll(cmd.Context(), mark.status, filters, until)
			if err != nil {
				return err
			}
			return writeList(a, cmd, results, notificationRow)
		}}
		command.Flags().BoolVar(&all, "all", false, "Apply to all notifications matching filters in the selected scope")
		command.Flags().StringSliceVar(&filters, "status", nil, "Filter statuses for --all")
		command.Flags().StringVar(&before, "before", "", "Updated before a date, timestamp, or duration (requires --all)")
		group.AddCommand(command)
	}
	return group
}

func validateNotificationStatuses(statuses []string) error {
	for _, status := range statuses {
		if !slices.Contains([]string{"unread", "read", "pinned"}, status) {
			return clierr.Wrap(clierr.Usage, errors.New("--status must contain unread, read, or pinned"))
		}
	}
	return nil
}

func notificationRow(item services.Notification) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Repository, item.Title, item.URL)
}
