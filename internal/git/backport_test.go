// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// backportFixture is a clone of a bare remote laid out like Gitea's: main, a release branch cut
// before pull request #7 and the pull request's head at refs/pull/7/head. The clone is on branch
// work, so that switching back is visible.
type backportFixture struct {
	t                    *testing.T
	runner               System
	work, pusher         string
	mergeBase, merge     string
	commits, pullCommits []string
	releaseTip, original string
	clock                int
}

// newBackportFixture merges pull request #7, which has two commits, into main in the given style.
func newBackportFixture(t *testing.T, style string) *backportFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(root, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=gt", "GIT_AUTHOR_EMAIL=gt@example.invalid", "GIT_COMMITTER_NAME=gt", "GIT_COMMITTER_EMAIL=gt@example.invalid")
	f := &backportFixture{t: t, runner: System{Env: env}, work: filepath.Join(root, "work"), pusher: filepath.Join(root, "pusher")}
	remote := filepath.Join(root, "remote.git")
	f.git(root, "init", "--bare", "-b", "main", remote)
	f.git(root, "clone", remote, f.pusher)
	f.commit("base.txt", "base\n", "base")
	f.git(f.pusher, "push", "origin", "HEAD:main", "HEAD:release/v1")
	f.releaseTip = f.git(f.pusher, "rev-parse", "HEAD")
	f.mergeBase = f.releaseTip
	f.git(f.pusher, "switch", "-c", "feature")
	f.pullCommits = []string{f.commit("one.txt", "one\n", "add one"), f.commit("two.txt", "two\n", "add two")}
	f.git(f.pusher, "push", "origin", "HEAD:refs/pull/7/head")
	f.git(f.pusher, "switch", "main")
	switch style {
	case "merge":
		f.git(f.pusher, "merge", "--no-ff", "-m", "Merge pull request #7", "feature")
		f.commits = f.pullCommits
	case "squash":
		f.git(f.pusher, "merge", "--squash", "feature")
		f.git(f.pusher, "commit", "-m", "Add one and two (#7)", "--date", f.tick())
		f.commits = []string{f.git(f.pusher, "rev-parse", "HEAD")}
	case "rebase":
		f.commit("main.txt", "main\n", "advance main")
		f.git(f.pusher, "switch", "feature")
		f.git(f.pusher, "rebase", "--quiet", "main")
		f.git(f.pusher, "switch", "main")
		f.git(f.pusher, "merge", "--ff-only", "feature")
		f.commits = strings.Split(f.git(f.pusher, "rev-list", "--reverse", "HEAD~2..HEAD"), "\n")
	case "fast-forward":
		f.git(f.pusher, "merge", "--ff-only", "feature")
		f.commits = f.pullCommits
	default:
		t.Fatalf("unknown merge style %s", style)
	}
	f.merge = f.git(f.pusher, "rev-parse", "HEAD")
	f.git(f.pusher, "push", "origin", "HEAD:main")
	f.git(root, "clone", remote, f.work)
	f.git(f.work, "switch", "-c", "work")
	f.original = f.git(f.work, "rev-parse", "HEAD")
	return f
}

func (f *backportFixture) git(dir string, args ...string) string {
	f.t.Helper()
	output, err := f.runner.Output(context.Background(), dir, false, args...)
	require.NoError(f.t, err, args)
	return output
}

// tick returns a new author date, so that commits differ in time like they do in a real history.
func (f *backportFixture) tick() string {
	f.clock++
	return strings.Replace("2026-01-01T00:00:SSZ", "SS", string(rune('0'+f.clock/10))+string(rune('0'+f.clock%10)), 1)
}

func (f *backportFixture) commit(name, content, message string) string {
	f.t.Helper()
	require.NoError(f.t, os.WriteFile(filepath.Join(f.pusher, name), []byte(content), 0o600))
	f.git(f.pusher, "add", name)
	f.git(f.pusher, "commit", "--quiet", "-m", message, "--date", f.tick())
	return f.git(f.pusher, "rev-parse", "HEAD")
}

