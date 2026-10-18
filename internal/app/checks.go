// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
)

func (a *application) pullChecksCommand() *cobra.Command {
	var watch, web bool
	var interval, timeout time.Duration
	cmd := &cobra.Command{Use: "checks [<number> | <url> | <branch>]", Short: "Show the status checks of a pull request", Long: `Show the latest status of each check of a pull request's head commit.

gt exits with 8 while checks are pending and with 1 when a check failed, like gh. With
--watch, it waits until every check finished, or one failed.`, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if interval <= 0 || timeout <= 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
		}
		t, n, err := a.pullSelectorAny(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		if web {
			item, err := service.View(cmd.Context(), n)
			if err != nil {
				return err
			}
			return a.openBrowser(cmd, item.URL)
		}
		var items []services.Check
		if watch {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			redraw := a.checksRedraw(cmd, interval)
			var latest []services.Check
			items, err = service.WatchChecks(ctx, n, interval, func(current []services.Check) error {
				latest = current
				return redraw(current)
			})
			if err != nil {
				waiting := "checks to be reported"
				if len(latest) > 0 {
					_, pending, _ := services.Tally(latest)
					waiting = terminal.Plural(pending, "pending check")
				}
				return timeoutError(err, timeout, "waiting for %s", waiting)
			}
			if a.deps.Streams.OutTTY && a.options.json == "" {
				return checksOutcome(items)
			}
		} else if items, err = service.Checks(cmd.Context(), n); err != nil {
			return err
		}
		if a.options.json != "" {
			if err := a.writeJSON(cmd, items); err != nil {
				return err
			}
			return checksOutcome(items)
		}
		if len(items) == 0 {
			if a.deps.Streams.OutTTY {
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "no checks reported for pull request %s#%d\n", t.Repo, n)
			}
			return err
		}
		if err := a.writeHuman(cmd, a.checksText(items)); err != nil {
			return err
		}
		return checksOutcome(items)
	}}
	cmd.Flags().BoolVar(&watch, "watch", false, "Wait for the checks to finish, stopping at the first failure")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Second, "Time between refreshes with --watch")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Hour, "Longest time to wait with --watch")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the pull request in the browser")
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	cmd.Example = `# Show the checks of the current branch's pull request
$ gt pr checks

# Wait until the checks of #13 finish, then merge it
$ gt pr checks 13 --watch && gt pr merge 13`
	return cmd
}

// checksOutcome is the exit status of checks: 1 when one failed, 8 while any is pending.
func checksOutcome(items []services.Check) error {
	switch failing, pending, _ := services.Tally(items); {
	case failing > 0:
		return clierr.Quiet(clierr.Failure)
	case pending > 0:
		return clierr.Quiet(clierr.Pending)
	}
	return nil
}

// checkCounts words how the checks stand: "0 failing, 1 successful, and 2 pending checks".
func checkCounts(items []services.Check) string {
	failing, pending, passed := services.Tally(items)
	return fmt.Sprintf("%d failing, %d successful, and %d pending checks", failing, passed, pending)
}

// checksText is the human summary of checks: a colored verdict, the counts and a table.
func (a *application) checksText(items []services.Check) string {
	d := a.display()
	verdict, color := "All checks were successful", "green"
	switch err := checksOutcome(items); {
	case clierr.ExitCode(err) == clierr.Failure:
		verdict, color = "Some checks were not successful", "red"
	case err != nil:
		verdict, color = "Some checks are still pending", "yellow"
	}
	rows := make([][]terminal.Cell, len(items))
	for i, item := range items {
		rows[i] = []terminal.Cell{terminal.StatusIcon(item.State, ""), {Text: item.Name}, {Text: item.State}, {Text: item.Description}, {Text: item.URL}}
	}
	columns := []terminal.Column{{TTYOnly: true}, {Header: "Name", Flex: true, Min: 16}, {Header: "Status"}, {Header: "Description", Flex: true}, {Header: "Link"}}
	table := terminal.Table(columns, rows, d)
	if !d.TTY {
		return table
	}
	return terminal.Colorize(color, verdict, d.Color) + "\n" + checkCounts(items) + "\n\n" + table
}

// checksRedraw reports each change of the checks while --watch waits: on a terminal it redraws the
// summary in place; otherwise it writes a line of counts to stderr.
func (a *application) checksRedraw(cmd *cobra.Command, interval time.Duration) func([]services.Check) error {
	if !a.deps.Streams.OutTTY || a.options.json != "" {
		return func(items []services.Check) error {
			_, err := fmt.Fprintf(cmd.ErrOrStderr(), "checks: %s\n", checkCounts(items))
			return err
		}
	}
	lines := 0
	return func(items []services.Check) error {
		text := "Refreshing every " + shortDuration(interval) + ". Press Ctrl+C to quit.\n\n"
		if len(items) == 0 {
			text += "Waiting for checks to be reported…\n"
		} else {
			text += a.checksText(items)
		}
		clear := ""
		if lines > 0 {
			clear = fmt.Sprintf("\x1b[%dA\x1b[J", lines)
		}
		lines = screenRows(text, a.deps.Streams.Width)
		_, err := io.WriteString(cmd.OutOrStdout(), clear+text)
		return err
	}
}

// displayEscapes matches the color and hyperlink sequences that take no room on screen.
var displayEscapes = regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;[^\x07\x1b]*(?:\x07|\x1b\\\\)")

// screenRows counts the rows the cursor sits below the start of text on a terminal width columns wide,
// where a line wider than the terminal wraps onto more rows; a width of 0 means unlimited.
func screenRows(text string, width int) int {
	rows := strings.Count(text, "\n")
	if width <= 0 {
		return rows
	}
	for _, line := range strings.Split(text, "\n") {
		if cells := runewidth.StringWidth(displayEscapes.ReplaceAllString(line, "")); cells > width {
			rows += (cells - 1) / width
		}
	}
	return rows
}
