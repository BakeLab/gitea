// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// notificationScope is the notifications a command acts on: the account's inbox, or one repository's.
type notificationScope struct {
	services.Notifications
	// where names the scope in messages: "in your inbox" or "in alice/project".
	where string
	// host and user are the instance and account of the inbox.
	host, user string
}

// accountNotification is a listed notification with the account it belongs to.
type accountNotification struct {
	Host string `json:"host"`
	User string `json:"user"`
	services.Notification
}

func (a *application) notificationCommand() *cobra.Command {
	group := &cobra.Command{Use: "notification", Short: "Read and triage your notifications", Long: `Read and triage the notifications of your account, across your whole inbox unless -R or
--this-repo selects a repository. IDs identify notification threads, not issue numbers. The
type of a notification is issue, pull, commit or repository, as --type selects them.`}
	var thisRepo bool
	group.PersistentFlags().BoolVar(&thisRepo, "this-repo", false, "Only notifications of the repository in the current directory")
	scope := func(cmd *cobra.Command) (notificationScope, error) {
		t, err := a.resolve(cmd.Context(), thisRepo)
		if err != nil {
			return notificationScope{}, err
		}
		where := "in your inbox"
		if t.Repo != "" {
			where = "in " + t.Repo
		}
		return notificationScope{services.Notifications{Client: t.client, Repository: t.Repo}, where, t.Host, t.User}, nil
	}
	group.AddCommand(a.notificationListCommand(scope), a.notificationWatchCommand(scope), a.notificationViewCommand(scope))
	for _, mark := range []struct {
		verb, status, filter, short, ask, done, example string
	}{
		{"mark-read", "read", "unread", "Mark notifications as read", "Mark all %s as read?", "Marked %s as read", "$ gt notification mark-read 28\n# Everything in one repository\n$ gt notification mark-read --all -R alice/project"},
		{"mark-unread", "unread", "read", "Mark notifications as unread", "Mark all %s as unread?", "Marked %s as unread", "$ gt notification mark-unread 28 26"},
		{"pin", "pinned", "unread", "Pin notifications", "Pin all %s?", "Pinned %s", "$ gt notification pin 28"},
		{"unpin", "read", "pinned", "Unpin notifications", "Unpin all %s?", "Unpinned %s", "$ gt notification unpin 28\n# Unpin everything\n$ gt notification unpin --all"},
	} {
		var all bool
		var filters []string
		var until string
		command := &cobra.Command{
			Use: mark.verb + " [<id>...]", Short: mark.short, Example: mark.example, ValidArgsFunction: a.completeNotifications(),
			Annotations: map[string]string{"gt.confirm": "true"},
			Args: func(_ *cobra.Command, args []string) error {
				if len(args) == 0 && !all {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("notification %s requires notification IDs or --all", mark.verb))
				}
				if len(args) > 0 && all {
					return clierr.Wrap(clierr.Usage, errors.New("--all cannot be combined with notification IDs"))
				}
				return nil
			},
			RunE: func(cmd *cobra.Command, args []string) error {
				ids, err := resourceNumbers("resource", args)
				if err != nil {
					return err
				}
				if err := a.renamedFlag(cmd, "before", "until"); err != nil {
					return err
				}
				if !all && (cmd.Flags().Changed("status") || cmd.Flags().Changed("until")) {
					return clierr.Wrap(clierr.Usage, errors.New("--status and --until require --all"))
				}
				var cutoff time.Time
				if cmd.Flags().Changed("until") {
					parsed, err := parseRunTime(until, a.deps.Now())
					if err != nil {
						return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --until: %w", err))
					}
					cutoff = *parsed
				}
				s, err := scope(cmd)
				if err != nil {
					return err
				}
				if !all {
					return batchDone(a, cmd, ids, func(id int64) string { return fmt.Sprintf("notification %d", id) }, func(id int64) (services.Notification, error) {
						item, err := markNotification(cmd.Context(), s, id, mark.verb, mark.status)
						// The batch names the notification already.
						if services.IsNotFound(err) {
							return item, explain(err, "not found %s", s.where)
						}
						if elsewhere, ok := errors.AsType[*services.ElsewhereError](err); ok {
							return item, explain(err, "belongs to %s, not selected repository %s", elsewhere.Repository, elsewhere.Selected)
						}
						return item, err
					}, func(item services.Notification) string { return fmt.Sprintf(mark.done, notificationLabel(item)) })
				}
				what := notificationsWith(filters)
				if !cutoff.IsZero() {
					what += " updated before " + cutoff.Format("2006-01-02 15:04")
				}
				what += " " + s.where
				if a.canPrompt() {
					if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf(mark.ask, what), DefaultYes: true}); err != nil {
						return err
					}
				}
				results, err := s.MarkAll(cmd.Context(), mark.status, filters, cutoff)
				if err != nil {
					return err
				}
				if a.options.json != "" {
					return a.writeJSON(cmd, results)
				}
				if len(results) == 0 {
					a.warn(cmd, "No %s", what)
					return nil
				}
				a.done(cmd, mark.done, terminal.Plural(len(results), "notification"))
				return nil
			},
		}
		command.Flags().BoolVar(&all, "all", false, "Apply to every notification that matches --status in the scope")
		command.Flags().StringSliceVar(&filters, "status", []string{mark.filter}, "With --all, the statuses to change: unread, read, pinned")
		enumValues(command, "status", "unread", "read", "pinned")
		command.Flags().StringVar(&until, "until", "", "With --all, only notifications updated before a date, timestamp, or duration such as 1h")
		renameFlag(command, "before", "until")
		group.AddCommand(command)
	}
	return jsonOutput[services.Notification](group)
}

