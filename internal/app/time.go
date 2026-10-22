// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func trackedDuration(seconds int64) time.Duration { return time.Duration(seconds) * time.Second }

// parseSpent reads time spent in whole seconds, written like 1h25m or "1h 25m".
func parseSpent(raw string) (int64, error) {
	duration, err := time.ParseDuration(strings.Join(strings.Fields(raw), ""))
	switch {
	case err != nil:
		return 0, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid duration %q: use h, m and s units such as 1h25m", raw))
	case duration <= 0:
		return 0, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid duration %q: time spent must be positive, such as 1h25m", raw))
	case duration%time.Second != 0:
		return 0, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid duration %q: use whole seconds, such as 1h25m", raw))
	}
	return int64(duration / time.Second), nil
}

// spent formats tracked seconds for people: "1h 30m", "15m", "45s".
func spent(seconds int64) string {
	if seconds < 0 {
		return "-" + spent(-seconds)
	}
	d := trackedDuration(seconds)
	var parts []string
	for _, unit := range []struct {
		size   time.Duration
		suffix string
	}{{time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if n := d / unit.size; n > 0 && (unit.size != time.Second || seconds < 3600) {
			parts = append(parts, strconv.FormatInt(int64(n), 10)+unit.suffix)
		}
		d %= unit.size
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

func (a *application) timeCommand() *cobra.Command {
	group := &cobra.Command{Use: "time", Short: "Track time spent on issues and pull requests", Long: `Log the time spent on issues and pull requests, list it for a repository, an issue or
yourself across repositories, and delete it.`}
	group.AddCommand(a.timeListCommand(), a.timeCreateCommand(), a.timeDeleteCommand(), a.timeResetCommand())
	return jsonOutput(group, jsonFields[services.TrackedTime]())
}

func (a *application) timeListCommand() *cobra.Command {
	opts := services.TimeOptions{}
	var tracked timeRange
	var total bool
	var issue string
	cmd := &cobra.Command{Use: "list", Short: "List tracked time", Long: `List the time tracked in a repository, on one issue with --issue, or by yourself across
repositories with --mine. On a terminal, the total of the listed time follows the table.`, Example: `$ gt time list
$ gt time list --issue 12
$ gt time list --mine --since 168h`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if scopes := setFlags([]string{"--mine", "--issue", "--username"}, opts.Mine, issue != "", opts.Username != ""); len(scopes) > 1 {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with %s", scopes[0], scopes[1]))
		}
		if total && a.options.json != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--total is a human-output summary; use --jq 'map(.seconds) | add // 0' for JSON"))
		}
		var err error
		if opts.Since, opts.Before, err = tracked.parse(a.deps.Now()); err != nil {
			return err
		}
		var target target
		if issue != "" {
			target, opts.Issue, err = a.resolveRef(cmd, issue, issueRef)
		} else {
			target, err = a.resolve(cmd.Context(), !opts.Mine)
		}
		if errors.Is(err, errRepositoryRequired) {
			return clierr.Wrap(clierr.Usage, errors.New("repository required: use --repo owner/name, or --mine for your time across repositories"))
		}
		if err != nil {
			return err
		}
		opts.Username = target.expandMe(opts.Username)
		page, err := (services.Times{Client: target.client, Repository: target.Repo}).ListPage(cmd.Context(), opts)
		if err != nil {
			return err
		}
		where := "in " + target.Repo
		switch {
		case opts.Mine:
			where = "by " + target.login()
		case opts.Issue != 0:
			where = fmt.Sprintf("on %s#%d", target.Repo, opts.Issue)
		}
		spec := listSpec[services.TrackedTime]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Issue", Flex: true}, {Header: "User"}, {Header: "Spent"}, {Header: "When"}},
			Row:     trackedTimeRow(target.Repo, !a.display().TTY), Empty: "no time tracked " + where, Context: "time records " + where,
		}
		return a.writeTimes(cmd, page, opts.Page, spec, total)
	}}
	cmd.Flags().BoolVar(&opts.Mine, "mine", false, "List your time across repositories")
	cmd.Flags().StringVar(&issue, "issue", "", "List the time on one issue or pull request, by `number` or URL")
	cmd.Flags().StringVar(&opts.Username, "username", "", "List the time of one `user`; @me for yourself")
	pageFlags(cmd, &opts.Limit, &opts.Page)
	tracked.register(cmd, "since", "Only time tracked since a date, timestamp, or duration ago", "until", "Only time tracked before a date, timestamp, or duration ago")
	cmd.Flags().BoolVar(&total, "total", false, "Print the total of the listed time also when piped")
	_ = cmd.RegisterFlagCompletionFunc("issue", a.completeIssues("all"))
	_ = cmd.RegisterFlagCompletionFunc("username", a.completeUsers())
	return cmd
}

