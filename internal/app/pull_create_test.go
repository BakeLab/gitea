// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func publishCreateEnv(t *testing.T, creates *int) (Dependencies, *fakeGit, string) {
	t.Helper()
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","permissions":{"push":true}}`))
			return
		case "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
			return
		case "/api/v1/repos/alice/project/branches/nope":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"branch does not exist [name: nope]"}`))
			return
		case "/api/v1/repos/alice/project/pulls/7/reviews":
			writeFirstPage(w, r, `[{"id":1,"state":"APPROVED","official":true,"user":{"login":"carol"}}]`)
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		*creates++
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Contains(t, []any{"feature", "bob:feature"}, payload["head"])
		_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
	}))
	t.Cleanup(server.Close)
	deps, _, _ := serverDeps(t, server.URL)
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
	runner := &fakeGit{results: map[string]string{
		"rev-parse --is-inside-work-tree":                "true",
		"rev-parse --verify refs/heads/feature^{commit}": "localsha",
		"remote":                             "origin",
		"remote get-url origin":              server.URL + "/alice/project.git",
		"config --get branch.feature.remote": "origin",
		pushKey:                              "",
	}}
	deps.Git = runner
	return deps, runner, pushKey
}

func TestPullCreateAgitPushesToMatchingRemoteWithoutRemoteFlag(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","default_branch":"main","permissions":{"push":true}}`))
		case r.URL.Path == "/api/v1/orgs/alice", strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "-o", "title=Title", "--", "gitea", "refs/heads/feature:refs/for/main/feature")
	runner := &fakeGit{results: map[string]string{
		"remote":                          "origin gitea",
		"remote get-url origin":           server.URL + "/bob/project.git",
		"remote get-url gitea":            server.URL + "/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		pushKey:                           "",
	}}
	deps.Git = runner
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.PullForm = &recordingPullForm{values: terminal.PullFormValues{
		IssueFormValues: terminal.IssueFormValues{Title: "Title"}, Agit: true, Base: "main", Head: "feature",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--title", "Title", "--head", "feature", "--base", "main", "--no-input"}))
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--interactive"}))
	pushes := 0
	for _, call := range runner.calls {
		if strings.Contains(call, " push ") {
			require.Equal(t, pushKey, call)
			pushes++
		}
	}
	require.Equal(t, 2, pushes)
	runner.results["remote"] = "one two"
	runner.results["remote get-url one"], runner.results["remote get-url two"] = server.URL+"/alice/project.git", server.URL+"/alice/project.git"
	err := Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--title", "Title", "--head", "feature", "--base", "main", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "select --remote")
	err = Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--title", "Title", "--head", "main", "--base", "main", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "the AGit topic main must differ from the base branch")
}

func TestPullCreateAgitRecordsThePullRequestOfTheBranch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls" {
			_, _ = w.Write([]byte(`[{"number":6,"head":{"ref":"refs/pull/6/head","label":"","sha":"othersha"},"base":{"ref":"main"}},
				{"number":5,"title":"Title","html_url":"https://example.test/alice/project/pulls/5","head":{"ref":"refs/pull/5/head","label":"","sha":"localsha"},"base":{"ref":"main"}}]`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "-o", "title=Title", "--", "origin", "refs/heads/feature:refs/for/main/feature")
	runner := &fakeGit{results: map[string]string{
		"remote":                          "origin",
		"remote get-url origin":           server.URL + "/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		"rev-parse --verify refs/heads/feature^{commit}":     "localsha",
		"config branch.feature.gitea-cli-pr 5":               "",
		"config branch.feature.gitea-cli-base-remote origin": "",
		pushKey: "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--title", "Title", "--head", "feature", "--base", "main", "--no-input"}))
	require.Contains(t, runner.calls, pushKey)
	require.Contains(t, runner.calls, "config branch.feature.gitea-cli-pr 5")
	require.Contains(t, runner.calls, "config branch.feature.gitea-cli-base-remote origin")
	require.Equal(t, "https://example.test/alice/project/pulls/5\n", out.String(), "the URL is printed like for other created pull requests")
}

