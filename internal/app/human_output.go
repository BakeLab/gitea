// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
)

// preferences applies environment overrides to the stored preferences, matching config get --show-source.
func (a *application) preferences() (settings.Preferences, error) {
	snapshot, err := a.deps.Settings.Load()
	if err != nil {
		return settings.Preferences{}, err
	}
	preferences, _, err := a.effectivePreferences(snapshot)
	return preferences, err
}

func (a *application) colorEnabled() bool {
	preferences, _ := a.preferences()
	return terminal.ColorEnabled(preferences.Color, a.deps.Streams.OutTTY, a.deps.Getenv)
}

// display describes stdout for tables and views. The hyperlinks (auto, always, never) and markdown
// (auto, raw) preferences are read from GITEA_CLI_HYPERLINKS and GITEA_CLI_MARKDOWN until config.yml stores them.
func (a *application) display() terminal.Display {
	preferences, _ := a.preferences()
	streams := a.deps.Streams
	return terminal.Display{
		TTY:       streams.OutTTY,
		Color:     terminal.ColorEnabled(preferences.Color, streams.OutTTY, a.deps.Getenv),
		TrueColor: terminal.TrueColor(a.deps.Getenv),
		Links:     terminal.HyperlinksEnabled(a.deps.Getenv("GITEA_CLI_HYPERLINKS"), streams.OutTTY, a.deps.Getenv),
		Markdown:  terminal.MarkdownEnabled(a.deps.Getenv("GITEA_CLI_MARKDOWN"), streams.OutTTY),
		Width:     streams.Width,
		Now:       a.deps.Now(),
	}
}

// writeHuman pages multi-line human list/view text. JSON callers must not use it.
// --no-input does not disable paging.
func (a *application) writeHuman(cmd *cobra.Command, text string) error {
	preferences, _ := a.preferences()
	return terminal.WriteHuman(cmd.Context(), a.deps.Streams, preferences.Pager, a.deps.Pager, text)
}

// writeJSON writes v as JSON, filtered by --json fields and --jq.
func (a *application) writeJSON(cmd *cobra.Command, v any) error {
	pretty := a.deps.Streams.OutTTY
	return terminal.JSONOutput{Fields: a.options.json, Query: a.options.jq, Pretty: pretty, Color: pretty && a.colorEnabled()}.Write(cmd.OutOrStdout(), v)
}

// listSpec describes how a list command shows its items to people.
type listSpec[T any] struct {
	Columns []terminal.Column
	Row     func(T) []terminal.Cell
	// Empty, such as "no open issues in alice/x", goes to stderr on a TTY when nothing is listed.
	Empty string
	// Context names the listed items, such as "open issues in alice/x", for the line that heads the
	// table on a TTY: "Showing 30 of 112 open issues in alice/x".
	Context string
}

// listPage places a page of items in the whole listing.
type listPage struct {
	// Number is the 1-based --page value.
	Number int
	// More reports later pages. Total is the size of the whole listing when Counted.
	More    bool
	Total   int
	Counted bool
}

// writeTable writes items as a JSON array, or as a table through the pager. On a TTY the table has
// a header row and, with a spec Context, a context line that also points at the next page; an empty
// list prints the spec's Empty message to stderr instead. Piped tables are tab-separated, and the
// next-page hint goes to stderr.
func writeTable[T any](a *application, cmd *cobra.Command, items []T, spec listSpec[T], page listPage) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, items)
	}
	d := a.display()
	if len(items) == 0 {
		if !d.TTY || spec.Empty == "" {
			return nil
		}
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), spec.Empty)
		return err
	}
	rows := make([][]terminal.Cell, len(items))
	for i, item := range items {
		rows[i] = spec.Row(item)
	}
	text := terminal.Table(spec.Columns, rows, d)
	if d.TTY && spec.Context != "" {
		return a.writeHuman(cmd, showing(len(items), page, spec.Context)+"\n\n"+text)
	}
	if err := a.writeHuman(cmd, text); err != nil {
		return err
	}
	return a.morePagesHint(cmd, page.More, page.Number)
}