// notificationFilters registers the list filters of list and watch.
func notificationFilters(cmd *cobra.Command, opts *services.NotificationOptions, updated *timeRange) {
	pageFlags(cmd, &opts.Limit, &opts.Page)
	cmd.Flags().StringSliceVar(&opts.Statuses, "status", []string{"unread", "pinned"}, "Only notifications with these statuses: unread, read, pinned")
	enumValues(cmd, "status", "unread", "read", "pinned")
	cmd.Flags().StringSliceVar(&opts.Types, "type", nil, "Only notifications about these subjects: issue, pull, commit, repository")
	enumValues(cmd, "type", "issue", "pull", "commit", "repository")
	updated.register(cmd, "since", "Only notifications updated since a date, timestamp, or duration such as 24h", "until", "Only notifications updated before a date, timestamp, or duration such as 1h")
	renameFlag(cmd, "before", "until")
}

// notificationOptions completes the list filters for the API.
func (a *application) notificationOptions(cmd *cobra.Command, opts services.NotificationOptions, updated *timeRange) (services.NotificationOptions, error) {
	if err := a.renamedFlag(cmd, "before", "until"); err != nil {
		return opts, err
	}
	types := make([]string, len(opts.Types))
	for i, kind := range opts.Types {
		types[i] = titleCase(kind)
	}
	opts.Types = types
	var err error
	opts.Since, opts.Before, err = updated.parse(a.deps.Now())
	return opts, err
}

