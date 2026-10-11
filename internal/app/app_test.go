// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/tea/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestHelpWorksWithoutValidConfiguration(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"version"}, {"man"}, {"man", "pr", "checkout"}, {"completion", "bash"}, {"--help"}} {
		deps, out, _ := testDeps(t)
		require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "config.yml"), []byte("not: ["), 0o600))
		require.NoError(t, Execute(context.Background(), deps, args))
		require.NotEmpty(t, out.String())
	}
}

func TestAliasesAndConfigPersistence(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "prefs", "config list"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"prefs"}))
	require.Contains(t, out.String(), "git_protocol")
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "preferences", "prefs"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "prompt", "disabled"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "preferences"}))
	err := Execute(context.Background(), deps, []string{"alias", "set", "config", "issue list"})
	require.Error(t, err)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	s, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "disabled", s.Preferences.Prompt)
}

func TestBrokenAliasDoesNotBlockOtherCommands(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "config.yml"), []byte("version: 1\naliases:\n  status: config list\n  dangling: missing\n"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "list"}))
	require.Contains(t, out.String(), "git_protocol")
	err := Execute(context.Background(), deps, []string{"dangling"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "status"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "dangling"}))
}

func TestCommandLineEdgeCases(t *testing.T) {
	deps, out, _ := testDeps(t)
	err := Execute(context.Background(), deps, []string{"issue", "bogus"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	err = Execute(context.Background(), deps, []string{"isue"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "did you mean issue?")
	// A help flag supplied as a flag value must not bypass validation.
	err = Execute(context.Background(), deps, []string{"config", "list", "--jq", ".", "--remote", "-h"})
	require.ErrorContains(t, err, "--jq requires --json")
	require.NoError(t, Execute(context.Background(), deps, []string{"--version"}))
	require.NotContains(t, out.String(), "tea version")
}

func TestAliasesAfterGlobalFlagsAndNestedHelp(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "prefs", "config list"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"--no-input", "prefs"}))
	require.Contains(t, out.String(), "git_protocol")
	require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "config.yml"), []byte("broken: ["), 0o600))
	for _, args := range [][]string{{"issue", "--help"}, {"repo"}, {"auth", "login", "--help"}} {
		require.NoError(t, Execute(context.Background(), deps, args))
	}
}

func TestJoinJSONFields(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"issue", "list", "--json", "number,title"}, []string{"issue", "list", "--json=number,title"}},
		{[]string{"--json", "id", "org", "list"}, []string{"--json=id", "org", "list"}},
		{[]string{"issue", "list", "--json"}, []string{"issue", "list", "--json"}},
		{[]string{"search", "repo", "foo", "--json"}, []string{"search", "repo", "foo", "--json"}},
		{[]string{"issue", "view", "--json", "5"}, []string{"issue", "view", "--json", "5"}},
		{[]string{"repo", "clone", "--json", "alice/project"}, []string{"repo", "clone", "--json", "alice/project"}},
		{[]string{"issue", "list", "--json", "--state", "open"}, []string{"issue", "list", "--json", "--state", "open"}},
		{[]string{"search", "issues", "--", "--json", "id"}, []string{"search", "issues", "--", "--json", "id"}},
		{[]string{"issue", "list", "--json", "Number"}, []string{"issue", "list", "--json", "Number"}},
		{[]string{"search", "repo", "--json", "tea"}, []string{"search", "repo", "--json", "tea"}},
		{[]string{"search", "issue", "--json", "bug"}, []string{"search", "issue", "--json", "bug"}},
		{[]string{"repo", "clone", "--json", "project"}, []string{"repo", "clone", "--json", "project"}},
		{[]string{"search", "repo", "--json", "name,url", "tea"}, []string{"search", "repo", "--json=name,url", "tea"}},
		{[]string{"--json", "number", "my-alias"}, []string{"--json=number", "my-alias"}},
	} {
		deps, _, _ := testDeps(t)
		require.Equal(t, tc.want, joinJSONFields(NewRoot(deps), tc.args), tc.args)
	}
}

func TestJSONFieldsAcceptSpaceSeparatedValue(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user/orgs":
			_, _ = w.Write([]byte(`[{"id":1,"username":"team","full_name":"Team"}]`))
		case "/api/v1/repos/search":
			require.Equal(t, "foo", r.URL.Query().Get("q"))
			_, _ = w.Write([]byte(`{"data":[{"id":2,"full_name":"alice/foo"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{{"org", "list", "--json", "id,name"}, {"org", "list", "--json=id,name"}, {"--json", "id,name", "org", "list"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args), args)
		require.JSONEq(t, `[{"id":1,"name":"team"}]`, out.String(), args)
	}
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"org", "list", "--json"}))
	require.Contains(t, out.String(), `"full_name":"Team"`)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "repo", "foo", "--json"}))
	require.Contains(t, out.String(), `"full_name":"alice/foo"`)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "repo", "--json", "foo"}))
	require.Contains(t, out.String(), `"full_name":"alice/foo"`)
}

func TestSudoSendsHeaderAfterIdentityCheck(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Sudo"))
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":7,"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"--sudo", "bob", "repo", "view", "--repo", "alice/project", "--json=full_name"}))
	require.Equal(t, []string{"/api/v1/user ", "/api/v1/repos/alice/project bob"}, seen)
	require.JSONEq(t, `{"full_name":"alice/project"}`, out.String())
	requireUsageBeforeRequests(t, "--sudo cannot be used with auth commands", "auth", "status", "--sudo", "bob")
	requireUsageBeforeRequests(t, "--sudo requires a username", "repo", "view", "--sudo", "")
}