// showing is the context line above a table: "Showing 30 of 112 open issues in alice/x · next: --page 2".
func showing(shown int, page listPage, what string) string {
	line := fmt.Sprintf("Showing %d", shown)
	switch {
	case page.Counted:
		line += fmt.Sprintf(" of %d", page.Total)
	case !page.More && page.Number <= 1:
		line += fmt.Sprintf(" of %d", shown)
	}
	line += " " + what
	if page.More {
		line += fmt.Sprintf(" · next: --page %d", page.Number+1)
	}
	return line
}

// writeView writes item as a JSON object, or as a view through the pager.
func writeView[T any](a *application, cmd *cobra.Command, item T, view func(T) terminal.View) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, item)
	}
	return a.writeHuman(cmd, view(item).Render(a.display()))
}

// done reports a completed mutation on stderr, such as "✓ Closed issue alice/x#5 (Fix login)",
// when people read stdout: on a TTY and without --json.
func (a *application) done(cmd *cobra.Command, format string, args ...any) {
	a.feedback(cmd, "✓", "green", format, args...)
}

// warn reports a mutation that had nothing to do, such as "! Issue alice/x#5 (Fix login) is already
// closed", under the same conditions as done.
func (a *application) warn(cmd *cobra.Command, format string, args ...any) {
	a.feedback(cmd, "!", "yellow", format, args...)
}

func (a *application) feedback(cmd *cobra.Command, icon, color, format string, args ...any) {
	if !a.deps.Streams.OutTTY || a.options.json != "" {
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(), terminal.Colorize(color, icon, a.colorEnabled())+" "+terminal.SafeLine(fmt.Sprintf(format, args...)))
}

// printURL writes the URL of a created resource to stdout unless --json is set, so scripts can
// capture it: url=$(gt issue create …).
func (a *application) printURL(cmd *cobra.Command, url string) error {
	if a.options.json != "" {
		return nil
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), terminal.SafeLine(url))
	return err
}

// refLabel names an issue or pull request in feedback: "alice/x#5 (Fix login)".
func refLabel(repo string, number int64, title string) string {
	return fmt.Sprintf("%s#%d (%s)", repo, number, title)
}

// abortBatch stops a batch instead of recording an item failure.
type abortBatch struct{ error }

func (e abortBatch) Unwrap() error { return e.error }

// applyBatch applies run to each selection and continues past individual failures, which it
// returns prefixed with the selection's label. succeeded sees each result as it arrives.
func applyBatch[K, T any](cmd *cobra.Command, keys []K, label func(K) string, run func(K) (T, error), succeeded func(T)) ([]T, []error) {
	results := []T{}
	var failures []error
	for _, key := range keys {
		if err := cmd.Context().Err(); err != nil {
			failures = append(failures, err)
			break
		}
		item, err := run(key)
		if errors.As(err, new(abortBatch)) {
			failures = append(failures, err)
			break
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", label(key), err))
			continue
		}
		results = append(results, item)
		succeeded(item)
	}
	return results, failures
}

// writeBatchJSON writes an object for one selection (none if it failed), otherwise an array.
func writeBatchJSON[T any](a *application, cmd *cobra.Command, selections int, results []T) error {
	switch {
	case selections != 1:
		return a.writeJSON(cmd, results)
	case len(results) == 1:
		return a.writeJSON(cmd, results[0])
	}
	return nil
}

// batch applies run to each selection like batchDone, for commands that predate it: its human
// output is one table of the results.
func batch[K, T any](a *application, cmd *cobra.Command, keys []K, label func(K) string, run func(K) (T, error), row func(T) []terminal.Cell) error {
	results, failures := applyBatch(cmd, keys, label, run, func(T) {})
	var err error
	if a.options.json == "" {
		err = a.writeHuman(cmd, legacyTable(a, cmd, results, row))
	} else {
		err = writeBatchJSON(a, cmd, len(keys), results)
	}
	return errors.Join(append(failures, err)...)
}

// batchDone applies a mutation to each selection like batch, reporting each success with a done
// line from report, such as "Closed issue alice/x#5 (Fix login)", and the results as JSON.
func batchDone[K, T any](a *application, cmd *cobra.Command, keys []K, label func(K) string, run func(K) (T, error), report func(T) string) error {
	results, failures := applyBatch(cmd, keys, label, run, func(item T) { a.done(cmd, "%s", report(item)) })
	var err error
	if a.options.json != "" {
		err = writeBatchJSON(a, cmd, len(keys), results)
	}
	return errors.Join(append(failures, err)...)
}