func (a *application) notificationListCommand(scope func(*cobra.Command) (notificationScope, error)) *cobra.Command {
	opts := services.NotificationOptions{}
	var updated timeRange
	var allAccounts bool
	command := &cobra.Command{
		Use: "list", Short: "List notifications", Args: argsExact(0),
		Long: `List the notifications of your account, or with --all-accounts those of every account you
are logged in to, most recently updated first, each with its host and user. --host and --user
narrow --all-accounts to the accounts they name, and --limit applies to the merged list. An account
that fails does not hide the others: they are listed, and gt exits with the failure.`,
		Example: `$ gt notification list
# Including the ones you read, of one repository
$ gt notification list --status unread,read,pinned -R alice/project
# Pull requests updated today
$ gt notification list --type pull --since 24h
# Every account, or every account on one instance
$ gt notification list --all-accounts
$ gt notification list --all-accounts --host gitea.com`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if allAccounts {
				if conflict := setFlags([]string{"--repo", "--this-repo", "--page", "GITEA_TOKEN"}, a.options.repo != "", cmd.Flags().Changed("this-repo"), cmd.Flags().Changed("page"), a.deps.Getenv("GITEA_TOKEN") != ""); len(conflict) > 0 {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("--all-accounts cannot be combined with %s", conflict[0]))
				}
			}
			listed, err := a.notificationOptions(cmd, opts, &updated)
			if err != nil {
				return err
			}
			if allAccounts {
				return a.listAccountNotifications(cmd, listed, opts.Statuses)
			}
			s, err := scope(cmd)
			if err != nil {
				return err
			}
			page, err := s.List(cmd.Context(), listed)
			if err != nil {
				return err
			}
			items := make([]accountNotification, len(page.Items))
			for i, item := range page.Items {
				items[i] = accountNotification{s.host, s.user, item}
			}
			what := notificationsWith(opts.Statuses) + " " + s.where
			empty := "no " + what
			if cmd.Flags().Changed("type") || cmd.Flags().Changed("since") || cmd.Flags().Changed("until") {
				empty = "no " + notificationsWith(opts.Statuses) + " match your filters " + s.where
			}
			return writeTable(a, cmd, items, listSpec[accountNotification]{
				Columns: notificationColumns, Row: func(item accountNotification) []terminal.Cell { return notificationRow(item.Notification) }, Empty: empty, Context: what,
			}, listPage{Number: opts.Page, More: page.More, Total: page.Total, Counted: page.Counted})
		},
	}
	notificationFilters(command, &opts, &updated)
	command.Flags().BoolVar(&allAccounts, "all-accounts", false, "List the notifications of every account you are logged in to")
	return jsonOutput[accountNotification](command)
}

// listAccountNotifications lists the inboxes of the accounts --host and --user select, merged with
// the most recently updated first. Each inbox is asked for --limit items, so the merged list is complete.
func (a *application) listAccountNotifications(cmd *cobra.Command, opts services.NotificationOptions, statuses []string) error {
	s, err := a.settings()
	if err != nil {
		return err
	}
	accounts, err := selectedAccounts(a, s)
	if err != nil {
		return err
	}
	items := []accountNotification{}
	var failures []error
	more := false
	for _, account := range accounts {
		t, err := a.accountTarget(cmd.Context(), account.host, account.user)
		var page services.Page[services.Notification]
		if err == nil {
			page, err = services.Notifications{Client: t.client}.List(cmd.Context(), opts)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s on %s: %w", account.user, account.host, err))
			continue
		}
		more = more || page.More
		for _, item := range page.Items {
			items = append(items, accountNotification{account.host, account.user, item})
		}
	}
	slices.SortStableFunc(items, func(x, y accountNotification) int {
		return terminal.ParseTime(y.UpdatedAt).Compare(terminal.ParseTime(x.UpdatedAt))
	})
	if len(failures) == len(accounts) {
		return errors.Join(failures...)
	}
	if len(items) > opts.Limit {
		items, more = items[:opts.Limit], true
	}
	what := notificationsWith(statuses) + " across " + terminal.Plural(len(accounts), "account")
	heading := what
	if more { // The total is unknown, so the table cannot say "Showing 30 of 30".
		heading = ""
	}
	err = writeTable(a, cmd, items, listSpec[accountNotification]{
		Columns: slices.Concat([]terminal.Column{{Header: "Host"}, {Header: "User"}}, notificationColumns),
		Row: func(item accountNotification) []terminal.Cell {
			return slices.Concat([]terminal.Cell{{Text: item.Host}, {Text: item.User}}, notificationRow(item.Notification))
		},
		Empty: "no " + what, Context: heading,
	}, listPage{Number: 1})
	if more {
		a.warn(cmd, "More notifications exist; raise --limit to list them")
	}
	return errors.Join(append(failures, err)...)
}

// notificationAccount is one stored account.
type notificationAccount struct{ host, user string }