// writeTimes writes tracked time like writeTable, followed on a terminal, or with total, by the sum
// of the listed time.
func (a *application) writeTimes(cmd *cobra.Command, page services.Page[services.TrackedTime], number int, spec listSpec[services.TrackedTime], total bool) error {
	d := a.display()
	if a.options.json != "" || (len(page.Items) == 0 && (d.TTY || !total)) || (!d.TTY && !total) {
		return writeTable(a, cmd, page.Items, spec, servicePage(page, number))
	}
	var sum int64
	rows := make([][]terminal.Cell, len(page.Items))
	for i, item := range page.Items {
		sum += item.Seconds
		rows[i] = spec.Row(item)
	}
	text := terminal.Table(spec.Columns, rows, d) + "Total: " + spentFor(!d.TTY, sum) + "\n"
	if d.TTY {
		text = showing(len(page.Items), servicePage(page, number), spec.Context) + "\n\n" + text
	}
	if err := a.writeHuman(cmd, text); err != nil {
		return err
	}
	if d.TTY {
		return nil
	}
	// A piped total covers the listed page only.
	return a.morePagesHint(cmd, page.More, number)
}

// spentFor is spent on a terminal; when exact (piped output) it keeps every second as a Go
// duration such as 1h2m3s, which spent drops from durations of an hour or more.
func spentFor(exact bool, seconds int64) string {
	if exact {
		return trackedDuration(seconds).String()
	}
	return spent(seconds)
}

// trackedTimeRow shows a record in repo; records of other repositories name theirs.
func trackedTimeRow(repo string, exact bool) func(services.TrackedTime) []terminal.Cell {
	return func(item services.TrackedTime) []terminal.Cell {
		issue := "#" + strconv.FormatInt(item.IssueNumber, 10)
		if !strings.EqualFold(item.Repository, repo) {
			issue = item.Repository + issue
		}
		return []terminal.Cell{
			{Text: strconv.FormatInt(item.ID, 10)},
			{Text: strings.TrimSpace(issue + " " + item.IssueTitle)},
			{Text: item.User},
			{Text: spentFor(exact, item.Seconds)},
			{Time: terminal.ParseTime(item.CreatedAt)},
		}
	}
}

func (a *application) timeCreateCommand() *cobra.Command {
	return &cobra.Command{Use: "create {<number> | <url>} <duration>", Aliases: []string{"add", "log"}, Short: "Log time spent on an issue", Long: `Log time spent on an issue or pull request. The duration uses h, m and s units, such as
1h25m or "1h 25m".`, Example: `$ gt time create 12 1h25m
$ gt time add 12 "1h 30m"
$ gt time log https://gitea.com/OWNER/REPO/issues/12 45m`, Annotations: map[string]string{"gt.arguments": issueArguments}, Args: argsExact(2), ValidArgsFunction: firstArg(a.completeIssues("open")), RunE: func(cmd *cobra.Command, args []string) error {
		seconds, err := parseSpent(args[1])
		if err != nil {
			return err
		}
		target, n, err := a.resolveRef(cmd, args[0], issueRef)
		if err != nil {
			return err
		}
		item, err := (services.Times{Client: target.client, Repository: target.Repo}).Create(cmd.Context(), n, seconds)
		if err != nil {
			return err
		}
		a.done(cmd, "Logged %s on %s", spent(item.Seconds), strings.TrimSuffix(refLabel(target.Repo, n, item.IssueTitle), " ()"))
		return a.jsonResult(cmd, item)
	}}
}

func (a *application) timeDeleteCommand() *cobra.Command {
	return &cobra.Command{Use: "delete {<number> | <url>} <time-id>", Short: "Delete a time record", Example: `# Find the record with gt time list --issue 12
$ gt time delete 12 8`, Annotations: map[string]string{"gt.text": "time delete does not return a resource", "gt.confirm": "true", "gt.arguments": issueArguments}, Args: argsExact(2), ValidArgsFunction: firstArg(a.completeIssues("all")), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[1])
		if err != nil {
			return err
		}
		target, n, err := a.resolveRef(cmd, args[0], issueRef)
		if err != nil {
			return err
		}
		service := services.Times{Client: target.client, Repository: target.Repo}
		record, err := service.Find(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		subject := fmt.Sprintf("%s logged by %s on %s#%d", spent(record.Seconds), record.User, target.Repo, n)
		return a.deleteConfirmed(cmd, subject, func() error { return service.Delete(cmd.Context(), n, id) })
	}}
}

func (a *application) timeResetCommand() *cobra.Command {
	return &cobra.Command{Use: "reset {<number> | <url>}", Short: "Delete your time logged on an issue", Long: "Delete the time you logged on an issue or pull request. Gitea keeps the time other users logged.", Example: "$ gt time reset 12 --yes", Annotations: map[string]string{"gt.text": "time reset does not return a resource", "gt.confirm": "true", "gt.arguments": issueArguments}, Args: argsExact(1), ValidArgsFunction: firstArg(a.completeIssues("all")), RunE: func(cmd *cobra.Command, args []string) error {
		target, n, err := a.resolveRef(cmd, args[0], issueRef)
		if err != nil {
			return err
		}
		subject := fmt.Sprintf("your time logged on %s#%d", target.Repo, n)
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + "?"}); err != nil {
			return err
		}
		reset, err := (services.Times{Client: target.client, Repository: target.Repo}).Reset(cmd.Context(), n)
		if err != nil {
			return err
		}
		if !reset {
			a.warn(cmd, "You have no time logged on %s#%d", target.Repo, n)
			return nil
		}
		a.done(cmd, "Deleted %s", subject)
		return nil
	}}
}
