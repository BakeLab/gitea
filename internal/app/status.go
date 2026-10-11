// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strconv"

	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) statusCommand(pullsOnly bool) *cobra.Command {
	var limit int
	var runs bool
	short := "Show assigned issues, pull requests, and selected repository failures"
	if pullsOnly {
		short = "Show your open authored, assigned, and requested-review pull requests"
	}
	command := &cobra.Command{Use: "status", Short: short, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		service := services.Status{Client: target.client, Repository: target.Repo}
		if pullsOnly {
			result, err := service.Pulls(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, result)
			}
			return a.writeHuman(cmd, pullStatusText(result, a.colorEnabled()))
		}
		result, err := service.Summary(cmd.Context(), limit, runs)
		if err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, result)
		}
		color := a.colorEnabled()
		text := fmt.Sprintf("%s on %s\n", result.User, result.Host) + statusSection("Assigned issues", result.AssignedIssues, statusRow, color) + pullStatusText(result.PullRequests, color)
		if result.RunsIncluded {
			text += statusSection("Failing runs in "+result.Repository, result.FailingRuns, func(run services.Run) []terminal.Cell {
				return terminal.Row(strconv.FormatInt(run.ID, 10), run.Title, run.Conclusion, run.URL)
			}, color)
		}
		return a.writeHuman(cmd, text)
	}}
	positiveIntVar(command, &limit, "limit", 30, 100, "Maximum results in each category (1..100)")
	if !pullsOnly {
		command.Flags().BoolVar(&runs, "runs", true, "Include failed or timed-out Actions runs when --repo or TEA_REPO is selected")
	}
	return command
}

func pullStatusText(result services.PullStatus, color bool) string {
	return statusSection("Authored pull requests", result.Authored, statusRow, color) +
		statusSection("Assigned pull requests", result.Assigned, statusRow, color) +
		statusSection("Review requested", result.ReviewRequested, statusRow, color)
}

// statusSection renders a heading over an indented table, or None when items is empty.
func statusSection[T any](name string, items []T, row func(T) []terminal.Cell, color bool) string {
	if len(items) == 0 {
		return name + "\n  None\n"
	}
	return name + "\n" + table(items, func(item T) []terminal.Cell {
		cells := row(item)
		cells[0].Text = "  " + cells[0].Text
		return cells
	}, color)
}

func statusRow(item services.SearchItem) []terminal.Cell {
	return terminal.Row(fmt.Sprintf("%s#%d", item.Repository, item.Number), item.Title, item.URL)
}