// selectedAccounts returns the stored accounts that --host and --user select, sorted.
func selectedAccounts(a *application, s settings.Snapshot) ([]notificationAccount, error) {
	selectedHost := a.options.host
	if selectedHost == "" {
		selectedHost, _ = a.envHost()
	}
	if selectedHost != "" {
		var err error
		if selectedHost, err = settings.ResolveHost(selectedHost, s.Hosts.Hosts); err != nil {
			return nil, clierr.Wrap(clierr.Usage, err)
		}
	}
	selectedUser, _ := a.selectedUser()
	var accounts []notificationAccount
	for _, instance := range slices.Sorted(maps.Keys(s.Hosts.Hosts)) {
		if selectedHost != "" && instance != selectedHost {
			continue
		}
		users := s.Hosts.Hosts[instance].Users
		for _, user := range slices.Sorted(maps.Keys(users)) {
			if selectedUser == "" || strings.EqualFold(user, selectedUser) {
				accounts = append(accounts, notificationAccount{instance, user})
			}
		}
	}
	switch {
	case len(accounts) > 0:
		return accounts, nil
	case selectedUser != "":
		if err := unknownAccountError(s.Hosts.Hosts, selectedHost, selectedUser); err != nil {
			return nil, clierr.Wrap(clierr.Authentication, err)
		}
	case selectedHost != "":
		return nil, clierr.Wrap(clierr.Authentication, fmt.Errorf("not logged in to %s; run gt auth login --host %s", selectedHost, selectedHost))
	}
	return nil, clierr.Wrap(clierr.Authentication, errors.New("not logged in to any Gitea instance; run gt auth login"))
}

// accountTarget connects to the stored account user of instance, whichever account --host, --user
// or the environment select.
func (a *application) accountTarget(ctx context.Context, instance, user string) (target, error) {
	host, selected := a.options.host, a.options.user
	defer func() { a.options.host, a.options.user = host, selected }()
	a.options.host, a.options.user = instance, user
	return a.resolveRequest(ctx, targetRequest{authenticate: true, noRepo: true})
}

func (a *application) notificationWatchCommand(scope func(*cobra.Command) (notificationScope, error)) *cobra.Command {
	opts := services.NotificationOptions{}
	var updated timeRange
	var interval, timeout time.Duration
	command := &cobra.Command{
		Use: "watch", Short: "Print notifications as they arrive", Args: argsExact(0),
		Long: `Print notifications as they arrive or change, until Ctrl-C or --timeout. On a terminal
it runs until stopped; otherwise it stops after a minute unless --timeout says otherwise.
With --json it prints one JSON object per line. It polls on through an outage of the instance
of up to 5 minutes.`,
		Example: "$ gt notification watch\n# Stream pull request notifications to a script, starting with the current ones\n$ gt notification watch --type pull --json id,repository,title --timeout 0 | ./notify.sh",
		RunE: func(cmd *cobra.Command, _ []string) error {
			listed, err := a.notificationOptions(cmd, opts, &updated)
			if err != nil {
				return err
			}
			if interval <= 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--interval must be positive"))
			}
			if timeout < 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--timeout cannot be negative"))
			}
			if !cmd.Flags().Changed("timeout") && !a.deps.Streams.OutTTY {
				timeout = time.Minute
			}
			s, err := scope(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			d := a.display()
			if d.TTY && a.options.json == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "Watching %s %s; press Ctrl-C to stop\n", notificationsWith(opts.Statuses), s.where)
			}
			out := terminal.SafeWriter(cmd.OutOrStdout(), d.TTY, d.Color)
			headed := false
			err = s.Watch(ctx, listed, interval, func(items []services.Notification) error {
				if a.options.json != "" {
					for _, item := range items {
						csv := a.options.csv != ""
						if err := (terminal.JSONOutput{Fields: a.options.json, Allowed: commandOutputFields(cmd), Query: a.options.jq, Safe: d.TTY, CSV: csv, NoHeader: headed, Template: a.options.template, Now: a.deps.Now}).Write(cmd.OutOrStdout(), item); err != nil {
							return err
						}
						headed = csv
					}
					return nil
				}
				if !d.TTY {
					rows := make([][]terminal.Cell, len(items))
					for i, item := range items {
						rows[i] = notificationRow(item)
					}
					_, err := fmt.Fprint(out, terminal.Table(notificationColumns, rows, d))
					return err
				}
				for _, item := range items {
					if _, err := fmt.Fprintln(out, watchLine(item, d)); err != nil {
						return err
					}
				}
				return nil
			})
			if expired(err) && cmd.Context().Err() == nil {
				return nil
			}
			return err
		},
	}
	notificationFilters(command, &opts, &updated)
	command.Flags().DurationVar(&interval, "interval", 5*time.Second, "Time between checks")
	command.Flags().DurationVar(&timeout, "timeout", 0, "Stop after this long (default: never on a terminal, 1m otherwise)")
	return command
}