func TestPullCreateSkipsPushWhenHeadAlreadyPublished(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	runner.results["for-each-ref --format=%(objectname) refs/remotes/origin/feature"] = "localsha"
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"}))
	require.Equal(t, 1, creates)
	require.NotContains(t, runner.calls, pushKey)
	for _, call := range runner.calls {
		require.NotContains(t, call, "push --")
	}
}

func TestPullCreateWithoutARemoteOfTheBaseIgnoresTheRemotePreference(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.Remote = "origin"; return nil }))
	runner.results["remote get-url origin"] = "https://github.com/example/project.git"
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"}))
	require.Equal(t, 1, creates)
	require.NotContains(t, runner.calls, pushKey)
}

func TestPullCreateChecksTheBaseBeforePushing(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "nope", "--no-input", "--yes"})
	require.EqualError(t, err, `branch "nope" not found in alice/project`)
	require.NotContains(t, runner.calls, pushKey)
	require.Zero(t, creates)
}

func TestPullCreateHelpListsYesForThePush(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--help"}))
	require.Contains(t, out.String(), "-y, --yes ")
	require.Contains(t, out.String(), "pass --yes to push it")
}

func TestPullCreatePushesWithYesWhenHeadUnpublished(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number,reviews", "--no-input", "--yes"}))
	require.Equal(t, 1, creates)
	require.Contains(t, runner.calls, pushKey)
	require.Contains(t, deps.Streams.Out.(*bytes.Buffer).String(), `"reviews":[{"author":"carol"`)
}

func TestPullCreateRefusesARepositoryWithoutPullRequestsBeforePushing(t *testing.T) {
	enabled, reads, creates := false, 0, 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project":
			reads++
			_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","default_branch":"main","has_pull_requests":%t,"permissions":{"push":true}}`, enabled)
		case "GET /api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case "POST /api/v1/repos/alice/project/pulls":
			creates++
			_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
	newRunner := func() *fakeGit {
		return &fakeGit{results: map[string]string{
			"rev-parse --is-inside-work-tree":                "true",
			"rev-parse --verify refs/heads/feature^{commit}": "localsha",
			"symbolic-ref --quiet HEAD":                      "feature",
			"remote":                                         "origin",
			"remote get-url origin":                          server.URL + "/alice/project.git",
			"config --get branch.feature.remote":             "origin",
			pushKey:                                          "",
		}}
	}
	for _, args := range [][]string{
		{"--title", "PR", "--head", "feature", "--base", "main", "--no-input", "--yes"},
		{"--title", "PR", "--head", "feature", "--no-input", "--yes"},
		{"--agit", "--title", "PR", "--head", "feature", "--base", "main", "--no-input"},
		{"--interactive"},
	} {
		reads = 0
		runner, form := newRunner(), &recordingPullForm{}
		deps.Git, deps.PullForm = runner, form
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		err := Execute(context.Background(), deps, append([]string{"pr", "create", "-R", "alice/project"}, args...))
		require.EqualError(t, err, "pull requests are disabled in alice/project; turn them on with: gt repo edit --enable-pull-requests -R alice/project", args)
		require.NotContains(t, strings.Join(runner.calls, "\n"), "push", args)
		require.Zero(t, form.calls, "the form does not ask for a pull request the repository refuses")
		require.Equal(t, 1, reads, args)
	}
	enabled, reads = true, 0
	runner := newRunner()
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--no-input", "--yes"}))
	require.Contains(t, runner.calls, pushKey)
	require.Equal(t, 1, creates)
	require.Equal(t, 1, reads, "the repository is read once for its default branch, pull requests and permissions")
}

