// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputTemplate(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFirstPage(w, r, `[{"id":2,"title":"laptop","fingerprint":"SHA256:aaa","created_at":"2026-10-11T17:00:00Z"},{"id":3,"title":"work","fingerprint":"SHA256:bbb","created_at":"2026-10-01T17:00:00Z"}]`)
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append([]string{"ssh-key", "list"}, args...))
		return out.String(), err
	}

	out, err := run("--template", `{{range .}}{{.id}} {{.title}} {{.fingerprint}}{{"\n"}}{{end}}`)
	require.NoError(t, err)
	require.Equal(t, "2 laptop SHA256:aaa\n3 work SHA256:bbb\n", out, "--template alone selects every field")

	out, err = run("--json", "title", "--template", `{{range .}}[{{.title}}|{{.id}}]{{end}}`)
	require.NoError(t, err)
	require.Equal(t, "[laptop|<no value>][work|<no value>]", out, "--json still chooses the fields")

	out, err = run("--template", `{{range .}}{{tablerow .title .fingerprint}}{{end}}{{tablerender}}`)
	require.NoError(t, err)
	require.Equal(t, "laptop  SHA256:aaa\nwork    SHA256:bbb\n", out)
}

func TestOutputTemplateIsCheckedBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"ssh-key", "list", "--template", "{{.x", "--no-input"}, "invalid --template"},
		{[]string{"ssh-key", "list", "--template", "x", "--jq", "."}, "--template cannot be combined with --jq"},
		{[]string{"ssh-key", "list", "--template", "x", "--csv"}, "--template cannot be combined with --csv"},
		{[]string{"ssh-key", "list", "--template="}, "--template requires a template"},
		{[]string{"ssh-key", "delete", "3", "--template", "x"}, "SSH key deletion does not return a resource; --template is not supported"},
		{[]string{"api", "user", "--template", "x"}, "gt api prints the API's own JSON; --template is not supported, use --jq"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			requireUsageBeforeRequests(t, tc.want, tc.args...)
		})
	}
}

// TestOutputTemplateLeavesTheTemplatesOfCreateCommands covers the create commands whose
// own --template names a template to start from, not a format.
func TestOutputTemplateLeavesTheTemplatesOfCreateCommands(t *testing.T) {
	deps, out, _ := testDeps(t)
	for _, command := range []string{"issue", "repo", "project"} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{command, "create", "--help"}))
		help := out.String()
		require.Len(t, regexp.MustCompile(`(?m)^[ \t]+(-T, )?--template +\w+ {2,}`).FindAllString(help, -1), 1, command)
		require.NotContains(t, help, "Go template", command)
	}
}

func TestOutputTemplateReadsOnlyTheFieldsItNames(t *testing.T) {
	var requests int
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls") && r.URL.Query().Get("page") <= "1":
			_, _ = w.Write([]byte(`[{"number":7,"title":"t","state":"open","head":{"ref":"f"},"base":{"ref":"main"}}]`))
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			_, _ = w.Write([]byte(`{"number":7,"title":"t","state":"open","head":{"ref":"f"},"base":{"ref":"main"}}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	run := func(template string) int {
		requests = 0
		deps, _, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "--template", template, "--no-input"}))
		return requests
	}
	require.Equal(t, 1, run(`{{range .}}{{.number}}{{end}}`))
	require.Greater(t, run(`{{range .}}{{.review_decision}}{{end}}`), 1)
}

func TestOutputTemplateFormatsTheReportOfADryRun(t *testing.T) {
	server := dryRunServer(t)
	out, _, err := dryRun(t, server, "label", "delete", "bug", "--template", `{{range .requests}}{{.method}} {{.url}}{{"\n"}}{{end}}`)
	require.NoError(t, err)
	require.Equal(t, "DELETE "+server.URL+"/api/v1/repos/alice/project/labels/9\n", out)
}
