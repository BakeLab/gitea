// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"strings"
	"testing"
	"time"

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

func TestViewKeepsPlainBodiesVerbatim(t *testing.T) {
	view := View{Title: "main", Body: "fix <parser> __init__\n# not a heading\x1b[2J", Plain: true}
	require.Contains(t, view.Render(Display{TTY: true, Markdown: true}), "\nfix <parser> __init__\n# not a heading[2J\n")
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

func TestMarkdownDropsEscapesFromCharacterReferences(t *testing.T) {
	for _, in := range []string{"a &#27;]0;pwned&#7; b", "a &#x1b;[31m b", "a &amp;#27;]52;c;x&#7; b", "a &#155;31m b"} {
		out := Markdown(in, Display{Width: 80})
		for _, r := range out {
			if r == 0x1b || r == 0x07 || (r >= 0x80 && r < 0xa0) {
				t.Fatalf("Markdown(%q) = %q contains control %U", in, out, r)
			}
		}
	}
}

func TestMarkdownKeepsOrdinaryCharacterReferences(t *testing.T) {
	plain := Display{Width: 80}
	require.Contains(t, Markdown("5 &lt; 6 &amp; &#169; &#x263a; &copy;", plain), "5 < 6 & © ☺ ©")
	require.Contains(t, Markdown("a&#9;b", plain), "a\tb")
	require.Equal(t, "ab", strings.TrimSpace(Markdown("a&#27;&#x1B;&#7;&#155;&#8238;b", plain)))
	require.Equal(t, "ab", strings.TrimSpace(Markdown("a&rlm;&lrm;&zwj;&shy;b", plain)), "named entities of format characters")
}

func TestMarkdownRendersLongBodiesQuickly(t *testing.T) {
	body := strings.Repeat("Some **bold** text with a [link](https://example.org) and `code`.\n\n", 3000)
	require.Greater(t, len(body), 200_000)
	for _, d := range []Display{{Width: 80}, {Width: 80, Color: true, Links: true}} {
		renderers.Lock()
		renderer, err := markdownRenderer(d.Width, d.Color)
		require.NoError(t, err)
		start := time.Now()
		_, err = renderer.Render(body)
		glamour := time.Since(start)
		renderers.Unlock()
		require.NoError(t, err)

		start = time.Now()
		rendered := Markdown(body, d)
		// Measured against glamour on the same machine, so a slow or busy one does not fail the test:
		// what Markdown does around glamour, such as stripping controls, stays small next to it.
		require.Less(t, time.Since(start), 3*glamour+time.Second, "rendering %d bytes took too long; glamour took %v", len(body), glamour)
		require.Equal(t, 3000, strings.Count(rendered, "text with a"))
	}
}

func TestStripControlsIsLinear(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("\x1b[1mbold\x1b[m plain \u200ftext \x1b]8;id=1;https://example.org\x07link\x1b]8;;\x07\n", 4000),
		strings.Repeat("a long code block line without any color in \u200fit\n", 20000) + "\x1b[1mend\x1b[m",
	} {
		start := time.Now()
		_ = strings.Map(func(r rune) rune { return r }, text)
		linear := time.Since(start)
		start = time.Now()
		stripped := stripControls(text)
		// Measured against one pass over the text on the same machine, so a slow one does not fail the test.
		require.Less(t, time.Since(start), 50*linear+time.Second, "stripping %d bytes took too long; one pass took %v", len(text), linear)
		require.Equal(t, strings.ReplaceAll(text, "\u200f", ""), stripped)
	}
}
