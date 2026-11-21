// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var templateItems = []map[string]any{
	{"number": 123456789, "title": "Fix login", "author": "alice", "labels": []string{"bug", "ui"}, "updated_at": "2026-10-11T17:00:00Z", "url": "https://x/1", "assets": []map[string]any{{"name": "a.txt"}, {"name": "b.txt"}}},
	{"number": 7, "title": "A much longer title than the others", "author": "bob", "labels": []string{}, "updated_at": "2026-10-01T20:00:00Z", "url": "https://x/2", "assets": []map[string]any{}},
}

func render(t *testing.T, o JSONOutput, value any) (string, error) {
	t.Helper()
	var out bytes.Buffer
	o.Now = func() time.Time { return time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) }
	err := o.Write(&out, value)
	return out.String(), err
}

func TestTemplateRendersTheSelectedJSON(t *testing.T) {
	out, err := render(t, JSONOutput{Template: `{{range .}}#{{.number}} {{.title}} ({{join "," .labels}}){{"\n"}}{{end}}`}, templateItems)
	require.NoError(t, err)
	require.Equal(t, "#123456789 Fix login (bug,ui)\n#7 A much longer title than the others ()\n", out, "an ID prints in full and nothing is added to the template")

	out, err = render(t, JSONOutput{Fields: "title", Allowed: []string{"title", "number"}, Template: `{{range .}}{{.title}}|{{.number}}{{"\n"}}{{end}}`}, templateItems)
	require.NoError(t, err)
	require.Equal(t, "Fix login|<no value>\nA much longer title than the others|<no value>\n", out, "only the selected fields are there")

	out, err = render(t, JSONOutput{Template: `{{.title}} by {{.author}}`}, templateItems[0])
	require.NoError(t, err)
	require.Equal(t, "Fix login by alice", out, "an object, not a list")
}

