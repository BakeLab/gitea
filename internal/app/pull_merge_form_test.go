// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

// mergeForm answers the merge form: pick selects among pull requests, options returns the merge options.
type mergeForm struct {
	pick    func([]terminal.MergePullChoice) int64
	options func(terminal.MergeFormChoices) terminal.MergeFormValues
	asked   []terminal.MergeFormChoices
}

func (f *mergeForm) SelectPull(_ context.Context, choices []terminal.MergePullChoice) (int64, error) {
	return f.pick(choices), nil
}

func (f *mergeForm) MergeOptions(_ context.Context, choices terminal.MergeFormChoices) (terminal.MergeFormValues, error) {
	f.asked = append(f.asked, choices)
	return f.options(choices), nil
}

// mergeRepository allows merge, rebase and squash merges in alice/project and squashes and deletes
// branches by default.
const mergeRepository = `{"full_name":"alice/project","allow_merge_commits":true,"allow_rebase":true,"allow_squash_merge":true,"default_merge_style":"squash","default_delete_branch_after_merge":true}`

// mergeServer serves pull request 3, "Add login" from feature into main, and records merges into
// payload. After a merge the pull request reads as merged with its head branch deleted.
func mergeServer(t *testing.T, pull string, payload *map[string]any) http.Handler {
	t.Helper()
	merged := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(mergeRepository))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" && merged:
			_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","merged":true,"base":{"ref":"main"},"head":{"ref":"refs/pull/3/head","label":"feature","sha":"abc123"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
			_, _ = w.Write([]byte(pull))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge":
			merged = true
			require.NoError(t, json.NewDecoder(r.Body).Decode(payload))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

const openPull = `{"number":3,"title":"Add login","state":"open","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`

func TestPullMergeUsesTheRepositoryDefaultsAndKeepsTheLocalBranch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		local bool
	}{
		{"repository default", nil, false},
		{"flag", []string{"--delete-branch"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			fake := &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "feature"}}
			deps.Git = fake
			require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project", "--yes"}, tc.flags...)))
			require.Equal(t, "squash", payload["Do"])
			require.Equal(t, true, payload["delete_branch_after_merge"])
			require.Empty(t, out.String())
			require.Empty(t, diagnostics.String())
			require.Equal(t, tc.local, slices.Contains(fake.calls, "rev-parse --verify refs/heads/feature^{commit}"), fake.calls)
		})
	}
}

func TestPullMergeAsksForTheMethodAndBranchDeletion(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	form := &mergeForm{options: func(choices terminal.MergeFormChoices) terminal.MergeFormValues {
		return terminal.MergeFormValues{Method: "rebase", DeleteBranch: false}
	}}
	prompt := &scriptedPrompter{}
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.MergeForm, deps.Prompter = form, prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY, deps.Streams.OutTTY = true, true, true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project"}))
	require.Equal(t, []terminal.MergeFormChoices{{
		Header:  `Merge #3 "Add login" (feature → main) in alice/project`,
		Methods: []string{"merge", "rebase", "squash"}, Method: "squash", DeleteBranch: true, Branch: "feature",
	}}, form.asked)
	require.Empty(t, prompt.confirmations, "the form is the confirmation")
	require.Equal(t, "rebase", payload["Do"])
	require.Equal(t, false, payload["delete_branch_after_merge"])
	require.Equal(t, "✓ Rebased and merged pull request alice/project#3 (Add login)\n", diagnostics.String())

	diagnostics.Reset()
	payload = nil
	server2 := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server2.Close()
	deps.Getenv = serverEnv(server2.URL, map[string]string{"NO_COLOR": "1"})
	prompt.accept = true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--squash", "-d"}))
	require.Len(t, form.asked, 1, "flags answer the form")
	require.Equal(t, []string{`Merge #3 "Add login" (feature → main) in alice/project?`}, prompt.confirmations)
	require.Equal(t, []bool{true}, prompt.defaults)
	require.Equal(t, "✓ Squashed and merged pull request alice/project#3 (Add login)\n✓ Deleted remote branch feature\n", diagnostics.String())
}

