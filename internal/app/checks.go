// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullChecksCommand() *cobra.Command {
	var watch, web bool
	var interval, timeout time.Duration
	cmd := &cobra.Command{Use: "checks [<number> | <url> | <branch>]", Short: "Show the status checks of a pull request", Long: `Show the latest status of each check of a pull request's head commit.

gt exits with 8 while checks are pending and with 1 when a check failed, like gh. With
--watch, it waits until every check finished, or one failed, and polls on through an outage of
the instance of up to 5 minutes.`, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
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
			redraw, leave := a.checksRedraw(cmd, interval)
			defer leave()
			var latest []services.Check
			items, err = service.WatchChecks(ctx, n, interval, func(current []services.Check) error {
				latest = current
				return redraw(current)
			})
			live := leave()
			if err != nil {
				waiting := "checks to be reported"
				if len(latest) > 0 {
					_, pending, _ := services.Tally(latest)
					waiting = terminal.Plural(pending, "pending check")
					if live && expired(err) {
						// The live view left with the alternate screen; not through the pager, which
						// would stop the command at its end.
						_, _ = io.WriteString(cmd.OutOrStdout(), a.checksText(latest))
					}
				}
				return timeoutError(err, timeout, "waiting for %s", waiting)
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
		write := a.writeHuman
		if watch {
			// Not through the pager, which would stop the command at its end, as in
			// gt pr checks --watch && gt pr merge.
			write = func(cmd *cobra.Command, text string) error {
				_, err := io.WriteString(cmd.OutOrStdout(), text)
				return err
			}
		}
		if err := write(cmd, a.checksText(items)); err != nil {
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
	return jsonOutput(cmd, jsonFields[services.Check]())
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
		icon := terminal.StatusIcon(item.State, "")
		icon.Link = item.URL
		rows[i] = []terminal.Cell{icon, {Text: item.Name}, {Text: item.State}, {Text: item.Description}, {Text: item.URL}}
	}
	// With hyperlinks the status icon links to the check, and a terminal leaves out the long URLs
	// that would wrap every row; without them the URL is the only way to the check.
	columns := []terminal.Column{{TTYOnly: true}, {Header: "Name", Flex: true, Min: 16}, {Header: "Status"}, {Header: "Description", Flex: true}, {Header: "Link", PipedOnly: d.Links}}
	table := terminal.Table(columns, rows, d)
	if !d.TTY {
		return table
	}
	return terminal.Colorize(color, verdict, d.Color) + "\n" + checkCounts(items) + "\n\n" + table
}

// liveRedraw reports whether a watch redraws its view in place: stdout is a terminal that
// interprets escape sequences and is not dumb, and no JSON is requested.
func (a *application) liveRedraw() bool {
	return a.deps.Streams.OutEscapes() && a.deps.Getenv("TERM") != "dumb" && a.options.json == ""
}

// checksRedraw reports each change of the checks while --watch waits: on a terminal it redraws the
// summary on the alternate screen, like run watch, so a resize or a summary taller than the screen
// leaves nothing behind; otherwise it writes a line of counts to stderr. leave restores the screen
// and reports whether it was redrawing, once.
func (a *application) checksRedraw(cmd *cobra.Command, interval time.Duration) (redraw func([]services.Check) error, leave func() bool) {
	if !a.liveRedraw() {
		return func(items []services.Check) error {
			_, err := fmt.Fprintf(cmd.ErrOrStderr(), "checks: %s\n", checkCounts(items))
			return err
		}, func() bool { return false }
	}
	out := cmd.OutOrStdout()
	live := false
	return func(items []services.Check) error {
			if !live {
				live = true
				fmt.Fprint(out, "\x1b[?1049h")
			}
			text := "Refreshing every " + shortDuration(interval) + ". Press Ctrl+C to quit.\n\n"
			if len(items) == 0 {
				text += "Waiting for checks to be reported…\n"
			} else {
				text += a.checksText(items)
			}
			_, err := io.WriteString(out, "\x1b[H\x1b[J"+text)
			return err
		}, func() bool {
			was := live
			if live {
				fmt.Fprint(out, "\x1b[?1049l")
				live = false
			}
			return was
		}
}
