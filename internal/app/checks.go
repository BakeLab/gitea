// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullChecksCommand() *cobra.Command {
	var watch bool
	var interval, timeout time.Duration
	cmd := &cobra.Command{Use: "checks <number>", Short: "Show the latest commit-status checks for a pull request", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if interval <= 0 || timeout <= 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
		}
		n, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: target.client, Repository: target.Repo}
		var items []services.Check
		if watch {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			items, err = service.WatchChecks(ctx, n, interval, func(items []services.Check) error {
				pending := 0
				for _, item := range items {
					if item.Pending() {
						pending++
					}
				}
				_, err := fmt.Fprintf(cmd.ErrOrStderr(), "checks: %d total, %d pending\n", len(items), pending)
				return err
			})
		} else {
			items, err = service.Checks(cmd.Context(), n)
		}
		if err != nil {
			return err
		}
		if err := writeList(a, cmd, items, func(item services.Check) []terminal.Cell { return terminal.Row(item.Name, item.State, item.URL) }); err != nil {
			return err
		}
		for _, item := range items {
			if !item.Passed() {
				return errors.New("pull request checks have not all succeeded")
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&watch, "watch", false, "Wait for checks to finish, failing immediately on unsuccessful status")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "Check polling interval")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Hour, "Maximum wait duration")
	return cmd
}
