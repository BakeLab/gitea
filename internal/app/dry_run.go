// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strings"

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

// dryRunCommands are the commands, besides those that call dryRunnable themselves, that take
// --dry-run: every command that changes things on Gitea and nothing else. Commands that change the
// local clone or configuration too are left out, as TestDryRunCoversEveryWrite explains for each.
var dryRunCommands = []string{
	"admin cron run", "admin org create", "admin user badge add", "admin user badge delete", "admin user create", "admin user delete",
	"admin user edit", "admin user key create", "admin user key delete", "admin user rename",
	"api",
	"branch create", "branch protect", "branch rename", "branch unprotect",
	"gpg-key create", "gpg-key delete", "gpg-key verify",
	"issue attachment delete", "issue attachment edit", "issue attachment upload", "issue comment attachment delete",
	"issue comment", "issue comment attachment edit", "issue comment attachment upload", "issue comment create", "issue comment delete",
	"issue comment edit", "issue create", "issue delete", "issue edit", "issue lock", "issue move", "issue pin", "issue unlock", "issue unpin",
	"label create", "label edit", "label import",
	"milestone close", "milestone create", "milestone delete", "milestone edit", "milestone issue add", "milestone issue remove", "milestone reopen",
	"notification mark-read", "notification mark-unread", "notification pin", "notification unpin",
	"org create", "org delete", "org edit", "org member conceal", "org member publicize", "org member remove",
	"package delete", "package link", "package unlink", "package upload",
	"pr attachment delete", "pr attachment edit", "pr attachment upload", "pr comment attachment delete", "pr comment attachment edit",
	"pr comment", "pr comment attachment upload", "pr comment create", "pr comment delete", "pr comment edit", "pr delete", "pr edit", "pr lock", "pr pin", "pr ready",
	"pr review", "pr review cancel-request", "pr review create", "pr review delete", "pr review dismiss", "pr review reply", "pr review request",
	"pr review resolve", "pr review submit", "pr review undismiss", "pr review unresolve", "pr unlock", "pr unpin", "pr update-branch",
	"project close", "project create", "project delete", "project edit", "project reopen", "project workflow create",
	"project workflow delete", "project workflow disable", "project workflow edit", "project workflow enable",
	"release asset delete", "release asset edit", "release asset upload", "release create", "release edit", "release publish", "release upload",
	"repo archive", "repo create", "repo deploy-key add", "repo deploy-key delete", "repo edit", "repo migrate", "repo rename",
	"repo transfer", "repo unarchive",
	"run cancel", "run delete", "run rerun",
	"runner delete", "runner disable", "runner enable", "runner token",
	"secret delete", "secret set", "ssh-key create", "ssh-key delete",
	"tag create", "tag delete", "tag protect", "tag unprotect", "team create", "team delete", "team edit", "team member add", "team member remove", "team repo add", "team repo remove",
	"time cancel", "time create", "time delete", "time reset", "time start", "time stop",
	"variable delete", "variable set",
	"webhook create", "webhook delete", "webhook edit", "webhook test",
	"wiki create", "wiki delete", "wiki edit",
	"workflow disable", "workflow enable", "workflow run",
}

// addDryRun gives the commands of dryRunCommands their --dry-run flag.
func (a *application) addDryRun(root *cobra.Command) {
	for _, path := range dryRunCommands {
		if cmd, _, err := root.Find(strings.Fields(path)); err == nil && cmd != root && cmd.Flags().Lookup("dry-run") == nil {
			a.dryRunnable(cmd)
		}
	}
}
