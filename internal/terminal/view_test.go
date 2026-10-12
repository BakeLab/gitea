// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func issueView() View {
	return View{
		Title: "Login fails\x1b[2J", ID: "#1",
		Badges: []Cell{{Text: "Open", Color: "green"}},
		Meta:   "alice opened about 1 hour ago • 2 comments",
		Fields: []Field{
			{Label: "State", Value: "open", PipedOnly: true},
			{Label: "Labels", Cells: []Cell{{Text: "bug", Color: "ee0701"}, {Text: "priority/high"}}},
			{Label: "Milestone", Value: "v1.0"},
			{Label: "Blocked by"},
			{Label: "Comments", Value: "2", TTYOnly: true},
		},
		Body: "Steps:\r\n1. Use `p@ss!`\x1b]52;c;aGk=\x07\n",
		Kind: "issue", URL: "http://localhost:3001/alice/project/issues/1",
	}
}

func TestViewRender(t *testing.T) {
	for _, tc := range []struct {
		name string
		view View
		d    Display
		want string
	}{
		{
			"key and value lines when piped",
			issueView(),
			Display{Color: true},
			"title:\tLogin fails[2J\nstate:\topen\nlabels:\tbug, priority/high\nmilestone:\tv1.0\nblocked by:\t\n" +
				"url:\thttp://localhost:3001/alice/project/issues/1\n--\nSteps:\n1. Use `p@ss!`]52;c;aGk=\n",
		},
		{
			"summary for people",
			issueView(),
			Display{TTY: true},
			"Login fails[2J #1\nOpen • alice opened about 1 hour ago • 2 comments\n" +
				"Labels:    bug, priority/high\nMilestone: v1.0\nComments:  2\n\n" +
				"Steps:\n1. Use `p@ss!`]52;c;aGk=\n\nView this issue on Gitea: http://localhost:3001/alice/project/issues/1\n",
		},
		{
			"styled for color terminals",
			View{Title: "v1.0", Badges: []Cell{{Text: "Latest", Color: "green"}}, Fields: []Field{{Label: "Labels", Cells: []Cell{{Text: "bug", Color: "ee0701"}}}}, URL: "https://example.org/r"},
			Display{TTY: true, Color: true},
			"\x1b[1mv1.0\x1b[0m\n\x1b[32mLatest\x1b[0m\n\x1b[1mLabels:\x1b[0m bug\n\n" +
				"\x1b[90mNo description provided\x1b[0m\n\n\x1b[90mView on Gitea: https://example.org/r\x1b[0m\n",
		},
		{
			"minimal",
			View{Title: "Home"},
			Display{TTY: true},
			"Home\n\nNo description provided\n",
		},
		{
			"bodyless for people",
			View{Title: "laptop", Fields: []Field{{Label: "Type", Value: "ssh-ed25519"}}, Bodyless: true, URL: "https://example.org/keys"},
			Display{TTY: true},
			"laptop\nType: ssh-ed25519\n\nView on Gitea: https://example.org/keys\n",
		},
		{
			"bodyless when piped",
			View{Title: "laptop", Fields: []Field{{Label: "Type", Value: "ssh-ed25519"}}, Bodyless: true, URL: "https://example.org/keys"},
			Display{},
			"title:\tlaptop\ntype:\tssh-ed25519\nurl:\thttps://example.org/keys\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.view.Render(tc.d))
		})
	}
}

func TestViewRendersMarkdownOnlyWhenEnabled(t *testing.T) {
	view := View{Title: "Docs", Body: "# Setup\n\nRun **gt** [now](https://example.org).\n\n- one\n- two"}
	require.Contains(t, view.Render(Display{TTY: true}), "\n# Setup\n\nRun **gt** [now](https://example.org).\n")
	require.Contains(t, view.Render(Display{Markdown: true}), "--\n# Setup\n\nRun **gt** [now](https://example.org).\n")

	plain := view.Render(Display{TTY: true, Markdown: true, Width: 40})
	require.Contains(t, plain, "\n  # Setup\n\n  Run **gt** now https://example.org.\n\n  • one\n  • two\n")
	require.NotContains(t, plain, "\x1b")

	colored := view.Render(Display{TTY: true, Markdown: true, Color: true})
	require.Contains(t, colored, "Run \x1b[1mgt\x1b[m")
	require.NotContains(t, colored, "\x1b]8;")
	require.Contains(t, view.Render(Display{TTY: true, Markdown: true, Color: true, Links: true}), "\x1b]8;id=")
}

func TestMarkdownSanitizesServerText(t *testing.T) {
	rendered := Markdown("Hello \x1b]0;PWNED\x07world\n\n`\x1b[2Jcode`", Display{Color: true, Links: true})
	require.NotContains(t, rendered, "\x1b]0;")
	require.NotContains(t, rendered, "\x1b[2J")
	require.Contains(t, rendered, "]0;PWNEDworld")
	for _, line := range strings.Split(Markdown(strings.Repeat("word ", 50), Display{Width: 40}), "\n") {
		require.LessOrEqual(t, len(line), 40, line)
		require.Equal(t, strings.TrimRight(line, " "), line)
	}
}

func TestMarkdownEnabled(t *testing.T) {
	require.True(t, MarkdownEnabled("auto", true))
	require.True(t, MarkdownEnabled("", true))
	require.False(t, MarkdownEnabled("raw", true))
	require.False(t, MarkdownEnabled("auto", false))
}