func TestPullCreatePushesANewBranchToTheForkInAForkClone(t *testing.T) {
	for _, test := range []struct {
		name, fork, remote, merge, want string
		readOnly                        bool
	}{
		{name: "without upstream", fork: "alice", want: "alice:feature"},
		{name: "started from the base branch", fork: "alice", remote: "upstream", merge: "refs/heads/main", want: "alice:feature"},
		{name: "tracking the fork", fork: "alice", remote: "origin", merge: "refs/heads/feature", want: "alice:feature"},
		{name: "fork of an organization", fork: "team", want: "team:feature"},
		{name: "tracking the base", fork: "alice", remote: "upstream", merge: "refs/heads/feature", want: "feature"},
		{name: "fork without push access", fork: "bob", readOnly: true, want: "feature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var head any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project/branches/main":
					_, _ = w.Write([]byte(`{"name":"main"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project":
					_, _ = w.Write([]byte(`{"full_name":"acme/project","has_pull_requests":true}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/"+test.fork+"/project":
					_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"` + test.fork + `/project","fork":true,"parent":{"full_name":"acme/project"},"permissions":{"push":` + strconv.FormatBool(!test.readOnly) + `}}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/project/pulls":
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					head = payload["head"]
					_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			remote := "origin"
			if !strings.Contains(test.want, ":") {
				remote = "upstream"
			}
			pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", remote, "refs/heads/feature")
			runner := &fakeGit{results: map[string]string{
				"rev-parse --is-inside-work-tree":                "true",
				"rev-parse --verify refs/heads/feature^{commit}": "localsha",
				"remote":                  "origin upstream",
				"remote get-url origin":   server.URL + "/" + test.fork + "/project.git",
				"remote get-url upstream": server.URL + "/acme/project.git",
				pushKey:                   "",
			}}
			if test.remote != "" {
				runner.results["config --get branch.feature.remote"] = test.remote
				runner.results["config --get branch.feature.merge"] = test.merge
			}
			deps.Git = runner
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input", "--yes"}))
			require.Contains(t, runner.calls, pushKey)
			require.Equal(t, test.want, head)
		})
	}
}