func TestTemplateFunctions(t *testing.T) {
	for _, tc := range []struct {
		name, template, want string
		output               JSONOutput
	}{
		{"pluck and join", `{{join ", " (pluck "name" (index . 0).assets)}}`, "a.txt, b.txt", JSONOutput{}},
		{"timeago", `{{timeago (index . 0).updated_at}}`, "about 3 hours ago", JSONOutput{}},
		{"timefmt", `{{timefmt "2006-01-02" (index . 0).updated_at}}`, "2026-10-11", JSONOutput{}},
		{"truncate", `{{truncate 8 (index . 1).title}}`, "A much …", JSONOutput{}},
		{"color", `{{color "red+b" "x"}}`, "\x1b[1;31mx\x1b[0m", JSONOutput{}},
		{"bright", `{{color "green+h" "x"}}`, "\x1b[92mx\x1b[0m", JSONOutput{}},
		{"autocolor off", `{{autocolor "red" "x"}}`, "x", JSONOutput{}},
		{"autocolor on", `{{autocolor "red" "x"}}`, "\x1b[31mx\x1b[0m", JSONOutput{Color: true, Pretty: true}},
		{"hyperlink off", `{{hyperlink "https://x/1" "one"}}`, "one", JSONOutput{}},
		{"hyperlink on", `{{hyperlink "https://x/1" "one"}}`, "\x1b]8;;https://x/1\x1b\\one\x1b]8;;\x1b\\", JSONOutput{Links: true}},
		{"hyperlink refuses other schemes", `{{hyperlink "javascript:x" "one"}}`, "one", JSONOutput{Links: true}},
		{"strings", `{{contains "ix" (index . 0).title}} {{hasPrefix "Fix" (index . 0).title}} {{hasSuffix "x" (index . 1).title}} {{regexMatch "^F.x" (index . 0).title}}`, "true true false true", JSONOutput{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.output.Template = tc.template
			out, err := render(t, tc.output, templateItems)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}

	t.Run("a table", func(t *testing.T) {
		out, err := render(t, JSONOutput{Template: `{{range .}}{{tablerow (printf "#%v" .number) .title .author}}{{end}}{{tablerender}}`}, templateItems)
		require.NoError(t, err)
		require.Equal(t, "#123456789  Fix login                            alice\n#7          A much longer title than the others  bob\n", out)
	})
	t.Run("a table with colored cells keeps its columns", func(t *testing.T) {
		out, err := render(t, JSONOutput{Template: `{{range .}}{{tablerow (color "red" .author) .title}}{{end}}{{tablerender}}`}, templateItems)
		require.NoError(t, err)
		require.Equal(t, "\x1b[31malice\x1b[0m  Fix login\n\x1b[31mbob\x1b[0m    A much longer title than the others\n", out)
	})
	t.Run("a table is rendered without tablerender too", func(t *testing.T) {
		out, err := render(t, JSONOutput{Template: `{{tablerow "a" "b"}}{{tablerow "cc" "d"}}`}, templateItems)
		require.NoError(t, err)
		require.Equal(t, "a   b\ncc  d\n", out)
	})
	t.Run("a cell is one line", func(t *testing.T) {
		out, err := render(t, JSONOutput{Template: `{{tablerow "a\r\nb" "x"}}{{tablerow "y" "z"}}`}, templateItems)
		require.NoError(t, err)
		require.Equal(t, "a  x\ny  z\n", out)
	})
}

func TestTemplateNumbersAreNumbers(t *testing.T) {
	for template, want := range map[string]string{
		`{{.n}}`:                              "123456789",
		`{{if .zero}}some{{else}}none{{end}}`: "none",
		`{{if gt .n 100}}big{{end}}`:          "big",
		`{{printf "%d" .n}}`:                  "123456789",
		`{{.f}}`:                              "1.5",
	} {
		var out strings.Builder
		require.NoError(t, JSONOutput{Template: template}.Write(&out, map[string]any{"n": 123456789, "zero": 0, "f": 1.5}), template)
		require.Equal(t, want, out.String(), template)
	}
}

func TestTemplateCleansMapKeysAndKeepsColorWhenTruncating(t *testing.T) {
	var out strings.Builder
	require.NoError(t, JSONOutput{Template: `{{range $k, $v := .}}{{$k}}={{$v}}{{end}}`, Safe: true}.Write(&out, map[string]any{"repo\x1b]0;owned\a": "read"}))
	require.NotContains(t, out.String(), "\x1b")
	require.Contains(t, out.String(), "repo")

	out.Reset()
	require.NoError(t, JSONOutput{Template: `{{truncate 4 (color "red" "hello")}}`}.Write(&out, map[string]any{}))
	require.Equal(t, "\x1b[31mhel…\x1b[0m", out.String())
}

func TestTemplateErrors(t *testing.T) {
	require.NoError(t, ValidateTemplate(`{{range .}}{{.title}}{{end}}`))
	for _, bad := range []string{`{{.title`, `{{nosuchfunc .}}`, `{{range .}}`} {
		require.ErrorContains(t, ValidateTemplate(bad), "invalid --template", bad)
	}
	for template, want := range map[string]string{
		`{{timeago "yesterday"}}`: `timeago: "yesterday" is not an RFC 3339 timestamp`,
		`{{timeago 5}}`:           "timeago: a timestamp is a string",
		`{{color "pink" "x"}}`:    `color: unknown color "pink"`,
		`{{color "red+z" "x"}}`:   `unknown modifier`,
		`{{join "," 5}}`:          "join: want a list",
		`{{regexMatch "(" "x"}}`:  "regexMatch:",
		`{{index . 5}}`:           "evaluate --template",
	} {
		_, err := render(t, JSONOutput{Template: template}, templateItems)
		require.ErrorContains(t, err, want, template)
	}
}

func TestTemplateKeepsServerTextOffTheTerminal(t *testing.T) {
	items := []map[string]any{{"title": "evil\x1b]0;owned\x07 title\x1b[31m", "labels": []string{"a\x1b[2Jb"}}}
	out, err := render(t, JSONOutput{Pretty: true, Template: `{{range .}}{{.title}}{{join "," .labels}}{{end}}`}, items)
	require.NoError(t, err)
	require.Equal(t, "evil]0;owned title[31ma[2Jb", out, "the escape and control characters go, the text stays")

	out, err = render(t, JSONOutput{Template: `{{range .}}{{.title}}{{end}}`}, items)
	require.NoError(t, err)
	require.Contains(t, out, "\x1b", "a script that pipes the output gets the text as it is")
}
