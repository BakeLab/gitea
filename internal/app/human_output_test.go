// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/tea/internal/terminal"
	"github.com/stretchr/testify/require"
)

type recordingPager struct {
	command string
	input   string
	calls   int
}

func (p *recordingPager) Page(_ context.Context, command string, input io.Reader, streams terminal.Streams) error {
	p.calls++
	p.command = command
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	p.input = string(data)
	_, err = io.WriteString(streams.Out, "PAGED:"+string(data))
	return err
}

// pagedDeps returns serverDeps on a terminal stdout with a recording pager,
// extra environment variables and the given config settings.
func pagedDeps(t *testing.T, serverURL string, env map[string]string, config ...string) (Dependencies, *bytes.Buffer, *bytes.Buffer, *recordingPager) {
	t.Helper()
	deps, out, diagnostics := testDeps(t)
	pager := &recordingPager{}
	deps.Pager = pager
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(serverURL, env)
	for i := 0; i < len(config); i += 2 {
		require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", config[i], config[i+1]}))
	}
	return deps, out, diagnostics, pager
}

func TestLabelListAppliesColorAndPagesWithNoInput(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/labels", r.URL.Path)
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"ff0000","description":"Defect"},{"id":2,"name":"docs","color":"00ff00","description":"Docs"}]`))
	}))
	defer server.Close()
	deps, out, diagnostics, pager := pagedDeps(t, server.URL, map[string]string{"PAGER": "fake-pager"}, "color", "always")
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, 1, pager.calls)
	require.Equal(t, "fake-pager", pager.command)
	require.Contains(t, pager.input, "\x1b[38;2;255;0;0mbug\x1b[0m")
	require.Contains(t, pager.input, "\x1b[38;2;0;255;0mdocs\x1b[0m")
	require.Equal(t, "PAGED:"+pager.input, out.String())
	require.Empty(t, diagnostics.String())
}

func TestIssueListRespectsColorNeverAndStillPages(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":1,"title":"One","state":"open"},{"number":2,"title":"Two","state":"closed"}]`))
	}))
	defer server.Close()
	deps, out, _, pager := pagedDeps(t, server.URL, nil, "color", "never", "pager", "configured-pager")
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, 1, pager.calls)
	require.Equal(t, "configured-pager", pager.command)
	require.NotContains(t, pager.input, "\x1b[")
	require.Contains(t, pager.input, "open")
	require.Contains(t, pager.input, "closed")
	require.Equal(t, "PAGED:"+pager.input, out.String())
}

func TestIssueViewJSONDoesNotPage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"number":7,"title":"Title","html_url":"https://example.org/alice/project/issues/7","body":"line1\nline2\nline3"}`)
	}))
	defer server.Close()
	deps, out, _, pager := pagedDeps(t, server.URL, map[string]string{"PAGER": "fake-pager"})
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "view", "7", "-R", "alice/project", "--json=number,title", "--no-input"}))
	require.Zero(t, pager.calls)
	require.JSONEq(t, `{"number":7,"title":"Title"}`, out.String())
}

func TestConfiguredPagerOverridesPagerEnvironment(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":1,"title":"One"},{"number":2,"title":"Two"}]`))
	}))
	defer server.Close()
	deps, _, _, pager := pagedDeps(t, server.URL, map[string]string{"PAGER": "env-pager"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "env-pager", pager.command)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "pager", "configured-pager"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "configured-pager", pager.command)
}

func TestTeaPagerAndColorEnvironmentOverrides(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"ff0000","description":""},{"id":2,"name":"docs","color":"00ff00","description":""}]`))
	}))
	defer server.Close()
	env := map[string]string{"TEA_PAGER": "tea-pager", "PAGER": "generic-pager", "TEA_COLOR": "always"}
	deps, out, _, pager := pagedDeps(t, server.URL, env, "pager", "configured-pager", "color", "never")
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "tea-pager", pager.command)
	require.Contains(t, pager.input, "\x1b[38;2;255;0;0mbug\x1b[0m")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "pager", "--show-source", "--json"}))
	require.JSONEq(t, `{"pager":{"value":"tea-pager","source":"TEA_PAGER"}}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "color", "--show-source", "--json"}))
	require.JSONEq(t, `{"color":{"value":"always","source":"TEA_COLOR"}}`, out.String())
}

