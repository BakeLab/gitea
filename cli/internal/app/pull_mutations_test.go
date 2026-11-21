// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

func TestPullDiffStripsTerminalControlsOnlyOnATerminal(t *testing.T) {
	const diff = "+hello \x1b]0;PWNED\x07 \x1b[31mred\x1b[0m\r\n-old\tline\n"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/2.diff", r.URL.Path)
		_, _ = w.Write([]byte(diff))
	}))
	defer server.Close()
	for _, tc := range []struct {
		tty  bool
		args []string
		env  map[string]string
		want string
	}{
		{false, nil, nil, diff},
		// A diff piped into git apply stays plain even when color is forced for other commands.
		{false, nil, map[string]string{"CLICOLOR_FORCE": "1"}, diff},
		{true, nil, map[string]string{"NO_COLOR": "1"}, "+hello ]0;PWNED [31mred[0m\n-old\tline\n"},
		{true, nil, nil, "\x1b[32m+hello ]0;PWNED [31mred[0m\x1b[0m\n\x1b[31m-old\tline\x1b[0m\n"},
		{false, []string{"--color", "always"}, nil, "\x1b[32m+hello ]0;PWNED [31mred[0m\x1b[0m\n\x1b[31m-old\tline\x1b[0m\n"},
	} {
		deps, out, _ := serverDeps(t, server.URL)
		deps.Getenv = serverEnv(server.URL, tc.env)
		deps.Streams.OutTTY = tc.tty
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "diff", "2", "-R", "alice/project"}, tc.args...)))
		require.Equal(t, tc.want, out.String(), "%+v", tc)
	}
}

func TestPullDiffColorsHeadersAndHunks(t *testing.T) {
	text, err := diffText(strings.NewReader("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n context\n"), true)
	require.NoError(t, err)
	require.Equal(t, "\x1b[1mdiff --git a/x b/x\x1b[0m\n\x1b[1m--- a/x\x1b[0m\n\x1b[1m+++ b/x\x1b[0m\n\x1b[36m@@ -1 +1 @@\x1b[0m\n context\n", text)
}

func TestPullDiffNameOnly(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/2/files", r.URL.Path)
		writeFirstPage(w, r, `[{"filename":"one.txt"},{"filename":"docs/two.md"},{"filename":"tab\tname.txt"},{"filename":"esc\u001b[31mred.txt"}]`)
	}))
	defer server.Close()
	for _, tc := range []struct {
		tty  bool
		want string
	}{
		// Piped names stay exact, so that they can be passed on to other commands.
		{false, "one.txt\ndocs/two.md\ntab\tname.txt\nesc\x1b[31mred.txt\n"},
		{true, "one.txt\ndocs/two.md\ntab name.txt\nesc[31mred.txt\n"},
	} {
		deps, out, _ := serverDeps(t, server.URL)
		deps.Streams.OutTTY = tc.tty
		require.NoError(t, Execute(context.Background(), deps, []string{"pr", "diff", "2", "-R", "alice/project", "--name-only"}))
		require.Equal(t, tc.want, out.String(), "tty %v", tc.tty)
	}
}

func TestPullCloseCommentsFirstAndDeletesTheBranchOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		flags   []string
		deleted bool
	}{
		{nil, false},
		{[]string{"--delete-branch"}, true},
	} {
		var requests []string
		server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls":
				writeFirstPage(w, r, `[]`)
			case r.Method == http.MethodGet:
				_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"open","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`))
			case r.Method == http.MethodPatch:
				_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`))
			case r.Method == http.MethodPost:
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			default:
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "main"}}
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "close", "3", "-R", "alice/project", "-c", "Superseded"}, tc.flags...)))
		server.Close()
		comment := slices.Index(requests, "POST /api/v1/repos/alice/project/issues/3/comments")
		edit := slices.Index(requests, "PATCH /api/v1/repos/alice/project/pulls/3")
		require.True(t, comment >= 0 && comment < edit, requests)
		require.Equal(t, tc.deleted, slices.Contains(requests, "DELETE /api/v1/repos/alice/project/branches/feature"), requests)
	}
}

func TestPullCloseAndReopenRefuseMergedPullRequests(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","merged":true}`))
	}))
	defer server.Close()
	for verb, want := range map[string]string{"close": "cannot be closed", "reopen": "cannot be reopened"} {
		deps, _, _ := serverDeps(t, server.URL)
		require.ErrorContains(t, Execute(context.Background(), deps, []string{"pr", verb, "3", "-R", "alice/project"}), want)
	}
}

