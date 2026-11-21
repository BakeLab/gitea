// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTable(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	tty := Display{TTY: true, Now: now}
	issues := []Column{{Header: "ID", Prefix: "#"}, {Header: "Title", Flex: true}, {Header: "Labels", Flex: true}, {Header: "Updated"}}
	issueRows := [][]Cell{
		{{Text: "12", Link: "https://example.org/12"}, {Text: "Emoji in title 🚀 works?"}, {}, {Time: now.Add(-2 * time.Hour)}},
		{{Text: "1", Link: "https://example.org/1"}, {Text: "Login fails with special characters in password"}, {Text: "bug, priority/high"}, {Time: now.Add(-90 * time.Minute)}},
	}
	for _, tc := range []struct {
		name    string
		columns []Column
		rows    [][]Cell
		d       Display
		want    string
	}{
		{"empty", issues, nil, tty, ""},
		{"aligns legacy rows without header", nil, [][]Cell{row("7", "Fix login", "open"), row("12", "Crash", "closed")}, tty, "7   Fix login  open\n12  Crash      closed\n"},
		{"aligns by display width", nil, [][]Cell{row("1", "äöü", "x"), row("2", "修复", "y"), row("3", "abcd", "z")}, tty, "1  äöü   x\n2  修复  y\n3  abcd  z\n"},
		{"trims empty trailing cells", nil, [][]Cell{row("1", "bug", ""), row("22", "feature", "new work")}, tty, "1   bug\n22  feature  new work\n"},
		{"pads ragged rows", nil, [][]Cell{row("1"), row("2", "two")}, tty, "1\n2  two\n"},
		{
			"heads columns and formats cells for people",
			issues, issueRows, tty,
			"ID   TITLE                                            LABELS              UPDATED\n" +
				"#12  Emoji in title 🚀 works?                                             about 2 hours ago\n" +
				"#1   Login fails with special characters in password  bug, priority/high  about 1 hour ago\n",
		},
		{
			"shrinks the widest flexible column first",
			issues, issueRows,
			Display{TTY: true, Now: now, Width: 72},
			"ID   TITLE                         LABELS              UPDATED\n" +
				"#12  Emoji in title 🚀 works?                          about 2 hours ago\n" +
				"#1   Login fails with special ch…  bug, priority/high  about 1 hour ago\n",
		},
		{
			"keeps flexible columns at their floor",
			issues, issueRows,
			Display{TTY: true, Now: now, Width: 40},
			"ID   TITLE     LABELS    UPDATED\n" +
				"#12  Emoji i…            about 2 hours ago\n" +
				"#1   Login f…  bug, pr…  about 1 hour ago\n",
		},
		{
			"cuts wide runes by display width",
			[]Column{{Header: "Title", Flex: true}, {Header: "By"}},
			[][]Cell{row("支持中文路径 (support CJK paths)", "alice")},
			Display{TTY: true, Width: 20},
			"TITLE          BY\n支持中文路径…  alice\n",
		},
		{
			"writes tab-separated values when piped",
			issues, issueRows,
			Display{Width: 40, Color: true, Links: true},
			"12\tEmoji in title 🚀 works?\t\t2026-10-11T18:00:00Z\n" +
				"1\tLogin fails with special characters in password\tbug, priority/high\t2026-10-11T18:30:00Z\n",
		},
		{
			"keeps columns to their output",
			[]Column{{Header: "ID"}, {Header: "State", PipedOnly: true}, {Header: "Link", TTYOnly: true}},
			[][]Cell{row("1", "open", "→")},
			tty,
			"ID  LINK\n1   →\n",
		},
		{
			"keeps piped-only columns when piped",
			[]Column{{Header: "ID"}, {Header: "State", PipedOnly: true}, {Header: "Link", TTYOnly: true}},
			[][]Cell{row("1", "open", "→")},
			Display{},
			"1\topen\n",
		},
		{
			"strips hostile text in both outputs",
			nil,
			[][]Cell{row("1", "a\x1b]52;c;cHduZWQ=\x07b\x1b[2J\u009b31m\u202etxt.exe\r\ntail\tend", "open")},
			Display{},
			"1\ta]52;c;cHduZWQ=b[2J31mtxt.exe tail end\topen\n",
		},
		{"ignores styles without color", nil, [][]Cell{{{Text: "7", Link: "https://example.org/7"}, {Text: "bug", Color: "ff0000"}, {Text: "open", Bold: true}}}, tty, "7  bug  open\n"},
		{
			"styles after alignment and links only the first column",
			[]Column{{Header: "ID"}, {Header: "Name"}, {Header: "State"}},
			[][]Cell{{{Text: "7", Link: "https://example.org/7"}, {Text: "bug", Color: "ff0000", Link: "https://example.org/bug"}, {Text: "open", Color: "green", Bold: true}}, row("12", "feature", "closed")},
			Display{TTY: true, Color: true, Links: true},
			"\x1b[90mID\x1b[0m  \x1b[90mNAME\x1b[0m     \x1b[90mSTATE\x1b[0m\n" +
				"\x1b]8;;https://example.org/7\x1b\\7\x1b]8;;\x1b\\   bug      \x1b[1m\x1b[32mopen\x1b[0m\x1b[0m\n" +
				"12  feature  closed\n",
		},
		{
			"links without color",
			nil,
			[][]Cell{{{Text: "7", Link: "https://example.org/7"}, {Text: "bug", Muted: true}}},
			Display{TTY: true, Links: true},
			"\x1b]8;;https://example.org/7\x1b\\7\x1b]8;;\x1b\\  bug\n",
		},
		{
			"leaves hex colors plain",
			nil,
			[][]Cell{{{Text: "bug", Color: "#ff0000"}, {Text: "old", Muted: true}}},
			Display{TTY: true, Color: true},
			"bug  \x1b[90mold\x1b[0m\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Table(tc.columns, tc.rows, tc.d))
		})
	}
}

func TestSafeText(t *testing.T) {
	for _, tc := range []struct{ name, in, text, line string }{
		{"plain", "fix login", "fix login", "fix login"},
		{"keeps tabs and line breaks", "a\tb\r\nc\nd", "a\tb\nc\nd", "a b c d"},
		{"drops lone carriage returns", "safe\rFAKE", "safeFAKE", "safeFAKE"},
		{
			"strips hostile body",
			"Steps:\r\n\x1b]52;c;cHduZWQ=\x07run\x1b[2J\u009b31m\u202e​it\x7f\n\x00done",
			"Steps:\n]52;c;cHduZWQ=run[2J31mit\ndone",
			"Steps: ]52;c;cHduZWQ=run[2J31mit done",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.text, SafeText(tc.in))
			require.Equal(t, tc.line, SafeLine(tc.in))
		})
	}
}

// row returns unstyled cells.
func row(texts ...string) []Cell {
	cells := make([]Cell, len(texts))
	for i, text := range texts {
		cells[i].Text = text
	}
	return cells
}
