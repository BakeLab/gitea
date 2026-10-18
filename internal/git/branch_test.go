// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"os/exec"
	"testing"

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
