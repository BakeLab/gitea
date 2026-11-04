// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// dryRunCases lists every command that takes --dry-run, which TestDryRunFlagsHaveCases enforces, and
// the write each one previews.
var dryRunCases = []struct {
	args []string
	want string
}{
	{[]string{"issue", "close", "5"}, "PATCH"},
	{[]string{"issue", "reopen", "6"}, "PATCH"},
	{[]string{"pr", "close", "7"}, "PATCH"},
	{[]string{"pr", "reopen", "8"}, "PATCH"},
	{[]string{"pr", "merge", "7", "--squash", "--delete-branch"}, "POST"},
	{[]string{"release", "delete", "v1", "--delete-tag"}, "DELETE"},
	{[]string{"label", "delete", "bug"}, "DELETE"},
	{[]string{"branch", "delete", "old"}, "DELETE"},
	{[]string{"repo", "delete", "alice/project"}, "DELETE"},
}

// readOnlyGit is what a dry run may ask Git.
var readOnlyGit = []string{"symbolic-ref ", "rev-parse ", "config --get", "remote get-url ", "remote -v", "for-each-ref ", "merge-base "}

// dryRun runs args with --dry-run against server. It fails the test when a command reached the
// clone other than to read it.
func dryRun(t *testing.T, server *httptest.Server, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	deps, out, diagnostics := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Streams.OutTTY = true
	fake := &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "fix", "rev-parse --verify refs/heads/fix^{commit}": "abc1234"}}
	deps.Git = fake
	err = Execute(context.Background(), deps, append(args, "--dry-run", "--no-input"))
	for _, call := range fake.calls {
		require.True(t, slices.ContainsFunc(readOnlyGit, func(prefix string) bool { return strings.HasPrefix(call, prefix) }), "a dry run ran git %s", call)
	}
	return out.String(), diagnostics.String(), err
}

