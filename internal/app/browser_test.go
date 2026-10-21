// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

type recordingBrowser struct{ resource, command string }

func (b *recordingBrowser) Open(_ context.Context, resource, command string) error {
	b.resource, b.command = resource, command
	return nil
}

func TestRepositoryWebUsesInjectedBrowserAndConfigPrecedence(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = fmt.Fprint(w, `{"full_name":"alice/project","html_url":"https://example.org/alice/project"}`)
	}))
	defer server.Close()
	deps, out, diagnostics := testDeps(t)
	browser := &recordingBrowser{}
	deps.Browser = browser
	deps.Getenv = serverEnv(server.URL, map[string]string{"BROWSER": "environment-browser"})
	args := []string{"repo", "view", "--repo", "alice/project", "--web", "--no-input"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "https://example.org/alice/project", browser.resource)
	require.Equal(t, "environment-browser", browser.command)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "browser", "configured-browser"}))
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "configured-browser", browser.command)
	require.Equal(t, 2, calls)
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, append(args, "--json"))))
	require.Equal(t, 2, calls)
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}

func TestBranchWebPagesCheckTheBranch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","html_url":"https://example.org/alice/project"}`)
		case "/api/v1/repos/alice/project/branches/release/1.0":
			_, _ = fmt.Fprint(w, `{"name":"release/1.0"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{
		{"repo", "view", "--repo", "alice/project", "--web", "--branch", "release/1.0"},
		{"branch", "view", "release/1.0", "--repo", "alice/project", "--web"},
	} {
		browser := &recordingBrowser{}
		deps.Browser = browser
		require.NoError(t, Execute(context.Background(), deps, append(args, "--no-input")))
		require.Equal(t, "https://example.org/alice/project/src/branch/release/1.0", browser.resource)
	}
	for _, args := range [][]string{
		{"repo", "view", "--repo", "alice/project", "--web", "--branch", "nope"},
		{"branch", "view", "nope", "--repo", "alice/project", "--web"},
	} {
		browser := &recordingBrowser{}
		deps.Browser = browser
		require.EqualError(t, Execute(context.Background(), deps, append(args, "--no-input")), `branch "nope" not found in alice/project`)
		require.Empty(t, browser.resource)
	}
}

func TestDiscussionViewsShareBrowserContract(t *testing.T) {
	for _, group := range []string{"issue", "pr"} {
		t.Run(group, func(t *testing.T) {
			calls := 0
			resource := "https://example.org/alice/project/" + group + "/7"
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = fmt.Fprintf(w, `{"number":7,"title":"Discussion","html_url":%q}`, resource)
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			browser := &recordingBrowser{}
			deps.Browser = browser
			args := []string{group, "view", "7", "--repo", "alice/project", "--web", "--no-input"}
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, resource, browser.resource)
			require.Empty(t, out.String())
			require.Empty(t, diagnostics.String())
			require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, append(args, "--json"))))
			require.Equal(t, 1, calls)
		})
	}
}

func TestOpenBrowserTellsTerminalUsers(t *testing.T) {
	for _, tty := range []bool{true, false} {
		deps, out, diagnostics := testDeps(t)
		browser := &recordingBrowser{}
		deps.Browser = browser
		deps.Streams.OutTTY = tty
		a, cmd := outputApplication(deps, "")
		require.NoError(t, a.openBrowser(cmd, "http://localhost:3001/alice/project/issues/1"))
		require.Equal(t, "http://localhost:3001/alice/project/issues/1", browser.resource)
		require.Empty(t, out.String())
		if tty {
			require.Equal(t, "Opening localhost:3001/alice/project/issues/1 in your browser.\n", diagnostics.String())
		} else {
			require.Empty(t, diagnostics.String())
		}
	}
}