func TestColorAutoUsesStdoutTTY(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"ff0000","description":""}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Streams.OutTTY = false
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Equal(t, "1  bug  ff0000\n", out.String())
	require.NotContains(t, out.String(), "\x1b[")

	out.Reset()
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "-R", "alice/project", "--no-input"}))
	require.Contains(t, out.String(), "\x1b[38;2;255;0;0mbug\x1b[0m")
}

func TestMultiItemHumanOutputPagesOnce(t *testing.T) {
	issue := func(n int) string {
		return fmt.Sprintf(`{"number":%d,"title":"Issue %d","state":"closed","html_url":"https://example.org/i/%d"}`, n, n, n)
	}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/milestones/3":
			_, _ = w.Write([]byte(`{"id":3,"title":"v1"}`))
		case "/api/v1/repos/alice/project/issues":
			_, _ = w.Write([]byte("[" + issue(1) + "," + issue(2) + "]"))
		case "/api/v1/repos/alice/project/issues/1", "/api/v1/repos/alice/project/issues/2":
			_, _ = w.Write([]byte(issue(int(r.URL.Path[len(r.URL.Path)-1] - '0'))))
		case "/api/v1/repos/alice/project/branches":
			_, _ = w.Write([]byte(`[{"name":"main","commit":{"id":"abc"}},{"name":"dev","commit":{"id":"def"}}]`))
		case "/api/v1/repos/alice/project/issues/5/comments":
			_, _ = w.Write([]byte(`[{"id":1,"body":"one","user":{"login":"alice"}},{"id":2,"body":"two","user":{"login":"bob"}}]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"milestone", "issue", "list", "3"}, "1  Issue 1  closed\n2  Issue 2  closed\n"},
		{[]string{"issue", "close", "1", "2"}, "1  Issue 1  closed\n2  Issue 2  closed\n"},
		{[]string{"branch", "list"}, "main  abc  false\ndev   def  false\n"},
		{[]string{"issue", "comment", "list", "5"}, "1 alice\none\n\n2 bob\ntwo\n\n"},
	} {
		t.Run(tc.args[0]+" "+tc.args[1], func(t *testing.T) {
			deps, out, _, pager := pagedDeps(t, server.URL, nil, "pager", "fake-pager", "color", "never")
			require.NoError(t, Execute(context.Background(), deps, append(tc.args, "-R", "alice/project", "--yes")))
			require.Equal(t, 1, pager.calls)
			require.Equal(t, tc.want, pager.input)
			require.Equal(t, "PAGED:"+tc.want, out.String())
		})
	}
}

func TestListsHintWhenMorePagesExist(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, total := min(limit, 50), 120
		items := []string{}
		for id := (page-1)*size + 1; id <= min(page*size, total); id++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"username":"org%d","full_name":"owner/repo%d"}`, id, id, id))
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	for _, args := range [][]string{{"org", "list"}, {"repo", "list"}, {"issue", "list", "-R", "alice/project"}, {"pr", "list", "-R", "alice/project"}} {
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--limit", "100")))
		require.Len(t, strings.Split(strings.TrimSpace(out.String()), "\n"), 100, args)
		require.Equal(t, "more results available; use --page 2\n", diagnostics.String(), args)
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--limit", "100", "--page", "2")))
		require.Len(t, strings.Split(strings.TrimSpace(out.String()), "\n"), 20, args)
		require.Empty(t, diagnostics.String(), args)
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--json=id")))
		require.Empty(t, diagnostics.String(), args)
	}
}
