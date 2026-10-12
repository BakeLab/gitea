// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// syncFixture is a clone of a bare remote whose main branch is one commit ahead of the clone's.
type syncFixture struct {
	t             *testing.T
	runner        System
	work          string
	behind, ahead string
}

func newSyncFixture(t *testing.T) syncFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(root, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=gt", "GIT_AUTHOR_EMAIL=gt@example.invalid", "GIT_COMMITTER_NAME=gt", "GIT_COMMITTER_EMAIL=gt@example.invalid")
	f := syncFixture{t: t, runner: System{Env: env}, work: filepath.Join(root, "work")}
	remote, pusher := filepath.Join(root, "remote.git"), filepath.Join(root, "pusher")
	f.git(root, "init", "--bare", "-b", "main", remote)
	f.git(root, "clone", remote, pusher)
	f.git(pusher, "commit", "--allow-empty", "-m", "first")
	f.git(pusher, "push", "origin", "HEAD:main")
	f.git(root, "clone", remote, f.work)
	f.git(f.work, "switch", "-c", "experiment")
	f.git(pusher, "commit", "--allow-empty", "-m", "second")
	f.git(pusher, "push", "origin", "HEAD:main")
	f.behind, f.ahead = f.git(f.work, "rev-parse", "main"), f.git(pusher, "rev-parse", "HEAD")
	return f
}

func (f syncFixture) git(dir string, args ...string) string {
	f.t.Helper()
	output, err := f.runner.Output(context.Background(), dir, false, args...)
	require.NoError(f.t, err, args)
	return output
}

func (f syncFixture) sync(branch string) (SyncResult, error) {
	return SyncBranch(context.Background(), f.runner, SyncOptions{Directory: f.work, Remote: "origin", Branch: branch})
}

func TestSyncBranchKeepsTheCheckedOutBranch(t *testing.T) {
	for _, tc := range []struct {
		name, checkout string
		prepare        func(f syncFixture)
		want           func(f syncFixture) SyncResult
		tracks         bool
	}{
		{
			name: "another branch is checked out", checkout: "experiment",
			want: func(f syncFixture) SyncResult { return SyncResult{"main", f.ahead, f.behind, "origin"} },
		},
		{
			name: "another branch with local changes", checkout: "experiment",
			prepare: func(f syncFixture) {
				require.NoError(t, os.WriteFile(filepath.Join(f.work, "draft.txt"), []byte("draft"), 0o600))
			},
			want: func(f syncFixture) SyncResult { return SyncResult{"main", f.ahead, f.behind, "origin"} },
		},
		{
			name: "the branch is checked out", checkout: "main",
			want: func(f syncFixture) SyncResult { return SyncResult{"main", f.ahead, f.behind, "origin"} },
		},
		{
			name: "the branch is missing", checkout: "experiment",
			prepare: func(f syncFixture) { f.git(f.work, "branch", "-D", "main") },
			want:    func(f syncFixture) SyncResult { return SyncResult{"main", f.ahead, "", "origin"} },
			tracks:  true,
		},
		{
			name: "the branch is up to date", checkout: "experiment",
			prepare: func(f syncFixture) {
				f.git(f.work, "fetch", "origin")
				f.git(f.work, "branch", "-f", "main", f.ahead)
			},
			want: func(f syncFixture) SyncResult { return SyncResult{"main", f.ahead, f.ahead, "origin"} },
		},
		{
			name: "the branch is ahead", checkout: "main",
			prepare: func(f syncFixture) {
				f.git(f.work, "fetch", "origin")
				f.git(f.work, "reset", "--hard", f.ahead)
				f.git(f.work, "commit", "--allow-empty", "-m", "local")
			},
			want: func(f syncFixture) SyncResult {
				local := f.git(f.work, "rev-parse", "main")
				return SyncResult{"main", local, local, "origin"}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			if tc.checkout == "main" {
				f.git(f.work, "switch", "main")
			}
			if tc.prepare != nil {
				tc.prepare(f)
			}
			want := tc.want(f)
			result, err := f.sync("main")
			require.NoError(t, err)
			require.Equal(t, tc.checkout, f.git(f.work, "branch", "--show-current"))
			require.Equal(t, want.Commit, f.git(f.work, "rev-parse", "main"))
			require.Equal(t, want, result)
			if tc.tracks {
				require.Equal(t, "origin", f.git(f.work, "config", "branch.main.remote"))
				require.Equal(t, "refs/heads/main", f.git(f.work, "config", "branch.main.merge"))
			}
		})
	}
}

func TestSyncBranchRefusesWhatItCannotFastForward(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		prepare       func(f syncFixture)
	}{
		{
			name: "local changes on the checked-out branch", message: "working tree has changes",
			prepare: func(f syncFixture) {
				f.git(f.work, "switch", "main")
				require.NoError(t, os.WriteFile(filepath.Join(f.work, "draft.txt"), []byte("draft"), 0o600))
			},
		},
		{
			name: "diverged history", message: "main and origin/main have diverged",
			prepare: func(f syncFixture) {
				f.git(f.work, "switch", "main")
				f.git(f.work, "commit", "--allow-empty", "-m", "local")
				f.git(f.work, "switch", "experiment")
			},
		},
		{
			name: "checked out in another worktree", message: "main is checked out in the worktree",
			prepare: func(f syncFixture) { f.git(f.work, "worktree", "add", filepath.Join(t.TempDir(), "linked"), "main") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSyncFixture(t)
			tc.prepare(f)
			branch, before := f.git(f.work, "branch", "--show-current"), f.git(f.work, "rev-parse", "main")
			_, err := f.sync("main")
			require.ErrorContains(t, err, tc.message)
			require.Equal(t, branch, f.git(f.work, "branch", "--show-current"))
			require.Equal(t, before, f.git(f.work, "rev-parse", "main"))
		})
	}
}

func TestSyncBranchValidatesItsInput(t *testing.T) {
	f := newSyncFixture(t)
	_, err := SyncBranch(context.Background(), f.runner, SyncOptions{Directory: f.work, Remote: "origin"})
	require.ErrorContains(t, err, "requires a remote and branch")
	_, err = f.sync("bad..name")
	require.Error(t, err)
	_, err = SyncBranch(context.Background(), f.runner, SyncOptions{Directory: t.TempDir(), Remote: "origin", Branch: "main"})
	require.Error(t, err)
}