func TestPullReopenWarnsWhenAlreadyOpen(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"open"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "reopen", "3", "-R", "alice/project"}))
	require.Contains(t, diagnostics.String(), "is already open")
}

func TestPullMutationsReadReviewsOnlyForTheirJSONFields(t *testing.T) {
	const merged = `{"number":3,"title":"Add login","state":"closed","merged":true,"base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123"}}`
	var reads []string
	pull := ""
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project":
			_, _ = w.Write([]byte(mergeRepository))
		case "GET /api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case "GET /api/v1/repos/alice/project/pulls/3/reviews":
			reads = append(reads, r.URL.Path)
			writeFirstPage(w, r, `[{"id":1,"state":"APPROVED","official":true,"user":{"login":"carol"}}]`)
		case "GET /api/v1/repos/alice/project/pulls/3":
			reads = append(reads, r.URL.Path)
			_, _ = w.Write([]byte(pull))
		case "PATCH /api/v1/repos/alice/project/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"open"}`))
		case "POST /api/v1/repos/alice/project/pulls/3/merge":
			pull = merged
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		pull string
		args []string
	}{
		{`{"number":3,"title":"Add login","state":"closed"}`, []string{"pr", "reopen", "3"}},
		{openPull, []string{"pr", "edit", "3", "--title", "Add login"}},
		{`{"number":3,"title":"WIP: Add login","state":"open","draft":true}`, []string{"pr", "ready", "3"}},
		{openPull, []string{"pr", "merge", "3", "--yes", "--delete-branch=false"}},
		{merged, []string{"pr", "merge", "3", "--yes"}},
	} {
		pull = tc.pull
		deps, out, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, append(tc.args, "-R", "alice/project", "--json", "review_decision,reviews")), tc.args)
		var item struct {
			Decision string `json:"review_decision"`
			Reviews  []struct {
				Author string `json:"author"`
			} `json:"reviews"`
		}
		require.NoError(t, json.Unmarshal(out.Bytes(), &item), tc.args)
		require.Equal(t, "approved", item.Decision, tc.args)
		require.Len(t, item.Reviews, 1, tc.args)
		require.Equal(t, "carol", item.Reviews[0].Author, tc.args)
	}

	reads, pull = nil, `{"number":3,"title":"Add login","state":"closed"}`
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "reopen", "3", "-R", "alice/project", "--json", "number"}))
	require.Equal(t, []string{"/api/v1/repos/alice/project/pulls/3"}, reads)
}

func TestPullMergeSendsTitleAndMessage(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--method", "squash",
		"--title", "Squash title", "--message", "Squash body", "--delete-branch=false", "--yes",
	}))
	require.Equal(t, "squash", payload["Do"])
	require.Equal(t, "Squash title", payload["MergeTitleField"])
	require.Equal(t, "Squash body", payload["MergeMessageField"])
	require.Equal(t, false, payload["delete_branch_after_merge"])
	require.Equal(t, "abc123", payload["head_commit_id"])

	payload = nil
	server = newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps.Getenv = serverEnv(server.URL, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--method", "rebase", "--yes"}))
	require.Equal(t, "rebase", payload["Do"])
}

func TestPullMergeMessageFileAndEditor(t *testing.T) {
	var payload map[string]any
	deps, _, _ := testDeps(t)
	file := t.TempDir() + "/message.md"
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--message-file", file, "--yes"}))
	require.Equal(t, "from file", payload["MergeMessageField"])

	server = newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	deps.Editor, _ = newDraftEditor(t, "from editor")
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--editor", "--yes", "--no-input"}))
	require.Equal(t, "from editor", payload["MergeMessageField"])
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--message", "x", "--editor", "--yes",
	})))
}

