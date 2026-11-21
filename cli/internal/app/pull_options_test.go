// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

func TestPullMergeAdminAndMatchHeadCommit(t *testing.T) {
	// merge runs gt against a server that has not merged anything yet, and returns what it was sent.
	merge := func(args ...string) (map[string]any, error) {
		var payload map[string]any
		server := newAuthenticatedServer(mergeServer(t, strings.ReplaceAll(openPull, "abc123", "abc1234567"), &payload))
		defer server.Close()
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project", "--method", "squash", "--yes"}, args...))
		return payload, err
	}

	payload, err := merge()
	require.NoError(t, err)
	require.Equal(t, false, payload["force_merge"], "a merge does not override the branch protection unless asked")

	payload, err = merge("--admin")
	require.NoError(t, err)
	require.Equal(t, true, payload["force_merge"])

	payload, err = merge("--match-head-commit", "ABC1234")
	require.NoError(t, err)
	require.Equal(t, "abc1234567", payload["head_commit_id"], "a prefix of the head matches, whatever its case, and the whole head is sent")

	payload, err = merge("--match-head-commit", "def4567")
	require.Equal(t, clierr.HeadMismatch, clierr.ExitCode(err))
	require.ErrorContains(t, err, "the head of pull request alice/project#3 (Add login) is abc1234, not def4567")
	require.Nil(t, payload, "a head that moved is not merged")

	for _, args := range [][]string{{"--match-head-commit", "xyz"}, {"--match-head-commit", "abc"}} {
		_, err := merge(args...)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), args)
	}
	for _, flag := range []string{"--admin", "--match-head-commit=abc1234"} {
		_, err := merge("--method", "manually-merged", "--commit", "abc1234", flag)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), flag)
	}
}

func TestPullMergeReportsAHeadThatMoved(t *testing.T) {
	// head is what Gitea reports after it refused the merge, and "" when it cannot be read.
	for head, want := range map[string]string{
		"def4567890": "the head of pull request alice/project#3 (Add login) is def4567, not abc1234; look at what was pushed and merge again",
		"abc1234567": "the head of pull request alice/project#3 (Add login) is no longer abc1234; look at what was pushed and merge again",
		"":           "the head of pull request alice/project#3 (Add login) is no longer abc1234; look at what was pushed and merge again",
	} {
		refused := false
		server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
				_, _ = w.Write([]byte(mergeRepository))
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" && refused && head == "":
				w.WriteHeader(http.StatusInternalServerError)
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" && refused:
				_, _ = w.Write([]byte(strings.ReplaceAll(openPull, "abc123", head)))
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
				_, _ = w.Write([]byte(strings.ReplaceAll(openPull, "abc123", "abc1234567")))
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge":
				refused = true
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"head out of date"}`))
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--method", "squash", "--yes", "--match-head-commit", "abc1234"})
		server.Close()
		require.Equal(t, clierr.HeadMismatch, clierr.ExitCode(err), head)
		require.EqualError(t, err, want, head)
	}
}

func TestPullMergeOfAMergedPullRequestWarnsWhateverItsHead(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, `{"number":3,"title":"Add login","state":"closed","merged":true,"base":{"ref":"main"},"head":{"ref":"feature","sha":"abc1234567"}}`, &payload))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Streams.ErrTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--method", "squash", "--yes", "--match-head-commit", "def4567"}))
	require.Contains(t, diagnostics.String(), "is already merged")
	require.Nil(t, payload)
}

func TestPullMergeDisableAutoCancelsTheScheduledMerge(t *testing.T) {
	var deletes int
	scheduled := true
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(mergeRepository))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
			_, _ = w.Write([]byte(openPull))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/git/refs/pull/3/head":
			writePullRef(w, 3, "abc123")
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge":
			deletes++
			if !scheduled {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`)) // as Gitea answers, with nothing to tell it from a missing pull request
				return
			}
			scheduled = false
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, diagnostics := serverDeps(t, server.URL)
		deps.Streams.ErrTTY = true
		err := Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project", "--disable-auto", "--no-input"}, args...))
		return out.String() + diagnostics.String(), err
	}

	out, err := run("--json", "number")
	require.NoError(t, err)
	require.Contains(t, out, `"number":3`)
	require.Equal(t, 1, deletes)
	out, err = run()
	require.NoError(t, err, "nothing scheduled is not a failure")
	require.Contains(t, out, "No merge is scheduled for pull request alice/project#3")
	require.Equal(t, 2, deletes)

	for _, flag := range []string{"--auto", "--squash", "--delete-branch", "--title=x", "--admin", "--match-head-commit=abc1234", "--editor"} {
		_, err := run(flag)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), flag)
		require.ErrorContains(t, err, strings.SplitN(flag, "=", 2)[0]+" cannot be combined with --disable-auto", flag)
	}
	require.Equal(t, 2, deletes, "a refused combination sends nothing")
}

