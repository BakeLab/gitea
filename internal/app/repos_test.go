// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

// repoTTYDeps returns serverDeps for a 100-column terminal without color or links, as people read
// gt's output, at a fixed time.
func repoTTYDeps(t *testing.T, serverURL string) (Dependencies, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	deps, out, diagnostics := serverDeps(t, serverURL)
	deps.Getenv = serverEnv(serverURL, map[string]string{"TERM": "dumb"})
	deps.Streams.OutTTY, deps.Streams.Width = true, 100
	deps.Now = func() time.Time { return testNow }
	return deps, out, diagnostics
}

const listedRepositories = `[
	{"full_name":"alice/old-stuff","description":"Archived repository","archived":true,"html_url":"https://gitea.test/alice/old-stuff","updated_at":"2026-10-11T18:00:00Z"},
	{"full_name":"acme/service","private":true,"fork":true,"html_url":"https://gitea.test/acme/service","updated_at":"2026-10-09T18:00:00Z"}
]`

func TestRepoListShowsColumns(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/user/repos", r.URL.Path)
		w.Header().Set("X-Total-Count", "2")
		_, _ = w.Write([]byte(listedRepositories))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "list"}))
	require.Equal(t, `Showing 2 of 2 repositories you can access

NAME             DESCRIPTION          INFO              UPDATED
alice/old-stuff  Archived repository  public, archived  about 2 hours ago
acme/service                          private, fork     about 2 days ago
`, out.String())
	deps, out, _ = serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "list"}))
	require.Equal(t, "alice/old-stuff\tArchived repository\tpublic, archived\t2026-10-11T18:00:00Z\thttps://gitea.test/alice/old-stuff\n"+
		"acme/service\t\tprivate, fork\t2026-10-09T18:00:00Z\thttps://gitea.test/acme/service\n", out.String())
}

func TestRepoListReportsAnEmptyCollection(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":3,"username":"acme"}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "list", "acme", "--type", "fork"}))
	require.Empty(t, out.String())
	require.Equal(t, "no forks owned by acme\n", diagnostics.String())
}

func TestRepoViewShowsStateAndReadme(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/bob/project":
			_, _ = w.Write([]byte(`{"full_name":"bob/project","description":"Demo","fork":true,"archived":true,"default_branch":"main",
				"stars_count":1,"forks_count":0,"open_issues_count":2,"open_pr_counter":1,"updated_at":"2026-10-11T19:00:00Z",
				"html_url":"https://gitea.test/bob/project","clone_url":"https://gitea.test/bob/project.git",
				"parent":{"full_name":"alice/project"}}`))
		case "/api/v1/repos/bob/project/contents/":
			require.Equal(t, "develop", r.URL.Query().Get("ref"))
			_, _ = w.Write([]byte(`[{"name":"docs","type":"dir"},{"name":"README","type":"file"},{"name":"readme.md","type":"file"}]`))
		case "/api/v1/repos/bob/project/raw/readme.md":
			_, _ = w.Write([]byte("Hello **there**"))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	rawMarkdown(t, deps)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "view", "bob/project", "--branch", "develop"}))
	require.Equal(t, `bob/project
public • fork • archived (read-only) • ★ 1 · 0 forks · 2 open issues · 1 open pull request · updated about 1 hour ago
Description:    Demo
Forked from:    alice/project
Default branch: main
Clone:          https://gitea.test/bob/project.git

Hello **there**

View this repository on Gitea: https://gitea.test/bob/project
`, out.String())
	deps, out, _ = serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "view", "bob/project", "-b", "develop"}))
	require.Equal(t, "title:\tbob/project\ndescription:\tDemo\nforked from:\talice/project\nmirror of:\t\ndefault branch:\tmain\nwebsite:\t\ntopics:\t\n"+
		"clone:\thttps://gitea.test/bob/project.git\nssh:\t\nurl:\thttps://gitea.test/bob/project\n--\nHello **there**\n", out.String())
}

func TestRepoSetDefaultValidatesTheRepository(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/alice/project" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	saved := "config --local gt.repository " + server.URL + "/alice/project"
	runner := &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", saved: ""}}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"repo", "set-default", "nobody/none"})
	require.Error(t, err)
	require.NotContains(t, strings.Join(runner.calls, "\n"), "config --local gt.repository")
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "set-default", "alice/project"}))
	require.Contains(t, runner.calls, saved)
	require.Equal(t, "✓ Set alice/project as the default repository for this clone\n", diagnostics.String())
	// Without an argument, a terminal chooses among the repositories of the remotes.
	runner.calls = nil
	err = Execute(context.Background(), deps, []string{"repo", "set-default"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "repo set-default requires a repository argument")
	require.NotContains(t, runner.calls, saved)
	runner.results[`config --get-regexp ^remote\..*\.url$`] = "remote.github.url https://github.com/example/project.git\nremote.origin.url " + server.URL + "/alice/project.git"
	prompter := &choosingPrompter{}
	deps.Prompter = prompter
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "set-default"}))
	require.Equal(t, []terminal.Choice{{Label: "alice/project (origin)", Value: server.URL + "/alice/project"}}, prompter.choices)
	require.Contains(t, runner.calls, saved)
}

func TestRepoSetDefaultViewAndUnset(t *testing.T) {
	deps, out, diagnostics := repoTTYDeps(t, "http://gitea.test")
	runner := &fakeGit{results: map[string]string{
		"rev-parse --is-inside-work-tree":      "true",
		"config --local --get gt.repository":   "http://gitea.test/alice/project",
		"config --local --unset gt.repository": "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "set-default", "--view"}))
	require.Equal(t, "http://gitea.test/alice/project\n", out.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "set-default", "-u"}))
	require.Equal(t, "✓ Removed the default repository http://gitea.test/alice/project from this clone\n", diagnostics.String())
	// Without a saved repository, both say so the same way, and stdout stays empty.
	deps.Git = &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", "config --local --get gt.repository": ""}}
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "set-default", "--view"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "set-default", "--unset"}))
	require.Empty(t, out.String())
	require.Equal(t, "! No default repository is set for this clone; set one with gt repo set-default OWNER/REPO\n! No default repository is set for this clone\n", diagnostics.String())
	deps.Git = &fakeGit{}
	err := Execute(context.Background(), deps, []string{"repo", "set-default", "-v"})
	require.ErrorContains(t, err, "set-default must run inside a Git clone")
}

func TestRepoViewOfAnEmptyRepositorySkipsTheReadme(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/empty", r.URL.Path)
		_, _ = w.Write([]byte(`{"full_name":"alice/empty","private":true,"empty":true,"html_url":"https://gitea.test/alice/empty"}`))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	rawMarkdown(t, deps)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "view", "alice/empty"}))
	require.Contains(t, out.String(), "private • empty • ★ 0")
	require.Contains(t, out.String(), "_This repository is empty._")
}
