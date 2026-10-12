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

func TestPushAgitPREncodesOptionsAndRefspec(t *testing.T) {
	runner := &checkoutRunner{}
	err := PushAgitPR(context.Background(), runner, AgitOptions{
		Directory: "/work", Remote: "origin", Head: "feature", Base: "main", Topic: "feature",
		Title: "Hello", Body: "line1\nline2", CredentialArgs: []string{"-c", "credential.helper=gt"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"rev-parse --is-inside-work-tree",
		"-c credential.helper=gt push -o title=Hello -o description={base64}bGluZTEKbGluZTI= -- origin refs/heads/feature:refs/for/main/feature",
	}, runner.calls)
}

func TestPushAgitPRRejectsSameBaseAndTopic(t *testing.T) {
	runner := &checkoutRunner{}
	err := PushAgitPR(context.Background(), runner, AgitOptions{Directory: "/work", Remote: "origin", Head: "main", Base: "main", Topic: "main"})
	require.ErrorContains(t, err, "cannot create")
	require.Empty(t, runner.calls)
}

func TestPushAgitPRWithOptionsToRealRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(root, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=gt", "GIT_AUTHOR_EMAIL=gt@example.invalid", "GIT_COMMITTER_NAME=gt", "GIT_COMMITTER_EMAIL=gt@example.invalid")
	runner := System{Env: env}
	remote, work := filepath.Join(root, "remote.git"), filepath.Join(root, "work")
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{root, []string{"init", "--bare", remote}},
		{remote, []string{"config", "receive.advertisePushOptions", "true"}},
		{root, []string{"init", "-b", "feature", work}},
		{work, []string{"commit", "--allow-empty", "-m", "initial"}},
		{work, []string{"remote", "add", "origin", remote}},
	} {
		_, err := runner.Output(context.Background(), step.dir, false, step.args...)
		require.NoError(t, err, step.args)
	}
	require.NoError(t, PushAgitPR(context.Background(), runner, AgitOptions{
		Directory: work, Remote: "origin", Head: "feature", Base: "main", Topic: "topic", Title: "Hello", Body: "line1\nline2",
	}))
	pushed, err := runner.Output(context.Background(), remote, false, "rev-parse", "refs/for/main/topic")
	require.NoError(t, err)
	local, err := runner.Output(context.Background(), work, false, "rev-parse", "HEAD")
	require.NoError(t, err)
	require.Equal(t, local, pushed)
}