// dryRunServer answers reads and fails the test on any write except an OAuth refresh.
func dryRunServer(t *testing.T) *httptest.Server {
	t.Helper()
	pull := func(number int, state, branch string, push bool) string {
		return fmt.Sprintf(`{"number":%d,"title":"Fix","state":%q,"mergeable":true,"head":{"ref":%q,"label":%q,"sha":"abc1234","repo":{"full_name":"alice/project","permissions":{"push":%t}}},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`, number, state, branch, branch, push)
	}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login/oauth/access_token" {
			_, _ = w.Write([]byte(`{"access_token":"fresh","refresh_token":"rotated","expires_in":3600,"token_type":"Bearer"}`))
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("a dry run sent %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/issues/5"):
			_, _ = w.Write([]byte(`{"number":5,"title":"Bug","state":"open","repository":{"full_name":"alice/project"}}`))
		case strings.HasSuffix(path, "/issues/404"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case strings.HasSuffix(path, "/issues/6"):
			_, _ = w.Write([]byte(`{"number":6,"title":"Bug","state":"closed","repository":{"full_name":"alice/project"}}`))
		case strings.HasSuffix(path, "/pulls/7"):
			_, _ = w.Write([]byte(pull(7, "open", "fix", false)))
		case strings.HasSuffix(path, "/pulls/8"):
			_, _ = w.Write([]byte(pull(8, "closed", "old", true)))
		case strings.HasSuffix(path, "/pulls/9"):
			_, _ = w.Write([]byte(pull(9, "open", "fix", true)))
		case strings.HasSuffix(path, "/releases/tags/v1"):
			_, _ = w.Write([]byte(`{"id":3,"tag_name":"v1","name":"v1"}`))
		case strings.HasSuffix(path, "/labels") && r.URL.Query().Get("page") > "1":
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(path, "/labels"):
			_, _ = w.Write([]byte(`[{"id":9,"name":"bug","color":"ff0000"}]`))
		case strings.HasSuffix(path, "/branches/old"):
			_, _ = w.Write([]byte(`{"name":"old"}`))
		case strings.HasSuffix(path, "/branches/fix"):
			_, _ = w.Write([]byte(`{"name":"fix","commit":{"id":"abc1234"}}`))
		case strings.HasSuffix(path, "/repos/alice/project"):
			_, _ = w.Write([]byte(`{"id":1,"full_name":"alice/project","name":"project","owner":{"login":"alice"},"default_branch":"main","allow_merge_commits":true,"allow_squash_merge":true,"allow_rebase":true,"allow_manual_merge":true,"has_issues":true}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestDryRunCommands runs every command that takes --dry-run: reads go through, no write and no
// change to the clone does, and stdout holds only the report.
func TestDryRunCommands(t *testing.T) {
	server := dryRunServer(t)
	for _, tc := range dryRunCases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, _, err := dryRun(t, server, tc.args...)
			require.NoError(t, err)
			require.Contains(t, out, "Dry run: would send "+tc.want+" ")
			for line := range strings.Lines(out) {
				require.True(t, strings.HasPrefix(line, "Dry run: would send "), line)
			}
		})
	}
}

// TestDryRunRunsToTheEnd covers the commands that write more than once, or write depending on what
// an earlier write answered.
func TestDryRunRunsToTheEnd(t *testing.T) {
	server := dryRunServer(t)
	repo := server.URL + "/api/v1/repos/alice/project"

	t.Run("a comment and the state change", func(t *testing.T) {
		out, _, err := dryRun(t, server, "issue", "close", "5", "--comment", "Done")
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.Len(t, lines, 2)
		require.Contains(t, lines[0], "POST "+repo+"/issues/5/comments")
		require.Contains(t, lines[1], "PATCH "+repo+"/issues/5")
	})
	t.Run("the state change and the head branch", func(t *testing.T) {
		out, stderr, err := dryRun(t, server, "pr", "close", "9", "--delete-branch")
		require.NoError(t, err)
		require.Contains(t, out, "PATCH "+repo+"/pulls/9")
		require.Contains(t, out, "DELETE "+repo+"/branches/fix")
		require.Empty(t, stderr)
	})
	t.Run("a merge that deletes the head branch", func(t *testing.T) {
		out, stderr, err := dryRun(t, server, "pr", "merge", "9", "--squash", "--delete-branch")
		require.NoError(t, err)
		require.Contains(t, out, "POST "+repo+"/pulls/9/merge")
		require.Contains(t, out, `"delete_branch_after_merge":true`)
		require.Empty(t, stderr, "no claim that Gitea kept the branch or that the merge happened")
	})
	t.Run("a manual merge deletes the head branch itself", func(t *testing.T) {
		out, _, err := dryRun(t, server, "pr", "merge", "9", "--method", "manually-merged", "--commit", "4b825dc", "--delete-branch")
		require.NoError(t, err)
		require.Contains(t, out, "POST "+repo+"/pulls/9/merge")
		require.Contains(t, out, "DELETE "+repo+"/branches/fix")
	})
	t.Run("an automatic merge", func(t *testing.T) {
		out, stderr, err := dryRun(t, server, "pr", "merge", "9", "--squash", "--auto")
		require.NoError(t, err)
		require.Contains(t, out, `"merge_when_checks_succeed":true`)
		require.Empty(t, stderr)
	})
	t.Run("a batch reports what it would not write", func(t *testing.T) {
		out, stderr, err := dryRun(t, server, "issue", "close", "5", "6")
		require.NoError(t, err)
		require.Equal(t, 1, strings.Count(out, "\n"))
		require.Contains(t, stderr, "is already closed")
	})
	t.Run("failure of another item", func(t *testing.T) {
		out, _, err := dryRun(t, server, "issue", "close", "5", "404")
		require.ErrorContains(t, err, "issue 404")
		require.Contains(t, out, "Dry run: would send PATCH ")
	})
	t.Run("json", func(t *testing.T) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, []string{"issue", "close", "5", "--dry-run", "--json", "number", "--jq", ".requests[0] | [.method, .body.state]"}))
		require.Equal(t, "[\"PATCH\",\"closed\"]\n", out.String())
	})
	t.Run("json without a resource", func(t *testing.T) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, []string{"branch", "delete", "old", "--dry-run", "--json"}))
		require.JSONEq(t, `{"dry_run":true,"requests":[{"method":"DELETE","url":"`+repo+`/branches/old"}]}`, out.String())
	})
	t.Run("json without a write", func(t *testing.T) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, []string{"issue", "close", "6", "--dry-run", "--json"}))
		require.JSONEq(t, `{"dry_run":true,"requests":[]}`, out.String())
	})
	t.Run("oauth refresh", func(t *testing.T) {
		deps, out, _ := testDeps(t)
		deps.Getenv = envFunc(map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, testManager(t, deps).Register(server.URL, "alice", "file", "oauth", auth.HostOptions{OAuthClientID: "client"}, auth.Credential{AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour)}))
		require.NoError(t, Execute(context.Background(), deps, []string{"label", "delete", "bug", "--dry-run", "--host", server.URL}))
		require.Equal(t, "Dry run: would send DELETE "+repo+"/labels/9\n", out.String())
	})
}

// TestDryRunFlagsHaveCases makes a new command with --dry-run add itself to dryRunCases, which
// proves it sends no write and leaves the clone alone. upgrade and extension upgrade have their own.
func TestDryRunFlagsHaveCases(t *testing.T) {
	deps, _, _ := testDeps(t)
	var got []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Flags().Lookup("dry-run") != nil {
			got = append(got, cmd.CommandPath())
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(NewRoot(deps))
	want := []string{"gt extension upgrade", "gt upgrade"}
	for _, tc := range dryRunCases {
		want = append(want, "gt "+tc.args[0]+" "+tc.args[1])
	}
	require.ElementsMatch(t, want, got)
}