func TestPullChecksRequiredShowsOnlyTheRequiredChecks(t *testing.T) {
	// What Gitea tells someone who does not administer the repository: no effective_branch_protection_name.
	branch := `{"name":"main","protected":true,"enable_status_check":true,"status_check_contexts":["ci/build","lint/*","{unit,e2e}/[ab]"]}`
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
		case "/api/v1/repos/alice/project/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"T","state":"open","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123"}}`))
		case "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(branch))
		case "/api/v1/repos/alice/project/statuses/abc123":
			writeFirstPage(w, r, `[{"id":1,"context":"ci/build","status":"success"},{"id":2,"context":"ci/optional","status":"failure"},{"id":3,"context":"lint/go","status":"success"},{"id":4,"context":"unit/a","status":"success"}]`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append([]string{"pr", "checks", "3", "-R", "alice/project", "--no-input"}, args...))
		return out.String(), err
	}

	_, err := run("--json", "name,state")
	require.Equal(t, 1, clierr.ExitCode(err), "an optional check that failed fails the command")

	out, err := run("--required", "--json", "name,state")
	require.NoError(t, err, "only the required checks count")
	require.JSONEq(t, `[{"name":"ci/build","state":"success"},{"name":"lint/go","state":"success"},{"name":"unit/a","state":"success"}]`, out, "patterns are globs as Gitea reads them")

	branch = `{"name":"main","enable_status_check":true,"status_check_contexts":["ci/build","docs"]}`
	out, err = run("--required", "--json", "name,state,description")
	require.Equal(t, clierr.Pending, clierr.ExitCode(err), "a required check that has not reported is pending")
	require.JSONEq(t, `[{"name":"ci/build","state":"success","description":""},{"name":"docs","state":"pending","description":"Waiting for status to be reported"}]`, out)

	branch = `{"name":"main","enable_status_check":true,"status_check_contexts":[]}`
	out, err = run("--required", "--json", "name")
	require.Equal(t, 1, clierr.ExitCode(err), "a rule without patterns requires every check")
	require.JSONEq(t, `[{"name":"ci/build"},{"name":"ci/optional"},{"name":"lint/go"},{"name":"unit/a"}]`, out)

	branch = `{"name":"main","status_check_contexts":["ci/optional"]}`
	out, err = run("--required")
	require.Equal(t, 1, clierr.ExitCode(err))
	require.EqualError(t, err, "no required checks reported on the 'feature' branch")
	require.Empty(t, out)

	require.Equal(t, clierr.Usage, clierr.ExitCode(func() error { _, err := run("--required", "--web"); return err }()))
}

