// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) statusCommand(pullsOnly bool) *cobra.Command {
	var limit int
	var runs bool
	short, example := "Show your work across repositories", `$ gt status
$ gt status -R alice/project --runs=false`
	long := `Show the open issues and pull requests assigned to you, the reviews requested from you, and
the open discussions that mention you. In a clone, it also lists the failing Actions runs of
the clone's repository; -R scopes everything to one repository.`
	if pullsOnly {
		short, example = "Show the status of your pull requests", `$ gt pr status
$ gt pr status -R alice/project`
		long = `Show the pull request of the current branch with its checks and review state, then the open
pull requests you created, those assigned to you, and those that request your review.`
	}
	command := &cobra.Command{Use: "status", Short: short, Long: long, Example: example, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		service := services.Status{Client: t.client, Repository: t.Repo}
		d := a.display()
		if pullsOnly {
			result, err := service.Pulls(cmd.Context(), limit)
			if err != nil {
				return err
			}
			current, details, branch := a.currentPullStatus(cmd)
			result.CurrentBranch = current
			if a.options.json != "" {
				return a.writeJSON(cmd, result)
			}
			return a.writeHuman(cmd, strings.TrimSuffix(currentBranchSection(d, branch, current, details)+pullStatusText(d, result, t.Repo), "\n"))
		}
		runsRepo, runsSource, discovered := t.Repo, t.RepoSource, false
		if runs && runsRepo == "" {
			if found, err := a.resolveTarget(cmd.Context(), true, false); err == nil && found.Host == t.Host {
				runsRepo, runsSource, discovered = found.Repo, found.RepoSource, true
			}
		}
		if !runs {
			runsRepo = ""
		}
		result, err := service.Summary(cmd.Context(), limit, runsRepo, discovered)
		if err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, result)
		}
		text := fmt.Sprintf("%s on %s\n\n", terminal.Emphasis(terminal.SafeLine(result.User), d.Color), terminal.SafeLine(result.Host)) +
			statusSection(d, "Assigned issues", result.AssignedIssues, t.Repo) +
			statusSection(d, "Assigned pull requests", result.PullRequests.Assigned, t.Repo) +
			statusSection(d, "Review requests", result.PullRequests.ReviewRequested, t.Repo) +
			statusSection(d, "Mentions", result.Mentions, t.Repo)
		if result.RunsIncluded {
			heading := "Failing runs in " + result.RunsRepository
			if runsSource != "" && t.Repo == "" {
				heading += " (from " + strings.Replace(runsSource, "remote ", "git remote ", 1) + ")"
			}
			text += section(d, heading, result.FailingRuns, func(run services.Run) []terminal.Cell {
				return []terminal.Cell{{Text: strconv.FormatInt(run.ID, 10), Link: run.URL}, terminal.StatusIcon(run.Status, run.Conclusion), {Text: run.Title}, {Text: run.Conclusion}, {Text: run.URL}}
			})
		}
		return a.writeHuman(cmd, strings.TrimSuffix(text, "\n"))
	}}
	positiveIntVar(command, &limit, "limit", 30, 100, "Maximum results in each section (1..100)")
	if !pullsOnly {
		command.Flags().BoolVar(&runs, "runs", true, "Include the failing Actions runs of the selected or discovered repository")
	}
	if pullsOnly {
		return jsonOutput(command, jsonFields[services.PullStatus]())
	}
	return jsonOutput(command, jsonFields[services.AccountStatus]())
}

// currentPullStatus reads the pull request of the checked-out branch with its checks and reviews.
// Outside a checkout, or with --repo, it reports none; branch names the checked-out branch. A
// failed lookup is reported as a warning, since the rest of the status still helps.
func (a *application) currentPullStatus(cmd *cobra.Command) (*services.Pull, pullDetails, string) {
	var details pullDetails
	if a.options.repo != "" {
		return nil, details, ""
	}
	branch, err := a.currentBranch(cmd.Context())
	if err != nil {
		return nil, details, ""
	}
	t, n, err := a.pullSelectorAny(cmd, nil)
	if err != nil {
		if !errors.Is(err, errNoBranchPull) && clierr.ExitCode(err) != clierr.Usage {
			a.warn(cmd, "Could not look up the pull request of %s: %v", branch, err)
		}
		return nil, details, branch
	}
	service := services.Pulls{Client: t.client, Repository: t.Repo}
	pull, err := service.View(cmd.Context(), n)
	if err != nil {
		a.warn(cmd, "Could not read pull request #%d: %v", n, err)
		return nil, details, branch
	}
	if details.reviews, err = (services.Reviews{Client: t.client, Repository: t.Repo}).All(cmd.Context(), n); err == nil {
		pull = service.WithReviews(cmd.Context(), pull, details.reviews)
	}
	if pull.HeadSHA != "" {
		details.checks, _ = service.CommitChecks(cmd.Context(), pull.HeadSHA)
	}
	return &pull, details, branch
}

