// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"

	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/spf13/cobra"
)

// dryRunnable adds --dry-run to cmd. A dry run withholds the write requests of the API client and
// answers them with a success, so only commands that skip their other changes, such as to the local
// clone, under it may take it. TestDryRunCommands checks each one.
func (a *application) dryRunnable(cmd *cobra.Command) *cobra.Command {
	cmd.Flags().BoolVar(&a.options.dryRun, "dry-run", false, "Show the write requests gt would send, without sending them")
	return cmd
}

// dryRunReport is what --dry-run prints.
type dryRunReport struct {
	DryRun   bool                      `json:"dry_run"`
	Requests []transport.DryRunRequest `json:"requests"`
}

// reportDryRun prints the withheld requests: as JSON with --json or --jq, else as one line each.
func (a *application) reportDryRun(requests []transport.DryRunRequest) error {
	out := a.deps.Streams.Out
	if a.options.dryRunJSON {
		return a.jsonWriter("", a.options.jq, nil).Write(out, dryRunReport{DryRun: true, Requests: requests})
	}
	for _, request := range requests {
		line := fmt.Sprintf("Dry run: would send %s %s", request.Method, request.URL)
		if len(request.Body) > 0 {
			line += " " + string(request.Body)
		}
		if _, err := fmt.Fprintln(out, terminal.SafeLine(line)); err != nil {
			return err
		}
	}
	return nil
}