func TestPullMergeReturnsTheMergedPullRequest(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--yes", "--json", "number,merged"}))
	require.JSONEq(t, `{"number":3,"merged":true}`, out.String())
}

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
// writePullRef answers a request for refs/pull/N/head, the head a pull request had when it was
// last open.
func writePullRef(w http.ResponseWriter, n int, sha string) {
	_, _ = fmt.Fprintf(w, `[{"ref":"refs/pull/%d/head","object":{"type":"commit","sha":%q}}]`, n, sha)
}

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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			writeFirstPage(w, r, `[]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/git/refs/pull/3/head":
			writePullRef(w, 3, "abc123")
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
			fake := &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feature"}}
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

func TestPullMergeTellsWhyItKeepsTheCheckedOutHeadBranch(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	fake := &fakeGit{
		results: map[string]string{"symbolic-ref --quiet HEAD": "feature", "rev-parse --verify refs/heads/feature^{commit}": "local1"},
		errs:    map[string]error{"merge-base --is-ancestor local1 abc123": &git.CommandError{ExitCode: 1}},
	}
	deps.Git = fake
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--yes", "--delete-branch"}))
	require.Equal(t, "! Kept local branch feature: it has commits that are not in the pull request\n", diagnostics.String())
	require.NotContains(t, fake.calls, "branch -D -- feature")
}

func TestPullMergeDryRunAsksNothing(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	form := &mergeForm{}
	deps, out, _ := serverDeps(t, server.URL)
	deps.MergeForm, deps.Prompter = form, &scriptedPrompter{}
	deps.Streams.InTTY, deps.Streams.ErrTTY, deps.Streams.OutTTY = true, true, true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--dry-run"}))
	require.Empty(t, form.asked)
	require.Nil(t, payload)
	require.Contains(t, out.String(), "Dry run: would send POST ")
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
			deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "main"}}
			require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project", "--yes"}, tc.flags...)))
			require.Equal(t, true, payload["delete_branch_after_merge"])
			require.Equal(t, "! Kept remote branch feature: Gitea did not delete it (it is protected, the default branch, or used by another open pull request)\n", diagnostics.String())
		})
	}
}

func TestPullMergeMarksAPullRequestAsManuallyMerged(t *testing.T) {
	for _, tc := range []struct {
		name, refusal, want string
		deleted             bool
	}{
		{"allowed", "", "", true},
		{"disallowed", `{"message":"merge strategy is not allowed or is invalid [repo_id: 1, strategy: manually-merged]"}`, "manual merges are disabled in alice/project; allow them with: gt repo edit --allow-manual-merge -R alice/project", false},
		{"commit not on base", `{"message":"commit not found in base branch"}`, "commit abc1234 is not on the base branch main of alice/project", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			deleted := false
			pulls := mergeServer(t, openPull, &payload)
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge" && tc.refusal != "":
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(tc.refusal))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" && payload != nil:
					_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","merged":true,"merge_commit_sha":"abc1234def","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`))
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/alice/project/branches/feature":
					deleted = true
					w.WriteHeader(http.StatusNoContent)
				default:
					pulls.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "main"}}
			err := Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "-s", "manually-merged", "--commit", "abc1234", "--delete-branch", "--yes"})
			if tc.want != "" {
				require.EqualError(t, err, tc.want)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, "manually-merged", payload["Do"])
			require.Equal(t, "abc1234", payload["MergeCommitID"])
			require.Equal(t, false, payload["delete_branch_after_merge"])
			require.Equal(t, tc.deleted, deleted)
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
		{"head branch deleted", `{"number":3,"title":"Add login","state":"open","base":{"ref":"main"},"head":{"ref":"refs/pull/3/head","label":"feature","sha":"abc123"}}`, nil, "pull request alice/project#3 (Add login) cannot be merged: its head branch feature was deleted; push it again to merge", clierr.Failure},
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

func TestPullMergeExplainsADivergedFastForward(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","allow_fast_forward_only_merge":true,"default_merge_style":"fast-forward-only"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
			_, _ = w.Write([]byte(openPull))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"Merge DivergingFastForwardOnly Error: exit status 128 - hint: Diverging branches can't be fast-forwarded\nfatal: Not possible to fast-forward, aborting."}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "-s", "fast-forward-only", "--delete-branch=false", "--yes"})
	require.EqualError(t, err, "cannot fast-forward pull request alice/project#3 (Add login): the base branch has commits the head lacks; rebase the head branch with: gt pr update-branch 3 --rebase, or choose another merge method")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
}