func TestPullCreateGoesThroughAForkWithoutPushAccessToTheBase(t *testing.T) {
	for _, test := range []struct {
		name, remotes, forkRepo, pushRemote string
		tty, forks                          bool
		want, err                           string
	}{
		{name: "fork remote", remotes: "origin fork", want: "alice:feature"},
		{name: "fork on a terminal", tty: true, forks: true, want: "alice:feature"},
		{name: "fork remote elsewhere", remotes: "origin fork", forkRepo: "bob/other", pushRemote: "alice", tty: true, forks: true, want: "alice:feature"},
		{name: "fork remotes elsewhere", remotes: "origin fork alice", forkRepo: "bob/other", tty: true, err: "you cannot push to acme/project, and Git remotes fork and alice point elsewhere; add a remote of your fork with gt repo fork --remote --remote-name NAME, then rerun gt pr create"},
		{name: "no fork without a terminal", err: "you cannot push to acme/project; fork it with gt repo fork --remote, then rerun gt pr create"},
		{name: "missing base", tty: true, err: `branch "main" not found in acme/project`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var head any
			forked := false
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project":
					_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"acme/project","permissions":{"push":false}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
					_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","fork":true,"parent":{"full_name":"acme/project"},"permissions":{"push":true}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project/branches/main" && test.name != "missing base":
					_, _ = w.Write([]byte(`{"name":"main"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/project/forks":
					forked = true
					_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","clone_url":"https://example.test/alice/project.git"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/project/pulls":
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					head = payload["head"]
					_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			pushRemote, forkRepo := cmp.Or(test.pushRemote, "fork"), cmp.Or(test.forkRepo, "alice/project")
			pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", pushRemote, "refs/heads/feature")
			recordKey := "config branch.feature.gitea-cli-base-remote origin"
			runner := &fakeGit{results: map[string]string{
				"rev-parse --is-inside-work-tree":                "true",
				"rev-parse --verify refs/heads/feature^{commit}": "localsha",
				"remote":                "origin",
				"remote get-url origin": server.URL + "/acme/project.git",
				"remote get-url fork":   server.URL + "/" + forkRepo + ".git",
				"remote add -- " + pushRemote + " https://example.test/alice/project.git": "",
				pushKey:   "",
				recordKey: "",
			}}
			if test.pushRemote != "" {
				runner.results["remote get-url "+pushRemote] = server.URL + "/alice/project.git"
			} else {
				runner.results["remote get-url alice"] = server.URL + "/bob/other.git"
			}
			if test.remotes != "" {
				runner.results["remote"] = test.remotes
			}
			deps.Git = runner
			args := []string{"pr", "create", "--title", "PR", "--head", "feature", "--base", "main"}
			if test.tty {
				deps.Prompter = &scriptedPrompter{accept: true}
				deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			} else {
				args = append(args, "--no-input", "--yes")
			}
			err := Execute(context.Background(), deps, args)
			require.Equal(t, test.forks, forked)
			if test.err != "" {
				require.EqualError(t, err, test.err)
				require.NotContains(t, runner.calls, pushKey)
				require.Nil(t, head)
				return
			}
			require.NoError(t, err)
			require.Contains(t, runner.calls, pushKey)
			require.Contains(t, runner.calls, recordKey) // so that discovery keeps the base rather than the fork the branch tracks
			require.Equal(t, test.want, head)
		})
	}
}

func TestPullCreatePushesANewBranchToAForkRemoteOnlyOutsideACloneOfTheBase(t *testing.T) {
	for _, test := range []struct {
		name, origin, remote, want string
	}{
		{name: "clone of the base with a fork remote", origin: "/acme/project.git", remote: "origin", want: "feature"},
		{name: "origin elsewhere with a fork remote", origin: "https://elsewhere.test/acme/project.git", remote: "fork", want: "alice:feature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var head any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
					_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","fork":true,"parent":{"full_name":"acme/project"},"permissions":{"push":true}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project":
					_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"acme/project","permissions":{"push":true}}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project/branches/main":
					_, _ = w.Write([]byte(`{"name":"main"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/acme/project/pulls":
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					head = payload["head"]
					_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			origin := test.origin
			if strings.HasPrefix(origin, "/") {
				origin = server.URL + origin
			}
			pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", test.remote, "refs/heads/feature")
			runner := &fakeGit{results: map[string]string{
				"rev-parse --is-inside-work-tree":                "true",
				"rev-parse --verify refs/heads/feature^{commit}": "localsha",
				"remote":                "origin fork",
				"remote get-url origin": origin,
				"remote get-url fork":   server.URL + "/alice/project.git",
				pushKey:                 "",
			}}
			deps.Git = runner
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "acme/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input", "--yes"}))
			require.Contains(t, runner.calls, pushKey)
			require.Equal(t, test.want, head)
		})
	}
}

