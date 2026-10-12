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

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestSetupGitCredentialsPreservesHelpersAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	runner := System{}
	_, err := runner.Output(ctx, cwd, false, "init")
	require.NoError(t, err)
	instance := "https://example.test:8443/gitea"
	key := "credential." + instance + ".helper"
	_, err = runner.Output(ctx, cwd, false, "config", "--local", "--add", key, "existing-helper")
	require.NoError(t, err)
	executable := filepath.Join(cwd, "path with spaces and 'quote'", "gt")
	require.NoError(t, SetupCredentials(ctx, runner, CredentialSetup{Directory: cwd, Local: true, Instance: instance, Executable: executable, User: "alice"}))
	require.NoError(t, SetupCredentials(ctx, runner, CredentialSetup{Directory: cwd, Local: true, Instance: instance, Executable: executable, User: "alice"}))
	data, err := runner.Output(ctx, cwd, false, "config", "--local", "--get-all", key)
	require.NoError(t, err)
	helpers := strings.Split(data, "\n")
	require.Len(t, helpers, 2)
	require.Equal(t, "existing-helper", helpers[0])
	words, err := shellquote.Split(strings.TrimPrefix(helpers[1], "!"))
	require.NoError(t, err)
	require.Equal(t, []string{executable, "auth", "git-credential", "--host", instance, "--user", "alice"}, words)
	data, err = runner.Output(ctx, cwd, false, "config", "--local", "--get", "credential."+instance+".useHttpPath")
	require.NoError(t, err)
	require.Equal(t, "true", data)
}

func TestSetupGitCredentialsWorksWithoutAnExistingGlobalFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "new.gitconfig"))
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, SetupCredentials(context.Background(), System{}, CredentialSetup{Directory: home, Instance: "https://example.test", Executable: executable}))
	data, err := os.ReadFile(filepath.Join(home, "new.gitconfig"))
	require.NoError(t, err)
	require.Contains(t, string(data), "useHttpPath = true")
	require.Contains(t, string(data), "auth git-credential")
}

func TestGitMissingConfigValueCarriesExitCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	_, err := (System{}).Output(context.Background(), t.TempDir(), false, "config", "--file", filepath.Join(t.TempDir(), "missing"), "--get", "not.present")
	var failure *CommandError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, 1, failure.ExitCode)
}