func TestPullUpdateBranch(t *testing.T) {
	var updates []string
	pull, allowRebase, compared := openPull, true, 0
	status, message := http.StatusOK, ""
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","allow_rebase_update":%t}`, allowRebase)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
			_, _ = w.Write([]byte(pull))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/compare/abc123...base1":
			_, _ = fmt.Fprintf(w, `{"total_commits":%d,"commits":[]}`, compared)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/update":
			updates = append(updates, r.URL.RawQuery)
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"message":%q}`, message)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "update-branch", "3", "-R", "alice/project", "--json", "number"}))
	require.JSONEq(t, `{"number":3}`, out.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "update-branch", "3", "-R", "alice/project", "--rebase"}))
	require.Equal(t, []string{"", "style=rebase"}, updates)

	// Gitea tells only site administrators that the head branch is up to date.
	status, updates = http.StatusInternalServerError, nil
	pull = `{"number":3,"title":"Add login","state":"open","merge_base":"base1","base":{"ref":"main","sha":"base1"},"head":{"ref":"feature","sha":"abc123"}}`
	for _, args := range [][]string{{"pr", "update-branch", "3", "-R", "alice/project"}, {"pr", "update-branch", "3", "-R", "alice/project", "--rebase"}} {
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, args))
		require.Equal(t, "! Pull request alice/project#3 (Add login) is already up to date with main\n", diagnostics.String())
	}
	require.Empty(t, updates)
	// Right after an update, Gitea still holds the merge base from before it.
	pull = `{"number":3,"title":"Add login","state":"open","merge_base":"old","base":{"ref":"main","sha":"base1"},"head":{"ref":"feature","sha":"abc123"}}`
	for _, missing := range []int{0, 2} {
		compared = missing
		diagnostics.Reset()
		err := Execute(context.Background(), deps, []string{"pr", "update-branch", "3", "-R", "alice/project"})
		if missing == 0 {
			require.NoError(t, err)
			require.Equal(t, "! Pull request alice/project#3 (Add login) is already up to date with main\n", diagnostics.String())
		} else {
			require.ErrorContains(t, err, "HTTP 500")
		}
	}
	pull, message = openPull, "HeadBranch of PR 3 is up to date"
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "update-branch", "3", "-R", "alice/project"}))
	require.Equal(t, "! Pull request alice/project#3 (Add login) is already up to date with main\n", diagnostics.String())

	status, message = http.StatusConflict, "rebase failed because of conflict"
	err := Execute(context.Background(), deps, []string{"pr", "update-branch", "3", "-R", "alice/project", "--rebase"})
	require.EqualError(t, err, "cannot update pull request alice/project#3 (Add login): its head branch feature conflicts with main; merge or rebase it locally and push it")

	allowRebase, updates = false, nil
	err = Execute(context.Background(), deps, []string{"pr", "update-branch", "3", "-R", "alice/project", "--rebase"})
	require.EqualError(t, err, "rebase updates are disabled in alice/project; update with a merge commit (without --rebase)")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.Empty(t, updates)
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

// Like pr close -d on a closed pull request, -d still cleans up after a merged one.
func TestPullMergeDeletesTheBranchOfAnAlreadyMergedPullRequest(t *testing.T) {
	deleted := false
	pulls := mergeServer(t, `{"number":3,"title":"Done","state":"closed","merged":true,"base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`, new(map[string]any))
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/alice/project/branches/feature" {
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		pulls.ServeHTTP(w, r)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "main"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "-d", "--yes"}))
	require.True(t, deleted)
}

