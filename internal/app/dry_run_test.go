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

// dryRunCases and dryRunWriteCases list every command that takes --dry-run, which
// TestDryRunFlagsHaveCases enforces, and the write each one previews.
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
		if status, body, ok := dryRunFixture(path, r.URL.Query().Get("page")); ok {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
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
			_, _ = w.Write([]byte(`{"id":3,"tag_name":"v1","name":"v1","assets":[{"id":1,"name":"a.txt"}]}`))
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
			_, _ = w.Write([]byte(dryRunGenericResponse(path, r.URL.Query().Get("page"))))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestDryRunCommands runs every command that takes --dry-run: reads go through, no write and no
// change to the clone does, and stdout holds only the report.
func TestDryRunCommands(t *testing.T) {
	server := dryRunServer(t)
	for _, tc := range append(slices.Clone(dryRunCases), dryRunWriteCases...) {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, _, err := dryRun(t, server, expandDryRunArgs(t, tc.args)...)
			require.NoError(t, err, "%v", tc.args)
			require.Contains(t, out, "Dry run: would send "+tc.want)
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
	t.Run("edits whose read-back has nothing to find", func(t *testing.T) {
		for _, args := range [][]string{
			{"pr", "edit", "7", "--milestone", "Bug"},
			{"pr", "edit", "7", "--add-assignee", "bob"},
			{"pr", "edit", "7", "--deadline", "2030-01-01"},
			{"pr", "edit", "7", "--add-label", "repo:bug"},
			{"issue", "edit", "5", "--add-label", "repo:bug"},
			{"issue", "edit", "5", "--set-labels", "repo:bug"},
			{"notification", "mark-read", "--all"},
		} {
			out, _, err := dryRun(t, server, args...)
			require.NoError(t, err, "%v", args)
			require.Contains(t, out, "Dry run: would send", "%v", args)
		}
	})
	t.Run("a start reports no stopwatch as stopped", func(t *testing.T) {
		out, stderr, err := dryRun(t, server, "time", "start", "7")
		require.NoError(t, err)
		require.Contains(t, out, "POST "+repo+"/issues/7/stopwatch/start")
		require.Empty(t, stderr)
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

// TestDryRunFlagsHaveCases makes a new command with --dry-run add itself to dryRunWriteCases, which
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
	root := NewRoot(deps)
	walk(root)
	want := []string{"gt extension upgrade", "gt upgrade"}
	for _, tc := range append(slices.Clone(dryRunCases), dryRunWriteCases...) {
		cmd, _, err := root.Find(tc.args)
		require.NoError(t, err)
		want = append(want, cmd.CommandPath())
	}
	require.ElementsMatch(t, want, got)
}

// dryRunReadVerbs are the last words of commands that only read.
var dryRunReadVerbs = []string{"list", "view", "get", "status", "logs", "diff", "download", "jobs", "timeline", "checks", "revisions", "comments", "watch", "export", "versions", "show", "activity"}

// dryRunExempt are the commands, with their subcommands, that have no --dry-run although they are not
// read-only, and why.
var dryRunExempt = []struct{ path, reason string }{
	{"gt auth", "changes the saved credentials, not Gitea"},
	{"gt config", "changes gt's settings, not Gitea"},
	{"gt alias", "changes gt's settings, not Gitea"},
	{"gt extension", "installs, runs and removes programs on this computer"},
	{"gt completion", "writes a script to stdout"},
	{"gt pr create", "pushes the head branch from the clone"},
	{"gt pr checkout", "changes the clone"},
	{"gt pr clean", "deletes branches of the clone"},
	{"gt pr backport", "cherry-picks in the clone and pushes"},
	{"gt repo clone", "creates a clone"},
	{"gt repo fork", "can clone and add remotes to the clone"},
	{"gt repo sync", "updates branches of the clone"},
	{"gt repo set-default", "changes the clone's configuration"},
	{"gt browse", "opens a browser"},
	{"gt doctor", "reads"},
	{"gt gpg-key token", "reads"},
	{"gt version", "reads"},
	{"gt schema", "reads"},
	{"gt search", "reads"},
}

// TestDryRunCoversEveryWrite makes a new command either take --dry-run or say why not, so that none
// of the writes of gt is left out by accident: add its path to dryRunCommands, with a case in
// dryRunWriteCases, when it only changes things on Gitea.
func TestDryRunCoversEveryWrite(t *testing.T) {
	deps, _, _ := testDeps(t)
	used := map[string]bool{}
	walkCommands(NewRoot(deps), func(cmd *cobra.Command) {
		if !cmd.HasParent() || !cmd.IsAvailableCommand() {
			return
		}
		if cmd.HasAvailableSubCommands() && !strings.Contains(cmd.Use, " ") {
			return // only a group; one that takes an operand, like pr review, writes itself
		}
		path := cmd.CommandPath()
		if cmd.Flags().Lookup("dry-run") != nil || slices.Contains(dryRunReadVerbs, cmd.Name()) {
			return
		}
		for _, exempt := range dryRunExempt {
			if path == exempt.path || strings.HasPrefix(path, exempt.path+" ") {
				used[exempt.path] = true
				return
			}
		}
		t.Errorf("%s has no --dry-run: add it to dryRunCommands, or to dryRunExempt with the reason", path)
	})
	for _, exempt := range dryRunExempt {
		require.True(t, used[exempt.path], "%s is exempt but no command needs it", exempt.path)
	}
}