func (f *backportFixture) options() BackportOptions {
	return BackportOptions{
		Directory: f.work, Remote: "origin", Number: 7, Base: "main", MergeCommit: f.merge, MergeBase: f.mergeBase,
		Target: "release/v1", Branch: "backport-7-release/v1",
	}
}

func (f *backportFixture) backport(opts BackportOptions) (BackportResult, error) {
	return Backport(context.Background(), f.runner, opts)
}

func (f *backportFixture) requireUntouched() {
	f.t.Helper()
	require.Equal(f.t, "work", f.git(f.work, "branch", "--show-current"))
	require.Equal(f.t, f.original, f.git(f.work, "rev-parse", "HEAD"))
	require.Empty(f.t, f.git(f.work, "status", "--porcelain"))
	require.Empty(f.t, f.git(f.work, "branch", "--list", "backport-7-release/v1"))
	picking, err := cherryPickInProgress(func(args ...string) (string, error) {
		return f.runner.Output(context.Background(), f.work, false, args...)
	})
	require.NoError(f.t, err)
	require.False(f.t, picking)
}

func TestBackportPicksWhatEachMergeStyleLanded(t *testing.T) {
	for _, style := range []string{"merge", "squash", "rebase", "fast-forward"} {
		t.Run(style, func(t *testing.T) {
			f := newBackportFixture(t, style)
			result, err := f.backport(f.options())
			require.NoError(t, err)
			require.Equal(t, f.commits, result.Picked)
			require.Len(t, result.Commits, len(f.commits))
			require.Equal(t, "work", f.git(f.work, "branch", "--show-current"), "switches back")
			branch := "refs/heads/backport-7-release/v1"
			require.Equal(t, f.releaseTip, f.git(f.work, "rev-parse", branch+"~"+string(rune('0'+len(f.commits)))))
			require.Equal(t, result.Commits[len(result.Commits)-1], f.git(f.work, "rev-parse", branch))
			for i, commit := range result.Commits {
				require.Contains(t, f.git(f.work, "log", "-1", "--format=%B", commit), "(cherry picked from commit "+f.commits[i]+")")
			}
			require.Equal(t, "one\n", f.git(f.work, "show", branch+":one.txt")+"\n")
			require.Equal(t, "two\n", f.git(f.work, "show", branch+":two.txt")+"\n")
			if style == "rebase" {
				_, err := f.runner.Output(context.Background(), f.work, false, "cat-file", "-e", branch+":main.txt")
				require.Error(t, err, "takes only the pull request's commits, not the base branch's")
			}
			require.Equal(t, "7", f.git(f.work, "config", "--get", "branch.backport-7-release/v1.gitea-cli-backport"))
		})
	}
}

func TestBackportWithoutMergeBaseTakesTheMergedCommitOnly(t *testing.T) {
	f := newBackportFixture(t, "fast-forward")
	opts := f.options()
	opts.MergeBase = ""
	result, err := f.backport(opts)
	require.NoError(t, err)
	require.Equal(t, []string{f.merge}, result.Picked)
}