// Gitea refuses to schedule a pull request twice with a 409 naming its internal ID.
func TestPullMergeAutoIsANoOpWhenAlreadyScheduled(t *testing.T) {
	pulls := mergeServer(t, openPull, new(map[string]any))
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"pull request is already scheduled to auto merge when checks succeed [pull_id: 33]"}`))
			return
		}
		pulls.ServeHTTP(w, r)
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--auto", "--delete-branch=false", "--yes", "--json", "number"}))
	require.Equal(t, "! Pull request alice/project#3 (Add login) is already scheduled to merge when its checks succeed\n", diagnostics.String())
	require.JSONEq(t, `{"number":3}`, out.String())
}

func TestPullMergeMethodFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "merge", "3", "--squash", "--rebase"}, "pr merge requires one merge method: --merge, --squash, --rebase or --method"},
		{[]string{"pr", "merge", "3", "--squash", "-s", "merge"}, "pr merge requires one merge method"},
		{[]string{"pr", "merge", "3", "-s"}, "-s takes a method (merge, rebase, rebase-merge, squash, fast-forward-only or manually-merged); for gh-style use --squash"},
		{[]string{"pr", "merge", "3", "-s", "bogus"}, "--method must be merge, rebase, rebase-merge, squash, fast-forward-only, or manually-merged"},
		{[]string{"pr", "merge", "3", "-s", "manually-merged"}, "--method manually-merged requires --commit"},
		{[]string{"pr", "merge", "3", "--squash", "--commit", "abc1234"}, "--commit requires --method manually-merged"},
		{[]string{"pr", "merge", "3", "-s", "manually-merged", "--commit", "main"}, `--commit requires a commit SHA of 7 to 64 hexadecimal digits, got "main"`},
		{[]string{"pr", "merge", "3", "-s", "manually-merged", "--commit", "abc1234", "--auto"}, "--method manually-merged cannot be combined with --auto"},
		{[]string{"pr", "merge", "3", "-s", "manually-merged", "--commit", "abc1234", "-t", "x"}, "--method manually-merged cannot be combined with --title"},
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
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "main"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge"}))
	require.Equal(t, "squash", payload["Do"])
}

func TestPullMergeRequiresNumberWithoutInteractive(t *testing.T) {
	deps, _, _ := testDeps(t)
	err := Execute(context.Background(), deps, []string{"pr", "merge", "-R", "alice/project", "--no-input"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull request number")
}

func TestPullMergeReportsNoBranchDeletionForAGitPullRequests(t *testing.T) {
	agit := `{"number":3,"title":"Add login","state":"open","base":{"ref":"main"},"head":{"ref":"refs/pull/3/head","label":"","sha":"abc123"}}`
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(mergeRepository))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(agit))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--yes", "--delete-branch"}))
	require.NotContains(t, diagnostics.String(), "emote branch")

	// The form does not offer to delete a branch the pull request does not have.
	form := &mergeForm{options: func(choices terminal.MergeFormChoices) terminal.MergeFormValues {
		return terminal.MergeFormValues{Method: choices.Method}
	}}
	deps.MergeForm, deps.Prompter = form, &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--squash"}))
	require.Empty(t, form.asked)
}

func TestPullCloseReportsTheDeletedHeadBranch(t *testing.T) {
	pull := `{"number":3,"title":"Add login","state":"STATE","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`
	for _, tc := range []struct {
		name, open, ref, json, warning string
		deletes                        int
	}{
		{"deleted", `[{"number":3,"state":"open","head":{"ref":"feature","repo":{"full_name":"alice/project"}}},{"number":4,"state":"open","head":{"ref":"feature","repo":{"full_name":"bob/project"}}}]`, "abc123", `{"state":"closed","head_remote_deleted":true}`, "", 1},
		// Deleting the branch would close the other pull request from it.
		{"used by another pull request", `[{"number":5,"title":"Backport","state":"open","head":{"ref":"feature","repo":{"full_name":"alice/project"}}}]`, "abc123", `{"state":"closed","head_remote_deleted":false}`, "! Kept remote and local branch feature: it is the head of open pull request alice/project#5 (Backport)\n", 0},
		// Gitea reports the moved branch as the head of the closed pull request.
		{"moved on", `[]`, "def456", `{"state":"closed","head_remote_deleted":false}`, "! Kept remote and local branch feature: it has commits that are not in pull request #3\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deletes := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
					_, _ = w.Write([]byte(strings.Replace(pull, "STATE", "open", 1)))
				case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
					_, _ = w.Write([]byte(strings.Replace(pull, "STATE", "closed", 1)))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls":
					writeFirstPage(w, r, tc.open)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/git/refs/pull/3/head":
					writePullRef(w, 3, tc.ref)
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/alice/project/branches/feature":
					deletes++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "close", "3", "-R", "alice/project", "--delete-branch", "--json", "state,head_remote_deleted"}))
			require.Equal(t, tc.deletes, deletes)
			require.JSONEq(t, tc.json, out.String())
			if tc.warning != "" {
				require.Equal(t, tc.warning, diagnostics.String())
			}
		})
	}
}

func TestPullDeleteBranchDeletesAForkBranchOnlyWhereYouCanPush(t *testing.T) {
	for _, push := range []bool{true, false} {
		t.Run(strconv.FormatBool(push), func(t *testing.T) {
			pull := fmt.Sprintf(`{"number":3,"title":"Add login","state":"STATE","base":{"ref":"main","repo":{"full_name":"alice/project"}},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"bob/project","permissions":{"push":%t}}}}`, push)
			deletes := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
					_, _ = w.Write([]byte(strings.Replace(pull, "STATE", "open", 1)))
				case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
					_, _ = w.Write([]byte(strings.Replace(pull, "STATE", "closed", 1)))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls":
					writeFirstPage(w, r, `[]`)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/git/refs/pull/3/head":
					writePullRef(w, 3, "abc123")
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/bob/project/branches/feature":
					deletes++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "close", "3", "-R", "alice/project", "-d"}))
			if push {
				require.Equal(t, 1, deletes)
			} else {
				require.Zero(t, deletes)
				require.Contains(t, diagnostics.String(), "Kept remote branch feature: you cannot push to bob/project")
			}
			out.Reset()
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "close", "--help"}))
			require.Contains(t, out.String(), "also in a fork you can push to")
			out.Reset()
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "--help"}))
			require.Contains(t, out.String(), "deletes the head branch of a pull request from a fork too, when you can push")
		})
	}
}
