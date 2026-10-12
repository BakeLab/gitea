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
	setup := CredentialSetup{Directory: cwd, Local: true, KeepHelpers: true, Instance: instance, Executable: executable, User: "alice"}
	require.NoError(t, SetupCredentials(ctx, runner, setup))
	require.NoError(t, SetupCredentials(ctx, runner, setup))
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

func TestSetupGitCredentialsResetsOtherHelpersForTheInstance(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	ctx := context.Background()
	cwd := t.TempDir()
	runner := System{}
	_, err := runner.Output(ctx, cwd, false, "init")
	require.NoError(t, err)
	instance := "https://example.test:8443/gitea"
	key := "credential." + instance + ".helper"
	for _, helper := range [][2]string{{"credential.helper", `!f() { test "$1" = get && printf 'username=other\npassword=leaked\n'; }; f`}, {key, "stale-helper"}} {
		_, err = runner.Output(ctx, cwd, false, "config", "--local", "--add", helper[0], helper[1])
		require.NoError(t, err)
	}
	executable := filepath.Join(t.TempDir(), "gt")
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\nfor last; do :; done\nif [ \"$last\" = get ]; then printf 'username=gt\\npassword=gt-token\\n'; fi\n"), 0o755))
	setup := CredentialSetup{Directory: cwd, Local: true, Instance: instance, Executable: executable}
	require.NoError(t, SetupCredentials(ctx, runner, setup))
	require.NoError(t, SetupCredentials(ctx, runner, setup))
	helper, err := CredentialHelper(instance, executable, "", "")
	require.NoError(t, err)
	data, err := runner.Output(ctx, cwd, false, "config", "--local", "--null", "--get-all", key)
	require.NoError(t, err)
	require.Equal(t, "\x00"+helper+"\x00", data)
	fill := exec.Command("git", "credential", "fill")
	fill.Dir = cwd
	fill.Stdin = strings.NewReader("protocol=https\nhost=example.test:8443\npath=gitea/alice/project.git\n\n")
	output, err := fill.Output()
	require.NoError(t, err)
	require.Contains(t, string(output), "username=gt\npassword=gt-token\n")
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