func (a *application) notificationViewCommand(scope func(*cobra.Command) (notificationScope, error)) *cobra.Command {
	return &cobra.Command{
		Use: "view <id>", Short: "View a notification and mark it as read", Args: argsExact(1), ValidArgsFunction: a.completeNotifications(),
		Long: `View a notification: its subject, the subject's state and the latest comment. Like
opening it on the web, viewing marks an unread notification as read; pinned ones stay
pinned.`,
		Example: "$ gt notification view 28\n$ gt notification view 28 --json title,url",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			s, err := scope(cmd)
			if err != nil {
				return err
			}
			item, err := s.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			var comment *services.Comment
			if a.options.json == "" {
				if comment, err = s.LatestComment(cmd.Context(), item); err != nil {
					return err
				}
			}
			// Marking is a side effect of viewing: a token without write:notification, or an
			// administrator reading another user's thread, still gets the view.
			if item.Unread && !item.Pinned {
				if _, err := s.Mark(cmd.Context(), id, "read"); err != nil {
					a.warn(cmd, "Could not mark notification %d as read: %v", id, err)
				} else {
					item.Unread = false
				}
			}
			return writeView(a, cmd, item, func(item services.Notification) terminal.View { return notificationView(item, comment, a.display()) })
		},
	}
}

// notificationColumns head notification tables; piped, they add the status and the URL.
var notificationColumns = []terminal.Column{
	{Header: "ID"},
	{Header: "Type"},
	{Header: "Repository"},
	{Header: "Subject", Flex: true},
	{Header: "State"},
	{Header: "Status", PipedOnly: true},
	{Header: "Updated"},
	{Header: "URL", PipedOnly: true},
}

// notificationRow shows a notification under notificationColumns; unread subjects are bold.
func notificationRow(item services.Notification) []terminal.Cell {
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.ID, 10), Link: item.URL},
		{Text: item.Type},
		{Text: item.Repository},
		{Text: notificationSubject(item), Bold: item.Unread, Muted: !item.Unread && !item.Pinned},
		{Text: item.State, Color: stateColors[item.State]},
		{Text: notificationStatus(item)},
		{Time: terminal.ParseTime(item.UpdatedAt)},
		{Text: item.URL},
	}
}

// notificationSubject reads "#17 Improve greeting" for issues and pull requests.
func notificationSubject(item services.Notification) string {
	if item.Number == 0 {
		return item.Title
	}
	return fmt.Sprintf("#%d %s", item.Number, item.Title)
}

// markNotification changes the status of notification id as verb does.
func markNotification(ctx context.Context, s notificationScope, id int64, verb, status string) (services.Notification, error) {
	// Gitea unpins by marking read, which would also mark a thread read that was not pinned.
	if verb == "unpin" {
		item, err := s.View(ctx, id)
		if err != nil {
			return item, err
		}
		if !item.Pinned {
			return item, errors.New("not pinned")
		}
	}
	return s.Mark(ctx, id, status)
}

// notificationsWith names the notifications with one of statuses: "unread or pinned notifications",
// or "notifications" for every status.
func notificationsWith(statuses []string) string {
	var distinct []string
	for _, status := range statuses {
		if !slices.Contains(distinct, status) {
			distinct = append(distinct, status)
		}
	}
	switch len(distinct) {
	case 0, 3:
		return "notifications"
	case 1:
		return distinct[0] + " notifications"
	}
	return distinct[0] + " or " + distinct[1] + " notifications"
}