func TestPullCreateWebPublishesTheHeadBeforeOpeningTheComparison(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","fork":true,"parent":{"full_name":"acme/project"},"permissions":{"push":true}}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project/branches/main" {
			_, _ = w.Write([]byte(`{"name":"main"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/project" {
			_, _ = w.Write([]byte(`{"full_name":"acme/project","has_pull_requests":true}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	browser := &recordingBrowser{}
	deps.Browser = browser
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
	runner := &fakeGit{results: map[string]string{
		"rev-parse --is-inside-work-tree":                "true",
		"rev-parse --verify refs/heads/feature^{commit}": "localsha",
		"remote":                  "origin upstream",
		"remote get-url origin":   server.URL + "/alice/project.git",
		"remote get-url upstream": server.URL + "/acme/project.git",
		pushKey:                   "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "acme/project", "--head", "feature", "--base", "main", "--web", "--no-input", "--yes"}))
	require.Contains(t, runner.calls, pushKey)
	require.True(t, strings.HasSuffix(browser.resource, "/acme/project/compare/main...alice:feature"), browser.resource)
}

func TestPullCreateRefusesAHeadWithoutCommitsBeforePushing(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	runner.results["rev-list --count refs/remotes/origin/main..refs/heads/feature"] = "0"
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--fill", "--head", "feature", "--base", "main", "--no-input", "--yes"})
	require.EqualError(t, err, "no commits between main and feature; commit your changes to feature first")
	require.Zero(t, creates)
	require.NotContains(t, runner.calls, pushKey)

	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--fill", "--head", "bob:feature", "--base", "main", "--json=number", "--no-input", "--yes"}), "the local feature is not bob's")
	require.Equal(t, 1, creates)

	runner.results["rev-list --count refs/remotes/origin/main..refs/heads/feature"] = "2"
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--fill", "--head", "feature", "--base", "main", "--json=number", "--no-input", "--yes"}))
	require.Equal(t, 2, creates)
}

func TestPullCreateRequiresYesWhenHeadUnpublishedWithoutInput(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--yes")
	require.Zero(t, creates)
	require.NotContains(t, runner.calls, pushKey)
}

func TestPullCreateCanceledWhenPublishDeclined(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	prompt := &scriptedPrompter{accept: false}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	require.Len(t, prompt.confirmations, 1)
	require.Zero(t, creates)
	require.NotContains(t, runner.calls, pushKey)
}

func TestPullCreateAllowMaintainerEditsDefaultAndOverride(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  bool
	}{
		{"default true", nil, true},
		{"explicit false", []string{"--allow-maintainer-edits=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				_, _ = w.Write([]byte(`{"number":7,"title":"PR"}`))
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			args := append([]string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"}, tc.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.want, payload["allow_maintainer_edit"])
		})
	}
}

func TestPullCreateRequestsReviewsAndUploadsAttachments(t *testing.T) {
	var reviewPayload map[string]any
	var uploaded []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/alice":
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v1/orgs/alice/teams" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(`[{"id":2,"name":"core"}]`))
		case r.URL.Path == "/api/v1/orgs/alice/teams":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/users/"):
			_, _ = w.Write([]byte(`{"login":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/users/") + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/7/requested_reviewers":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reviewPayload))
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"PR","requested_reviewers":[{"login":"bob"},{"login":"carol"}],"requested_reviewers_teams":[{"name":"core"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/7/reviews":
			writeFirstPage(w, r, `[{"id":1,"state":"REQUEST_REVIEW","user":{"login":"bob"}},{"id":2,"state":"REQUEST_REVIEW","user":{"login":"carol"}},{"id":3,"state":"REQUEST_REVIEW","team":{"name":"core"}}]`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/issues/7/assets":
			file, header, err := r.FormFile("attachment")
			require.NoError(t, err)
			_ = file.Close()
			uploaded = append(uploaded, header.Filename)
			if header.Filename == "broken.txt" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"id":1,"name":"` + header.Filename + `"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	for _, name := range []string{"log.txt", "broken.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	deps, out, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{
		"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number,reviewers,reviewer_teams", "--no-input",
		"--reviewer", "bob,carol", "--reviewer-team", "core", "--attachment", filepath.Join(dir, "log.txt"), "--attachment", filepath.Join(dir, "broken.txt"),
	})
	require.ErrorContains(t, err, "pull request #7 was created")
	require.ErrorContains(t, err, "broken.txt")
	require.Equal(t, map[string]any{"reviewers": []any{"bob", "carol"}, "team_reviewers": []any{"core"}}, reviewPayload)
	require.Equal(t, []string{"log.txt", "broken.txt"}, uploaded)
	require.JSONEq(t, `{"number":7,"reviewers":["bob","carol"],"reviewer_teams":["core"]}`, out.String())
}

func TestPullCreateRejectsUnknownReviewerTeamBeforeCreating(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/alice":
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/api/v1/orgs/alice/teams":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--no-input", "--reviewer-team", "ghost"})
	require.ErrorContains(t, err, `team "ghost" is unavailable`)
}

func TestPullCreateRejectsUnknownUsersBeforePushing(t *testing.T) {
	// Gitea looks a reviewer up by email, but takes assignees by name only.
	for _, tc := range []struct{ flag, ghost string }{{"--reviewer", "ghost"}, {"--assignee", "ghost"}, {"--assignee", "ghost@example.test"}} {
		t.Run(tc.flag+" "+tc.ghost, func(t *testing.T) {
			creates := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/bob":
					_, _ = w.Write([]byte(`{"login":"bob"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/"+tc.ghost:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"user redirect does not exist [name: ` + tc.ghost + `]"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
					creates++
					_, _ = w.Write([]byte(`{"number":7,"title":"PR"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
			runner := &fakeGit{results: map[string]string{
				"rev-parse --is-inside-work-tree":                "true",
				"rev-parse --verify refs/heads/feature^{commit}": "localsha",
				"remote":                "origin",
				"remote get-url origin": server.URL + "/alice/project.git",
				pushKey:                 "",
			}}
			deps.Git = runner
			err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--no-input", "--yes", tc.flag, "bob," + tc.ghost})
			require.ErrorContains(t, err, `user "`+tc.ghost+`" not found`)
			require.NotContains(t, runner.calls, pushKey)
			require.Zero(t, creates)
		})
	}
}

func TestPullCreateDerivesTitleAndBodyFromSingleCommit(t *testing.T) {
	message := "Add login form\n\nExplains why."
	for _, tc := range []struct {
		name, title, body string
		results           map[string]string
		flags             []string
	}{
		{"single commit", "Add login form", "Explains why.", map[string]string{"rev-list --count origin/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, nil},
		{"base remote", "Add login form", "Explains why.", map[string]string{"rev-list --count upstream/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, nil},
		{"local base", "Add login form", "", map[string]string{"rev-list --count main..feature": "1", "log -1 --format=%s%n%n%b feature": "Add login form"}, nil},
		{"explicit body kept", "Add login form", "Mine", map[string]string{"rev-list --count origin/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, []string{"--body", "Mine"}},
		{"explicit title kept", "Mine", "", map[string]string{"rev-list --count origin/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, []string{"--title", "Mine"}},
		{"several commits", "Feature", "", map[string]string{"rev-list --count origin/main..feature": "2", "log -1 --format=%s%n%n%b feature": message}, nil},
		{"git failure", "Feature", "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				_, _ = w.Write([]byte(`{"number":7}`))
			}))
			defer server.Close()
			deps, _, diagnostics := serverDeps(t, server.URL)
			deps.Git = &fakeGit{results: tc.results}
			args := append([]string{"pr", "create", "-R", "alice/project", "--head", "feature", "--base", "main", "--json=number", "--no-input"}, tc.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.title, payload["title"])
			require.Equal(t, tc.body, payload["body"])
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestPullCreateEditorSeedsPullRequestTemplate(t *testing.T) {
	var payload map[string]any
	var mu sync.Mutex
	var templateRequests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			require.Equal(t, "main", r.URL.Query().Get("ref"))
			mu.Lock()
			templateRequests = append(templateRequests, strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"))
			mu.Unlock()
			if r.URL.Path != "/api/v1/repos/alice/project/raw/.gitea/PULL_REQUEST_TEMPLATE.md" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte("---\nname: Pull request\ntitle: \"[PR] \"\n---\n## Checklist\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = w.Write([]byte(`{"number":7}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	editor, _ := newDraftEditor(t, "filled")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--editor", "--json=number", "--no-input"}))
	require.Equal(t, "## Checklist\n", editor.initial)
	require.ElementsMatch(t, []string{
		"PULL_REQUEST_TEMPLATE.md", "pull_request_template.md",
		".gitea/PULL_REQUEST_TEMPLATE.md", ".gitea/pull_request_template.md",
		".github/PULL_REQUEST_TEMPLATE.md", ".github/pull_request_template.md",
	}, templateRequests)
	require.Equal(t, "filled", payload["body"])
}

func TestPullCreateHelpListsDefaultsOnce(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--help"}))
	require.Contains(t, out.String(), "(default true)")
	require.NotContains(t, out.String(), "(default true) (default true)")
}

func TestPullCreateBranchFlagsSkipTheFormOnATerminal(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	form := &recordingPullForm{}
	deps.PullForm = form
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--head", "w3", "--base", "main", "--json=number"}))
	require.Zero(t, form.calls)
	require.Equal(t, "W3", payload["title"])
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--help"}))
	require.Contains(t, strings.Join(strings.Fields(out.String()), " "), "Any flag of this command, such as --fill, --head or --base (but not global flags such as --repo), skips the questions")
}

func TestCompareRefEscapesBranchNames(t *testing.T) {
	require.Equal(t, "feature/login", compareRef("feature/login"))
	require.Equal(t, "fix%2312", compareRef("fix#12"))
	require.Equal(t, "bob:fix%25x", compareRef("bob:fix%x"))
}

func TestPullCreateRefusesHeadEqualToBaseBeforePushing(t *testing.T) {
	creates := 0
	deps, runner, _ := publishCreateEnv(t, &creates)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "main", "--base", "main", "--no-input", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, `--head cannot be the base branch "main"`)
	runner.results["symbolic-ref --quiet HEAD"] = "refs/heads/main"
	err = Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--base", "main", "--no-input", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `must be on a branch named differently than "main"`)
	require.Zero(t, creates)
	for _, call := range runner.calls {
		require.NotContains(t, call, "push")
	}
}

type recordingPullForm struct {
	calls   int
	values  terminal.PullFormValues
	choices terminal.PullFormChoices
}

func (f *recordingPullForm) CreatePull(_ context.Context, choices terminal.PullFormChoices) (terminal.PullFormValues, error) {
	f.calls++
	f.choices = choices
	return f.values, nil
}

func TestPullCreateImplicitInteractiveForm(t *testing.T) {
	deadline := time.Date(2028, 1, 15, 0, 0, 0, 0, time.UTC)
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/raw/.github/pull_request_template.md":
			_, _ = w.Write([]byte(map[string]string{"main": "## Summary\n", "release": "## Release\n"}[r.URL.Query().Get("ref")]))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
			_, _ = w.Write([]byte(`{"number":9,"title":"Interactive","html_url":"https://example.org/alice/project/pulls/9","state":"open","base":{"ref":"main"},"head":{"ref":"feature"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	allow := true
	form := &recordingPullForm{values: terminal.PullFormValues{
		IssueFormValues: terminal.IssueFormValues{
			Title: "Interactive", Body: "Body text", Assignees: []string{"alice"}, Milestone: 4, Labels: []int64{2}, Deadline: &deadline,
		},
		Base: "main", Head: "feature", AllowMaintainerEdits: allow,
	}}
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.PullForm = form
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":                                       "feature",
		"rev-parse --is-inside-work-tree":                                 "true",
		"rev-parse --verify refs/heads/feature^{commit}":                  "localsha",
		"for-each-ref --format=%(objectname) refs/remotes/origin/feature": "localsha",
		"remote":                                "origin",
		"remote get-url origin":                 server.URL + "/alice/project.git",
		"config --get branch.feature.remote":    "origin",
		"rev-list --count origin/main..feature": "1",
		"log -1 --format=%s%n%n%b feature":      "Add login\n\nDetails",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--json=number,url", "--interactive"}))
	require.Equal(t, 1, form.calls)
	require.Equal(t, "Add login", form.choices.Title)
	require.Equal(t, "## Summary\n", form.choices.Body)
	seeded, err := form.choices.SeedBody(context.Background(), "release", "feature")
	require.NoError(t, err)
	require.Equal(t, "## Release\n", seeded)
	require.NotNil(t, form.choices.EditBody)
	require.True(t, form.choices.CanPush)
	require.Equal(t, "main", form.choices.Base)
	require.Equal(t, "feature", form.choices.HeadBranch)
	require.Equal(t, []string{"alice", "bob"}, form.choices.Assignees)
	require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, form.choices.Milestones)
	require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, form.choices.Labels)
	require.Empty(t, prompt.confirmations, "submitting the form creates the pull request")
	require.Equal(t, "alice/project", form.choices.Repo)
	require.Equal(t, "Interactive", createPayload["title"])
	require.Equal(t, "Body text", createPayload["body"])
	require.Equal(t, "main", createPayload["base"])
	require.Equal(t, "feature", createPayload["head"])
	require.Equal(t, true, createPayload["allow_maintainer_edit"])
	require.Equal(t, []any{"alice"}, createPayload["assignees"])
	require.Equal(t, float64(4), createPayload["milestone"])
	require.Equal(t, []any{float64(2)}, createPayload["labels"])
	require.Equal(t, "2028-01-15T23:59:59Z", createPayload["due_date"])
	require.JSONEq(t, `{"number":9,"url":"https://example.org/alice/project/pulls/9"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestPullCreateDefaultsFromForkBranchWithoutTTY(t *testing.T) {
	for _, remote := range []string{"fork", "pulls/bob/project"} {
		t.Run(remote, func(t *testing.T) { testPullCreateDefaultsFromForkBranch(t, remote) })
	}
}

func testPullCreateDefaultsFromForkBranch(t *testing.T, remote string) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","default_branch":"main"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = w.Write([]byte(`{"number":1,"html_url":"https://example.org/pulls/1"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	form := &recordingPullForm{}
	deps, _, _ := serverDeps(t, server.URL)
	deps.PullForm = form
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":           "über-fix",
		"config --get branch.über-fix.remote": remote,
		"config --get branch.über-fix.merge":  "refs/heads/über-fix",
		"remote get-url " + remote:            server.URL + "/bob/project.git",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--no-input"}))
	require.Zero(t, form.calls)
	require.Equal(t, "Über Fix", payload["title"])
	require.Equal(t, "bob:über-fix", payload["head"])
	require.Equal(t, "main", payload["base"])
}

func TestPullCreatePushesAForkBranchThatTracksItsNamesake(t *testing.T) {
	for _, test := range []struct {
		name, head, merge, want string
		push                    bool
	}{
		{name: "current branch of a fork clone", want: "bob:feature", push: true},
		{name: "current branch started from the fork's main", merge: "refs/heads/main", want: "bob:feature", push: true},
		{name: "explicit OWNER:BRANCH it tracks", head: "bob:feature", want: "bob:feature", push: true},
		{name: "OWNER:BRANCH of another owner", head: "carol:feature", want: "carol:feature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var head any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls" {
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					head = payload["head"]
					_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/branches/main" {
					_, _ = w.Write([]byte(`{"name":"main"}`))
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project" {
					_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/bob/project" {
					_, _ = w.Write([]byte(`{"full_name":"bob/project","has_pull_requests":true,"fork":true,"parent":{"full_name":"alice/project"},"permissions":{"push":true}}`))
					return
				}
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
			runner := &fakeGit{results: map[string]string{
				"symbolic-ref --quiet HEAD":                      "refs/heads/feature",
				"rev-parse --is-inside-work-tree":                "true",
				"rev-parse --verify refs/heads/feature^{commit}": "localsha",
				"remote":                             "origin",
				"config --get branch.feature.remote": "origin",
				"config --get branch.feature.merge":  cmp.Or(test.merge, "refs/heads/feature"),
				"remote get-url origin":              server.URL + "/bob/project.git",
				"for-each-ref --format=%(objectname) refs/remotes/origin/feature": "oldsha",
				pushKey: "",
			}}
			deps.Git = runner
			args := []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--base", "main", "--json=number", "--no-input", "--yes"}
			if test.head != "" {
				args = append(args, "--head", test.head)
			}
			require.NoError(t, Execute(context.Background(), deps, args))
			if test.push {
				require.Contains(t, runner.calls, pushKey)
			} else {
				require.NotContains(t, runner.calls, pushKey)
			}
			require.Equal(t, test.want, head)
		})
	}
}