// writeList writes items as a JSON array, or as one table through the pager. List commands move
// to writeTable.
func writeList[T any](a *application, cmd *cobra.Command, items []T, row func(T) []terminal.Cell) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, items)
	}
	return a.writeHuman(cmd, legacyTable(a, cmd, items, row))
}

// writeItem writes one item as a JSON object, or as its list row. Views move to writeView, and
// mutations to done and printURL.
func writeItem[T any](a *application, cmd *cobra.Command, item T, row func(T) []terminal.Cell) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, item)
	}
	return a.writeHuman(cmd, legacyTable(a, cmd, []T{item}, row))
}

// legacyColumns gives lists that still render through writeList a header row and a flexible
// column, keyed by command path without the root. Their owners replace an entry with a listSpec.
var legacyColumns = map[string][]terminal.Column{
	"issue list": numberedColumns,
	"pr list":    numberedColumns,
}

// numberedColumns head the rows of issueRow and pullRow.
var numberedColumns = []terminal.Column{{Header: "ID", Prefix: "#"}, {Header: "Title", Flex: true}, {Header: "State"}, {Header: "Author"}}

// legacyTable renders rows of commands that predate listSpec: tab-separated when piped, and on a
// TTY fit to the terminal width by truncating their widest column, unless legacyColumns names
// their columns.
func legacyTable[T any](a *application, cmd *cobra.Command, items []T, row func(T) []terminal.Cell) string {
	rows := make([][]terminal.Cell, len(items))
	for i, item := range items {
		rows[i] = row(item)
	}
	columns, ok := legacyColumns[strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")]
	if !ok {
		columns = widestFlex(rows)
	}
	return terminal.Table(columns, rows, a.display())
}

// widestFlex makes the column with the widest cell flexible.
func widestFlex(rows [][]terminal.Cell) []terminal.Column {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], runewidth.StringWidth(terminal.SafeLine(cell.Text)))
		}
	}
	columns := make([]terminal.Column, len(widths))
	if len(widths) > 0 {
		columns[slices.Index(widths, slices.Max(widths))].Flex = true
	}
	return columns
}

// writeRecord writes one item as a JSON object, or as a human-readable record.
func writeRecord[T any](a *application, cmd *cobra.Command, item T, record func(T) terminal.Record) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, item)
	}
	return a.writeHuman(cmd, record(item).Render(a.colorEnabled()))
}

// writeRecords writes items as a JSON array, or as consecutive human-readable records.
func writeRecords[T any](a *application, cmd *cobra.Command, items []T, record func(T) terminal.Record) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, items)
	}
	color := a.colorEnabled()
	var text strings.Builder
	for _, item := range items {
		text.WriteString(record(item).Render(color))
	}
	return a.writeHuman(cmd, text.String())
}

// numberedRecord heads an issue or pull request with its emphasized number and title.
func numberedRecord(number int64, title, url string) terminal.Record {
	return terminal.Record{Heading: []terminal.Cell{{Text: fmt.Sprintf("#%d", number), Bold: true}, {Text: title}}, Lines: []string{url}}
}

// stateColors colors issue, pull request and milestone states with the terminal's own palette.
var stateColors = map[string]string{"open": "green", "closed": "red", "merged": "magenta", "draft": "muted"}

// table renders rows aligned like a TTY table without header or truncation, for commands that
// predate listSpec and compose their own output.
func table[T any](items []T, row func(T) []terminal.Cell, color bool) string {
	rows := make([][]terminal.Cell, len(items))
	for i, item := range items {
		rows[i] = row(item)
	}
	return terminal.Table(nil, rows, terminal.Display{TTY: true, Color: color, TrueColor: true, Links: color})
}

// morePagesHint tells human readers on stderr that the listing continues past this page.
func (a *application) morePagesHint(cmd *cobra.Command, more bool, page int) error {
	if !more || a.options.json != "" {
		return nil
	}
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), "more results available; use --page %d\n", page+1)
	return err
}