func notificationStatus(item services.Notification) string {
	switch {
	case item.Pinned:
		return "pinned"
	case item.Unread:
		return "unread"
	}
	return "read"
}

// notificationLabel names a notification in feedback: "notification 28 (alice/project#17)".
func notificationLabel(item services.Notification) string {
	return fmt.Sprintf("notification %d (%s)", item.ID, subjectRef(item))
}

// subjectRef names the subject of a notification by its repository and number: "alice/project#17".
func subjectRef(item services.Notification) string {
	if item.Number == 0 {
		return item.Repository
	}
	return item.Repository + "#" + strconv.FormatInt(item.Number, 10)
}

// notificationKinds names the subject types in a view footer.
var notificationKinds = map[string]string{"issue": "issue", "pull": "pull request", "commit": "commit", "repository": "repository"}

// notificationView shows a notification with the latest comment on its subject as the body.
func notificationView(item services.Notification, comment *services.Comment, d terminal.Display) terminal.View {
	meta := item.Repository
	if updated := terminal.ParseTime(item.UpdatedAt); !updated.IsZero() {
		meta += " · updated " + terminal.RelativeTime(d.Now, updated)
	}
	view := terminal.View{
		Title: item.Title, Meta: meta, Kind: notificationKinds[item.Type], URL: item.URL,
		Badges: []terminal.Cell{{Text: titleCase(item.State), Color: stateColors[item.State]}, {Text: notificationStatus(item), Muted: true}},
		Fields: []terminal.Field{{Label: "Notification", Value: strconv.FormatInt(item.ID, 10)}, {Label: "Type", Value: notificationKinds[item.Type]}, {Label: "Repository", Value: item.Repository}},
	}
	if d.TTY {
		view.Body = "_No comments yet._"
	}
	view.Fields = append(view.Fields,
		terminal.Field{Label: "State", Value: item.State, PipedOnly: true},
		terminal.Field{Label: "Status", Value: notificationStatus(item), PipedOnly: true})
	if item.Number != 0 {
		view.ID = "#" + strconv.FormatInt(item.Number, 10)
	}
	if comment != nil {
		when := ""
		if created := terminal.ParseTime(comment.CreatedAt); d.TTY && !created.IsZero() {
			when = ", " + terminal.RelativeTime(d.Now, created)
		}
		commented := timeField("Commented", comment.CreatedAt, d)
		commented.PipedOnly = true
		view.Fields = append(view.Fields, terminal.Field{Label: "Latest comment", Value: comment.Author + when}, commented)
		view.Body = comment.Body
	}
	return view
}

// watchLine shows a notification that gt notification watch reports on a terminal:
// "15:04  alice/project#17  Improve greeting  pull · open".
func watchLine(item services.Notification, d terminal.Display) string {
	when := terminal.ParseTime(item.UpdatedAt)
	if when.IsZero() {
		when = d.Now
	}
	subject := subjectRef(item)
	details := terminal.SafeLine(item.Type)
	if item.State != "" {
		details += " · " + terminal.Colorize(stateColors[item.State], terminal.SafeLine(item.State), d.Color)
	}
	return terminal.Colorize("muted", when.Local().Format("15:04"), d.Color) + "  " + terminal.Emphasis(terminal.SafeLine(subject), d.Color) + "  " + terminal.SafeLine(item.Title) + "  " + details
}

// completeNotifications completes the IDs of the notifications in your inbox with their subjects.
func (a *application) completeNotifications() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		page, err := services.Notifications{Client: t.client, Repository: t.Repo}.List(ctx, services.NotificationOptions{Limit: completionLimit, Page: 1, Statuses: []string{"unread", "pinned", "read"}})
		return describe(page.Items, func(item services.Notification) (string, string) {
			return strconv.FormatInt(item.ID, 10), subjectRef(item) + " " + item.Title
		}), err
	})
}