// currentBranchSection shows the pull request of the checked-out branch and how it stands:
// "[feat/one] - Checks: 2 pending · Review: Approved".
func currentBranchSection(d terminal.Display, branch string, pull *services.Pull, details pullDetails) string {
	if branch == "" {
		return ""
	}
	heading := terminal.Emphasis("Current branch", d.Color) + "\n"
	if pull == nil {
		return heading + "  " + terminal.Colorize("muted", "There is no pull request associated with ["+terminal.SafeLine(branch)+"]", d.Color) + "\n\n"
	}
	state := pullState(*pull)
	// An AGit pull request has no head branch; it was pushed from the checked-out one.
	head := cmp.Or(pull.Head, branch)
	row := terminal.Table([]terminal.Column{{Prefix: "#"}, {Flex: true}, {}}, [][]terminal.Cell{{
		{Text: strconv.FormatInt(pull.Number, 10), Link: pull.URL, Color: stateColors[state]}, {Text: pull.Title}, {Text: "[" + head + "]", Color: "cyan"},
	}}, indented(d))
	facts := []string{}
	if checks := checkSummary(details.checks); checks != "" {
		facts = append(facts, "Checks: "+checks)
	}
	decision := map[string]string{"approved": "Approved", "changes_requested": "Changes requested", "review_required": "Review required"}[pull.ReviewDecision]
	if decision != "" {
		facts = append(facts, "Review: "+decision)
	}
	if state != "open" {
		facts = append(facts, titleCase(state))
	}
	text := heading + indent(row)
	if len(facts) > 0 {
		text += "    - " + strings.Join(facts, " · ") + "\n"
	}
	return text + "\n"
}

func pullStatusText(d terminal.Display, result services.PullStatus, repo string) string {
	return statusSection(d, "Created by you", result.Authored, repo) +
		statusSection(d, "Assigned to you", result.Assigned, repo) +
		statusSection(d, "Requesting a code review from you", result.ReviewRequested, repo)
}

// statusSection lists issues or pull requests under a heading, as #N within repo and OWNER/REPO#N
// elsewhere.
func statusSection(d terminal.Display, heading string, items []services.SearchItem, repo string) string {
	return section(d, heading, items, func(item services.SearchItem) []terminal.Cell {
		ref := fmt.Sprintf("%s#%d", item.Repository, item.Number)
		if repo != "" && strings.EqualFold(item.Repository, repo) {
			ref = fmt.Sprintf("#%d", item.Number)
		}
		return []terminal.Cell{{Text: ref, Link: item.URL}, {Text: item.Title}, {Text: item.URL}}
	})
}

// section renders a bold heading over an indented table without header, or "Nothing here". The
// last cell of a row is a URL that only piped output keeps.
func section[T any](d terminal.Display, heading string, items []T, row func(T) []terminal.Cell) string {
	text := terminal.Emphasis(heading, d.Color) + "\n"
	if len(items) == 0 {
		return text + "  " + terminal.Colorize("muted", "Nothing here", d.Color) + "\n\n"
	}
	rows := make([][]terminal.Cell, len(items))
	columns := []terminal.Column{{}}
	for i, item := range items {
		rows[i] = row(item)
	}
	for range len(rows[0]) - 2 {
		columns = append(columns, terminal.Column{Flex: true})
	}
	columns = append(columns, terminal.Column{PipedOnly: true})
	return text + indent(terminal.Table(columns, rows, indented(d))) + "\n"
}

// indented narrows the display by the two-space indent of status sections.
func indented(d terminal.Display) terminal.Display {
	if d.Width > 2 {
		d.Width -= 2
	}
	return d
}
