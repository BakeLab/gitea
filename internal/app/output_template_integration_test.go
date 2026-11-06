// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGiteaOutputTemplate(t *testing.T) {
	repo := newIntegrationRepo(t, "gt-template")
	first, second := repo.issue(t, "First <issue>"), repo.issue(t, "Second issue")
	repo.execute("label", "create", "bug", "--color", "ff0000", "-R", repo.FullName)
	repo.execute("issue", "edit", first, "--add-label", "bug", "-R", repo.FullName)

	out := string(repo.execute("issue", "list", "-R", repo.FullName, "--state", "all", "--template", `{{range .}}#{{.number}} {{.title}}{{"\n"}}{{end}}`))
	require.Equal(t, "#"+second+" Second issue\n#"+first+" First <issue>\n", out, "--template alone selects every field, and the template decides the layout")

	out = string(repo.execute("issue", "list", "-R", repo.FullName, "--json", "number,title,labels", "--template",
		`{{range .}}{{.number}}:{{join "," (pluck "name" .labels)}}{{"\n"}}{{end}}`))
	require.Equal(t, second+":\n"+first+":bug\n", out)

	out = string(repo.execute("issue", "list", "-R", repo.FullName, "--json", "number,title", "--template",
		`{{range .}}{{tablerow (printf "#%v" .number) .title}}{{end}}{{tablerender}}`))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2)
	require.Equal(t, strings.Index(lines[0], "Second"), strings.Index(lines[1], "First"), "the table keeps its columns aligned")

	out = string(repo.execute("issue", "view", first, "-R", repo.FullName, "--json", "number,created_at", "--template", `{{.number}} {{timefmt "2006" .created_at}} {{timeago .created_at}}`))
	require.Regexp(t, `^`+first+` 20\d\d (less than a minute ago|about \d+ \w+ ago|\d+ \w+ ago)$`, out)

	require.ErrorContains(t, repo.fails(t, "issue", "list", "--template", `{{.number`), "invalid --template")
	require.ErrorContains(t, repo.fails(t, "issue", "list", "--template", "x", "--jq", "."), "--template cannot be combined with --jq")
	require.ErrorContains(t, repo.fails(t, "issue", "delete", first, "--template", "x"), "--template is not supported")
	require.ErrorContains(t, repo.fails(t, "issue", "list", "--template", `{{timeago "yesterday"}}`), "not an RFC 3339 timestamp")

	// issue create keeps its own --template, which names an issue template to start from.
	require.ErrorContains(t, repo.fails(t, "issue", "create", "--template", "no-such-template", "--title", "x"), "template")
}
