// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/tea/internal/settings"
	"gitea.dev/tea/internal/terminal"
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

// writeHuman pages multi-line human list/view text. JSON callers must not use it.
// --no-input does not disable paging.
func (a *application) writeHuman(cmd *cobra.Command, text string) error {
	preferences, _ := a.preferences()
	return terminal.WriteHuman(cmd.Context(), a.deps.Streams, preferences.Pager, a.deps.Pager, text)
}

// writeJSON writes v as JSON, filtered by --json fields and --jq.
func (a *application) writeJSON(cmd *cobra.Command, v any) error {
	return terminal.WriteJSON(cmd.OutOrStdout(), v, a.options.json, a.options.jq)
}

// abortBatch stops a batch instead of recording an item failure.
type abortBatch struct{ error }

func (e abortBatch) Unwrap() error { return e.error }

// batch applies run to each selection and continues past individual failures, which
// it reports prefixed with the selection's label. Human output is one table of the
// results; JSON is an object for one selection (none if it failed), otherwise an array.
func batch[K, T any](a *application, cmd *cobra.Command, keys []K, label func(K) string, run func(K) (T, error), row func(T) []terminal.Cell) error {
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
	}
	var err error
	switch {
	case a.options.json == "":
		err = a.writeHuman(cmd, table(results, row, a.colorEnabled()))
	case len(keys) != 1:
		err = a.writeJSON(cmd, results)
	case len(results) == 1:
		err = a.writeJSON(cmd, results[0])
	}
	return errors.Join(append(failures, err)...)
}

// writeList writes items as a JSON array, or as one aligned table through the pager.
func writeList[T any](a *application, cmd *cobra.Command, items []T, row func(T) []terminal.Cell) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, items)
	}
	return a.writeHuman(cmd, table(items, row, a.colorEnabled()))
}

// writeItem writes one item as a JSON object, or as its list row.
func writeItem[T any](a *application, cmd *cobra.Command, item T, row func(T) []terminal.Cell) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, item)
	}
	return a.writeHuman(cmd, table([]T{item}, row, a.colorEnabled()))
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

// stateColors colors issue, pull request and milestone states on color terminals.
var stateColors = map[string]string{"open": "2da44e", "closed": "cf222e", "merged": "8250df"}

func table[T any](items []T, row func(T) []terminal.Cell, color bool) string {
	rows := make([][]terminal.Cell, len(items))
	for i, item := range items {
		rows[i] = row(item)
	}
	return terminal.Table(rows, color)
}

// morePagesHint tells human readers on stderr that the listing continues past this page.
func (a *application) morePagesHint(cmd *cobra.Command, more bool, page int) error {
	if !more || a.options.json != "" {
		return nil
	}
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), "more results available; use --page %d\n", page+1)
	return err
}
