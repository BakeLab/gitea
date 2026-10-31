// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/git/gittest"
	"github.com/stretchr/testify/require"
)

func TestCurrentBranchConfig(t *testing.T) {
	const head = "symbolic-ref --quiet HEAD"
	for _, test := range []struct {
		name   string
		runner discoverRunner
		want   BranchConfig
		err    error
	}{
		{name: "detached", runner: discoverRunner{}, err: ErrNotOnBranch},
		{name: "untracked", runner: discoverRunner{head: "topic"}, want: BranchConfig{Name: "topic"}},
		{name: "tracking", runner: discoverRunner{
			head: "feat/one", "config --get branch.feat/one.remote": "origin", "config --get branch.feat/one.merge": "refs/heads/feat/one",
		}, want: BranchConfig{Name: "feat/one", Remote: "origin", Merge: "feat/one"}},
		{name: "local upstream", runner: discoverRunner{
			head: "topic", "config --get branch.topic.remote": ".", "config --get branch.topic.merge": "refs/heads/main",
		}, want: BranchConfig{Name: "topic"}},
		{name: "pr checkout", runner: discoverRunner{
			head: "pr/17", "config --get branch.pr/17.remote": "pulls/bob/project", "config --get branch.pr/17.merge": "refs/heads/bob/fix",
			"config --get branch.pr/17.gitea-cli-pr": "17", "config --get branch.pr/17.gitea-cli-base-remote": "origin",
		}, want: BranchConfig{Name: "pr/17", Remote: "pulls/bob/project", Merge: "bob/fix", PullNumber: 17, BaseRemote: "origin"}},
		{name: "malformed pull number", runner: discoverRunner{head: "x", "config --get branch.x.gitea-cli-pr": "seventeen"}, want: BranchConfig{Name: "x"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := CurrentBranchConfig(context.Background(), test.runner, "/work")
			require.ErrorIs(t, err, test.err)
			require.Equal(t, test.want, config)
		})
	}
}

func TestCurrentBranchIgnoresATagWithTheSameName(t *testing.T) {
	gittest.Isolate(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=rel-2"},
		{"-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "--allow-empty", "--message=x"},
		{"tag", "rel-2"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	name, err := CurrentBranch(context.Background(), System{}, dir)
	require.NoError(t, err)
	require.Equal(t, "rel-2", name)
}

func TestLocalBranchesReadsTrackingAndRecordedPullRequests(t *testing.T) {
	gittest.Isolate(t)
	ctx := context.Background()
	dir := t.TempDir()
	run := func(date string, args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		command.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+date)
		out, err := command.Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	run("", "init", "--quiet", "--initial-branch=main")
	run("@1700000000 +0000", "commit", "--quiet", "--allow-empty", "--message=one")
	first := run("", "rev-parse", "HEAD")
	run("", "remote", "add", "origin", "https://example.invalid/alice/project.git")
	run("", "update-ref", "refs/remotes/origin/topic", first)
	run("", "branch", "--quiet", "--track", "local", "main")
	run("", "switch", "--quiet", "--create", "topic", "--track", "origin/topic")
	run("@1700000100 +0000", "commit", "--quiet", "--allow-empty", "--message=two")
	second := run("", "rev-parse", "HEAD")
	run("", "switch", "--quiet", "main")
	require.NoError(t, RenameBaseRemote(ctx, System{}, dir, "origin", "upstream"), "nothing recorded yet")
	require.NoError(t, RecordPull(ctx, System{}, dir, "topic", 17, "origin"))
	require.NoError(t, RecordBaseRemote(ctx, System{}, dir, "local", "fork"))

	branches, err := LocalBranches(ctx, System{}, dir)
	require.NoError(t, err)
	require.Equal(t, []LocalBranch{
		{BranchConfig: BranchConfig{Name: "local", BaseRemote: "fork"}, Commit: first, Committed: time.Unix(1700000000, 0)},
		{BranchConfig: BranchConfig{Name: "main"}, Commit: first, Current: true, Committed: time.Unix(1700000000, 0)},
		{
			BranchConfig: BranchConfig{Name: "topic", Remote: "origin", Merge: "topic", PullNumber: 17, BaseRemote: "origin"},
			Commit:       second, Committed: time.Unix(1700000100, 0), UpstreamCommitted: time.Unix(1700000000, 0),
		},
	}, branches)

	require.NoError(t, RenameBaseRemote(ctx, System{}, dir, "origin", "upstream"))
	require.Equal(t, "upstream", ReadBranchConfig(ctx, System{}, dir, "topic").BaseRemote)
	require.Equal(t, "fork", ReadBranchConfig(ctx, System{}, dir, "local").BaseRemote, "other remotes are kept")
}