// Gitea keeps the head branch after a merge when another open pull request uses it, since
// deleting it would close that one. gt must not delete it either: mergeServer fails on a DELETE.
func TestPullMergeKeepsAHeadBranchThatGiteaKept(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"repository default", nil},
		{"flag", []string{"--delete-branch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			pulls := mergeServer(t, openPull, &payload)
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" && payload != nil {
					_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","merged":true,"base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`))
					return
				}
				pulls.ServeHTTP(w, r)
			}))
			defer server.Close()
			deps, _, diagnostics := serverDeps(t, server.URL)
			deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "main"}}
			require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project", "--yes"}, tc.flags...)))
			require.Equal(t, true, payload["delete_branch_after_merge"])
			require.Equal(t, "! Kept remote branch feature: Gitea did not delete it (it is protected, the default branch, or used by another open pull request)\n", diagnostics.String())
		})
	}
}

func TestPullMergeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, pull string
		args       []string
		want       string
		code       int
	}{
		{"draft", `{"number":3,"title":"WIP: x","state":"open","draft":true}`, nil, "pull request alice/project#3 (WIP: x) is a draft; mark it ready with: gt pr ready 3", clierr.Failure},
		{"closed", `{"number":3,"title":"Old","state":"closed"}`, nil, "pull request alice/project#3 (Old) is closed; reopen it with: gt pr reopen 3", clierr.Failure},
		{"disallowed method", openPull, []string{"--method", "fast-forward-only"}, "fast-forward-only merges are disabled in alice/project; use one of: merge, rebase, squash", clierr.Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(mergeServer(t, tc.pull, &payload))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project", "--yes"}, tc.args...))
			require.EqualError(t, err, tc.want)
			require.Equal(t, tc.code, clierr.ExitCode(err))
			require.Nil(t, payload)
		})
	}
}

func TestPullMergeAlreadyMergedIsANoOp(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, `{"number":3,"title":"Done","state":"closed","merged":true}`, &payload))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project"}))
	require.Equal(t, "! Pull request alice/project#3 (Done) is already merged\n", diagnostics.String())
	require.Nil(t, payload)
}

func TestPullMergeMethodFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "merge", "3", "--squash", "--rebase"}, "choose one merge method: --merge, --squash, --rebase or --method"},
		{[]string{"pr", "merge", "3", "--squash", "-s", "merge"}, "choose one merge method"},
		{[]string{"pr", "merge", "3", "-s"}, "-s takes a method (merge, rebase, rebase-merge, squash or fast-forward-only); for gh-style use --squash"},
		{[]string{"pr", "merge", "3", "-s", "bogus"}, "--method must be merge, rebase, rebase-merge, squash, or fast-forward-only"},
	} {
		t.Run(tc.args[len(tc.args)-1], func(t *testing.T) {
			requireUsageBeforeRequests(t, tc.want, tc.args...)
		})
	}
}

func TestPullMergePicksAPullRequestForTheBranch(t *testing.T) {
	var payload map[string]any
	pulls := mergeServer(t, openPull, &payload)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/pulls" {
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"number":3,"title":"Add login","state":"open","base":{"ref":"main"},"head":{"ref":"feature"}},
				{"number":9,"title":"Other","state":"open","base":{"ref":"main"},"head":{"ref":"other"}}
			]`))
			return
		}
		pulls.ServeHTTP(w, r)
	}))
	defer server.Close()
	form := &mergeForm{
		pick: func(choices []terminal.MergePullChoice) int64 {
			require.Equal(t, []terminal.MergePullChoice{{Number: 3, Title: "Add login", Head: "feature"}, {Number: 9, Title: "Other", Head: "other"}}, choices)
			return 3
		},
		options: func(choices terminal.MergeFormChoices) terminal.MergeFormValues {
			return terminal.MergeFormValues{Method: choices.Method, DeleteBranch: choices.DeleteBranch}
		},
	}
	deps, _, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.MergeForm, deps.Prompter = form, &scriptedPrompter{}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "main"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge"}))
	require.Equal(t, "squash", payload["Do"])
}

func TestPullMergeRequiresNumberWithoutInteractive(t *testing.T) {
	deps, _, _ := testDeps(t)
	err := Execute(context.Background(), deps, []string{"pr", "merge", "-R", "alice/project", "--no-input"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull request number")
}