func TestPullCreateFillFirstAndFillVerbose(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","permissions":{"push":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			payload = nil
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	create := func(log string, args ...string) (map[string]any, error) {
		deps, _, _ := serverDeps(t, server.URL)
		pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
		deps.Git = &fakeGit{results: map[string]string{
			"rev-parse --is-inside-work-tree":                "true",
			"rev-parse --verify refs/heads/feature^{commit}": "localsha",
			"remote":                             "origin",
			"remote get-url origin":              server.URL + "/alice/project.git",
			"config --get branch.feature.remote": "origin",
			"rev-list --count refs/remotes/origin/main..refs/heads/feature": "3",
			"rev-list --count origin/main..feature":                         "3",
			"log --reverse --format=%s%x1f%b%x1e origin/main..feature":      log,
			pushKey: "",
		}}
		payload = nil
		err := Execute(context.Background(), deps, append([]string{"pr", "create", "-R", "alice/project", "--head", "feature", "--base", "main", "--json=number", "--no-input", "--yes"}, args...))
		return payload, err
	}
	three := "Add the form\x1fIt validates.\x1eFix a typo\x1f\x1eHandle empty input\x1fNeeds a test too.\x1e"

	got, err := create(three, "--fill")
	require.NoError(t, err)
	require.Equal(t, "Feature", got["title"], "--fill keeps its rule: the branch name, made readable, for several commits")
	require.Equal(t, "", got["body"])

	got, err = create(three, "--fill-first")
	require.NoError(t, err)
	require.Equal(t, "Add the form", got["title"], "the first commit, although there are three")
	require.Equal(t, "It validates.", got["body"])

	got, err = create(three, "--fill-verbose")
	require.NoError(t, err)
	require.Equal(t, "Feature", got["title"])
	require.Equal(t, "Add the form\n\nIt validates.\n\n---\n\nFix a typo\n\n---\n\nHandle empty input\n\nNeeds a test too.", got["body"], "every message, oldest first")

	got, err = create(three, "--fill-first", "--title", "Mine", "--body", "Mine too")
	require.NoError(t, err)
	require.Equal(t, "Mine", got["title"], "a title or body that is given wins")
	require.Equal(t, "Mine too", got["body"])

	got, err = create(three, "--fill-first", "--title", "Mine")
	require.NoError(t, err)
	require.Equal(t, "Mine", got["title"])
	require.Equal(t, "It validates.", got["body"], "a given title replaces only the title")

	got, err = create("Add the form\x1fIt validates.\x1e", "--fill-verbose", "--title", "Mine")
	require.NoError(t, err)
	require.Equal(t, "It validates.", got["body"], "the subject of a single commit is not repeated in the body")

	for flag, want := range map[string]string{"--fill-first": "--fill-first needs", "--fill-verbose": "--fill-verbose needs"} {
		_, err = create("", flag)
		require.Equal(t, clierr.Failure, clierr.ExitCode(err), "without the commits there is nothing to fill from")
		require.ErrorContains(t, err, want+" the commits of feature that main lacks")
	}
}

func TestPullCreateAgitFillsFromTheCommits(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"has_pull_requests":true,"full_name":"alice/project","default_branch":"main","permissions":{"push":true}}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	verbose := base64.StdEncoding.EncodeToString([]byte("Add the form\n\nIt validates.\n\n---\n\nFix a typo"))
	for _, tc := range []struct{ flag, title, description string }{
		{"--fill-first", "Add the form", "It validates."},
		{"--fill-verbose", "Feature", "{base64}" + verbose},
	} {
		deps, _, _ := serverDeps(t, server.URL)
		pushKey := credentialCall(t, deps, server.URL, "alice", "push", "-o", "title="+tc.title, "-o", "description="+tc.description, "--", "origin", "refs/heads/feature:refs/for/main/feature")
		runner := &fakeGit{results: map[string]string{
			"remote":                                "origin",
			"remote get-url origin":                 server.URL + "/alice/project.git",
			"rev-parse --is-inside-work-tree":       "true",
			"rev-list --count origin/main..feature": "2",
			"log --reverse --format=%s%x1f%b%x1e origin/main..feature": "Add the form\x1fIt validates.\x1eFix a typo\x1f\x1e",
			pushKey: "",
		}}
		deps.Git = runner
		require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--head", "feature", "--base", "main", "--no-input", tc.flag}), tc.flag)
		require.Contains(t, runner.calls, pushKey, tc.flag)
	}
}
