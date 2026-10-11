// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  [][]Cell
		color bool
		want  string
	}{
		{"empty", nil, false, ""},
		{"aligns columns", [][]Cell{Row("7", "Fix login", "open"), Row("12", "Crash", "closed")}, false, "7   Fix login  open\n12  Crash      closed\n"},
		{"counts runes, not bytes", [][]Cell{Row("1", "äöü", "x"), Row("2", "abcd", "y")}, false, "1  äöü   x\n2  abcd  y\n"},
		{"counts wide runes twice", [][]Cell{Row("1", "修复", "x"), Row("2", "abcd", "y")}, false, "1  修复  x\n2  abcd  y\n"},
		{"trims empty trailing cells", [][]Cell{Row("1", "bug", ""), Row("22", "feature", "new work")}, false, "1   bug\n22  feature  new work\n"},
		{"pads ragged rows", [][]Cell{Row("1"), Row("2", "two")}, false, "1\n2  two\n"},
		{
			"strips hostile title",
			[][]Cell{Row("1", "a\x1b]52;c;cHduZWQ=\x07b\x1b[2J\u009b31m‮txt.exe\r\ntail\tend", "open")},
			false,
			"1  a]52;c;cHduZWQ=b[2J31mtxt.exe tail end  open\n",
		},
		{"ignores styles without color", [][]Cell{{{Text: "7", Link: "https://example.org/7"}, {Text: "bug", Color: "ff0000"}, {Text: "open", Bold: true}}}, false, "7  bug  open\n"},
		{
			"styles after alignment",
			[][]Cell{{{Text: "7", Link: "https://example.org/7"}, {Text: "bug", Color: "ff0000"}, {Text: "open", Bold: true}}, Row("12", "feature", "closed")},
			true,
			"\x1b]8;;https://example.org/7\x1b\\7\x1b]8;;\x1b\\   \x1b[38;2;255;0;0mbug\x1b[0m      \x1b[1mopen\x1b[0m\n12  feature  closed\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Table(tc.rows, tc.color))
		})
	}
}

func TestRecord(t *testing.T) {
	record := Record{
		Heading: []Cell{{Text: "#7", Bold: true}, {Text: "Crash\x1b[2J\nnow"}, {Text: "bob\u202e"}},
		Lines:   []string{"Steps:\r\n\x1b]52;c;aGk=\x07run\n", "https://example.org/7"},
	}
	require.Equal(t, "#7 Crash[2J now bob\nSteps:\n]52;c;aGk=run\n\nhttps://example.org/7\n", record.Render(false))
	require.Equal(t, "\x1b[1m#7\x1b[0m Crash[2J now bob\nSteps:\n]52;c;aGk=run\n\nhttps://example.org/7\n", record.Render(true))
}

func TestSafeText(t *testing.T) {
	for _, tc := range []struct{ name, in, text, line string }{
		{"plain", "fix login", "fix login", "fix login"},
		{"keeps tabs and line breaks", "a\tb\r\nc\nd", "a\tb\nc\nd", "a b c d"},
		{"drops lone carriage returns", "safe\rFAKE", "safeFAKE", "safeFAKE"},
		{
			"strips hostile body",
			"Steps:\r\n\x1b]52;c;cHduZWQ=\x07run\x1b[2J\u009b31m\u202e\u200bit\x7f\n\x00done",
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
