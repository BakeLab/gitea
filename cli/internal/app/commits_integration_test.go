// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

func TestGiteaCommitReadsAndDiffFilters(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-commit-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName)...) }
	write := func(branch, path, content, message string) {
		deps.Streams.In = strings.NewReader(fmt.Sprintf(`{"branch":%q,"content":%q,"message":%q}`, branch, content, message))
		execute("api", "repos/"+repo.FullName+"/contents/"+path, "--input", "-")
	}
	// paged runs gt where more pages follow, which it says on stderr.
	paged := func(args ...string) []byte {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", repo.FullName)))
		return append([]byte(nil), out.Bytes()...)
	}
	head := func(ref string) string {
		return strings.TrimSpace(string(execute("api", "repos/"+repo.FullName+"/branches/"+ref, "--jq", ".commit.id")))
	}
	// base64 of "package a\n", "# docs\n" and "package a\nfunc TestA() {}\n".
	write(repo.DefaultBranch, "internal/a.go", "cGFja2FnZSBhCg==", "Add a")
	write(repo.DefaultBranch, "docs/b.md", "IyBkb2NzCg==", "Add docs")
	scoped("branch", "create", "feature")
	write("feature", "internal/a_test.go", "cGFja2FnZSBhCmZ1bmMgVGVzdEEoKSB7fQo=", "Add the test of a")
	write("feature", "logo.bin", "AAECAwQ=", "Add a binary file")
	mainHead, featureHead := head(repo.DefaultBranch), head("feature")

	t.Run("list", func(t *testing.T) {
		var commits []services.Commit
		require.NoError(t, json.Unmarshal(scoped("commit", "list", "--json", "sha,message,author_login"), &commits))
		require.Len(t, commits, 3, "the initial commit and two more")
		require.Equal(t, mainHead, commits[0].SHA, "newest first")
		require.Equal(t, "Add docs", strings.TrimSpace(commits[0].Message))
		require.Equal(t, "integration", commits[0].AuthorLogin)
		require.Equal(t, mainHead+"\tAdd docs\tintegration", strings.Join(strings.Split(strings.TrimSpace(string(paged("commit", "list", "-L", "1"))), "\t")[:3], "\t"), "piped, the full SHA comes first")

		require.NoError(t, json.Unmarshal(scoped("commit", "list", "--path", "internal/", "--json", "message"), &commits))
		require.Len(t, commits, 1)
		require.Equal(t, "Add a", strings.TrimSpace(commits[0].Message))

		require.NoError(t, json.Unmarshal(scoped("commit", "list", "feature", "--not", repo.DefaultBranch, "--json", "sha,additions,deletions,files"), &commits))
		require.Len(t, commits, 2, "what feature has and main has not")
		require.Equal(t, featureHead, commits[0].SHA)
		require.Equal(t, []string{"logo.bin"}, commits[0].Files, "files come when the output names them")
		require.Positive(t, commits[0].Additions+commits[1].Additions)

		require.NoError(t, json.Unmarshal(paged("commit", "list", "-L", "1", "--page", "2", "--json", "sha"), &commits))
		require.Len(t, commits, 1)
		require.NotEqual(t, mainHead, commits[0].SHA)
		require.ErrorContains(t, Execute(context.Background(), deps, []string{"commit", "list", "no-such-ref", "-R", repo.FullName}), "no-such-ref")
	})

	t.Run("view", func(t *testing.T) {
		var commit services.Commit
		require.NoError(t, json.Unmarshal(scoped("commit", "view", mainHead, "--json"), &commit))
		require.Equal(t, mainHead, commit.SHA)
		require.Equal(t, []string{"docs/b.md"}, commit.Files)
		require.Equal(t, 1, commit.Additions)
		require.Len(t, commit.Parents, 1)
		require.NotEmpty(t, commit.CommittedAt)
		require.Equal(t, commit.SHA, strings.TrimSpace(string(scoped("commit", "view", repo.DefaultBranch, "--json", "sha", "--jq", ".sha"))), "a branch names the commit it points to")
		text := string(scoped("commit", "view", mainHead))
		require.Contains(t, text, "title:\tAdd docs")
		require.Contains(t, text, "files:\tdocs/b.md")
		require.ErrorContains(t, Execute(context.Background(), deps, []string{"commit", "view", "0123456789abcdef0123456789abcdef01234567", "-R", repo.FullName}), "not found")
	})

	t.Run("diff", func(t *testing.T) {
		full := string(scoped("commit", "diff", featureHead))
		require.Contains(t, full, "diff --git a/logo.bin b/logo.bin")
		compare := string(scoped("commit", "compare", repo.DefaultBranch+"...feature", "--json", "total_commits"))
		require.JSONEq(t, `{"total_commits":2}`, compare)

		// internal/a_test.go and logo.bin are in different commits of feature; use the one with the test.
		var commits []services.Commit
		require.NoError(t, json.Unmarshal(scoped("commit", "list", "feature", "--path", "internal/a_test.go", "--json", "sha"), &commits))
		testCommit := commits[0].SHA
		require.Equal(t, "internal/a_test.go\n", string(scoped("commit", "diff", testCommit, "--name-only")))
		require.Equal(t, "A\tinternal/a_test.go\n", string(scoped("commit", "diff", testCommit, "--name-status")))
		require.Contains(t, string(scoped("commit", "diff", testCommit, "--stat")), "1 file changed, 2 insertions(+), 0 deletions(-)")
		require.Empty(t, string(paged("commit", "diff", testCommit, "--exclude", "*_test.go", "--name-only")), "everything is excluded")
		require.Contains(t, diagnostics.String(), "no changed file matches --path and --exclude", "and gt says so")
		narrowed := string(scoped("commit", "diff", testCommit, "--path", "internal/", "--exclude", "docs/"))
		require.True(t, strings.HasPrefix(narrowed, "diff --git a/internal/a_test.go"), "what is left is still a diff")
		require.Equal(t, string(scoped("commit", "diff", testCommit)), narrowed, "a filter that keeps every file changes nothing")
		require.Contains(t, string(scoped("commit", "diff", testCommit, "--patch")), "Subject: [PATCH] Add the test of a")
		require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"commit", "diff", testCommit, "--stat", "--name-only", "-R", repo.FullName})))

		scoped("tag", "create", "v1.2.0")
		scoped("branch", "create", "release/1.2")
		for _, ref := range []string{"v1.2.0", "release/1.2"} {
			require.Equal(t, string(scoped("commit", "diff", mainHead)), string(scoped("commit", "diff", ref)), "a ref with a dot or a slash")
		}

		binary := string(scoped("commit", "diff", featureHead, "--stat"))
		require.Contains(t, binary, "logo.bin")
		require.Contains(t, binary, "Bin")
	})

	t.Run("status", func(t *testing.T) {
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"commit", "status", mainHead, "-R", repo.FullName}))
		require.Contains(t, diagnostics.String(), "no statuses reported")
		report := func(state string) {
			execute("api", "repos/"+repo.FullName+"/statuses/"+mainHead, "-f", "state="+state, "-f", "context=ci/build", "-f", "description=A check")
		}
		run := func() error {
			out.Reset()
			return Execute(context.Background(), deps, []string{"commit", "status", mainHead, "-R", repo.FullName})
		}
		report("pending")
		require.Equal(t, clierr.Pending, clierr.ExitCode(run()), "a status that is still running")
		report("success")
		require.NoError(t, run())
		require.Contains(t, out.String(), "ci/build\tsuccess\tA check")
		report("failure")
		require.Equal(t, 1, clierr.ExitCode(run()), "a failed status")
		var checks []services.Check
		out.Reset()
		_ = Execute(context.Background(), deps, []string{"commit", "status", repo.DefaultBranch, "-R", repo.FullName, "--json"})
		require.NoError(t, json.Unmarshal(out.Bytes(), &checks))
		require.Len(t, checks, 1, "the latest status of the context, by the name of a branch")
		require.Equal(t, "ci/build", checks[0].Name)
		require.Equal(t, "failure", checks[0].State)
		require.Equal(t, mainHead, checks[0].SHA)
	})

	t.Run("pull request of a commit and diff of a pull request", func(t *testing.T) {
		var pull services.Pull
		require.NoError(t, json.Unmarshal(scoped("pr", "create", "--title", "The feature", "--body", "b", "--head", "feature", "--base", repo.DefaultBranch, "--json"), &pull))
		n := strconv.FormatInt(pull.Number, 10)
		require.Equal(t, "internal/a_test.go\nlogo.bin\n", string(scoped("pr", "diff", n, "--name-only")))
		require.Equal(t, "A\tinternal/a_test.go\nA\tlogo.bin\n", string(scoped("pr", "diff", n, "--name-status")))
		require.Equal(t, "internal/a_test.go\n", string(scoped("pr", "diff", n, "--path", "internal/", "--name-only")))
		require.Equal(t, "logo.bin\n", string(scoped("pr", "diff", n, "--exclude", "*_test.go", "--name-only")))
		require.Contains(t, string(scoped("pr", "diff", n, "--stat")), "2 files changed")

		scoped("pr", "merge", n, "--squash", "--yes")
		merged := head(repo.DefaultBranch)
		require.Equal(t, n, strings.TrimSpace(string(scoped("commit", "pr", merged, "--json", "number", "--jq", ".number"))), "the pull request that made the commit")
		require.ErrorContains(t, Execute(context.Background(), deps, []string{"commit", "pr", mainHead, "-R", repo.FullName}), "pull request")
	})
}