func TestBackportUndoesAConflictUnlessKept(t *testing.T) {
	conflicting := func(t *testing.T) *backportFixture {
		f := newBackportFixture(t, "squash")
		f.git(f.pusher, "switch", "release/v1")
		f.commit("two.txt", "different\n", "conflicting two")
		f.git(f.pusher, "push", "origin", "HEAD:release/v1")
		return f
	}
	t.Run("undone", func(t *testing.T) {
		f := conflicting(t)
		_, err := f.backport(f.options())
		conflict, ok := err.(*BackportConflict)
		require.True(t, ok, err)
		require.False(t, conflict.Kept)
		require.Equal(t, []string{"two.txt"}, conflict.Files)
		require.ErrorContains(t, err, "nothing was changed. Run again with --keep")
		f.requireUntouched()
	})
	t.Run("kept and continued", func(t *testing.T) {
		f := conflicting(t)
		opts := f.options()
		opts.Keep = true
		_, err := f.backport(opts)
		conflict, ok := err.(*BackportConflict)
		require.True(t, ok, err)
		require.True(t, conflict.Kept)
		require.ErrorContains(t, err, "then gt pr backport --continue")
		require.Equal(t, "backport-7-release/v1", f.git(f.work, "branch", "--show-current"))

		_, err = ContinueBackport(context.Background(), f.runner, f.work)
		require.ErrorContains(t, err, "a cherry-pick is in progress")

		require.NoError(t, os.WriteFile(filepath.Join(f.work, "two.txt"), []byte("two\n"), 0o600))
		f.git(f.work, "add", "two.txt")
		f.git(f.work, "-c", "core.editor=true", "cherry-pick", "--continue")
		result, err := ContinueBackport(context.Background(), f.runner, f.work)
		require.NoError(t, err)
		require.Equal(t, BackportResult{Target: "release/v1", Branch: "backport-7-release/v1", Number: 7, Commits: result.Commits}, result)
		require.Len(t, result.Commits, 1)
		require.Equal(t, "work", f.git(f.work, "branch", "--show-current"))

		require.NoError(t, RecordBackportPull(context.Background(), f.runner, f.work, "backport-7-release/v1", 12))
		config := ReadBranchConfig(context.Background(), f.runner, f.work, "backport-7-release/v1")
		require.Equal(t, int64(12), config.PullNumber)
		require.Equal(t, "origin", config.BaseRemote)
		_, err = f.runner.Output(context.Background(), f.work, false, "config", "--get", "branch.backport-7-release/v1.gitea-cli-backport")
		require.Error(t, err, "forgets the backport state")
	})
}

func TestBackportRefusesWhatItCannotDo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(f *backportFixture, opts *BackportOptions)
		want    string
	}{
		{"changed working tree", func(f *backportFixture, _ *BackportOptions) {
			require.NoError(t, os.WriteFile(filepath.Join(f.work, "base.txt"), []byte("changed\n"), 0o600))
		}, "working tree has changes"},
		{"existing branch", func(f *backportFixture, _ *BackportOptions) {
			f.git(f.work, "branch", "backport-7-release/v1")
		}, `branch "backport-7-release/v1" already exists`},
		{"missing target", func(_ *backportFixture, opts *BackportOptions) { opts.Target = "release/v9" }, "could not fetch the target branch release/v9"},
		{"merge commit off the base branch", func(f *backportFixture, opts *BackportOptions) {
			f.git(f.pusher, "switch", "-c", "elsewhere")
			opts.MergeCommit = f.commit("three.txt", "three\n", "add three")
			f.git(f.pusher, "push", "origin", "HEAD:elsewhere")
			f.git(f.work, "fetch", "--quiet", "origin", "elsewhere")
		}, "is not on the base branch main"},
		{"already backported", func(_ *backportFixture, opts *BackportOptions) { opts.Target = "main" }, "pull request #7 is already on main"},
		{"invalid branch", func(_ *backportFixture, opts *BackportOptions) { opts.Branch = "bad..name" }, `invalid branch name "bad..name"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBackportFixture(t, "merge")
			opts := f.options()
			tc.prepare(f, &opts)
			_, err := f.backport(opts)
			require.ErrorContains(t, err, tc.want)
			if tc.name != "changed working tree" && tc.name != "existing branch" {
				f.requireUntouched()
			}
		})
	}
}

func TestContinueBackportRequiresABackportBranch(t *testing.T) {
	f := newBackportFixture(t, "merge")
	_, err := ContinueBackport(context.Background(), f.runner, f.work)
	require.ErrorContains(t, err, "branch work is not a backport branch")
}
