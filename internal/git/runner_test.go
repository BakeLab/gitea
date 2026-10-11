// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestNoInputPreservesPerOperationSSHCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell executable")
	}
	directory := t.TempDir()
	log := filepath.Join(directory, "ssh-arguments")
	ssh := filepath.Join(directory, "ssh with spaces")
	require.NoError(t, os.WriteFile(ssh, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellquote.Join(log)+"\nexit 1\n"), 0o700))
	env := setEnv(os.Environ(), "GIT_SSH_COMMAND", "")
	env = setEnv(env, "GIT_SSH_VARIANT", "ssh")
	command := shellquote.Join(ssh, "-i", "/keys/key with spaces")
	_, err := (System{Env: env}).Output(context.Background(), directory, false, "-c", "core.sshCommand="+command, "ls-remote", "ssh://git@example.invalid/project.git")
	require.Error(t, err)
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Contains(t, args, "/keys/key with spaces")
	require.Contains(t, args, "-oBatchMode=yes")
}

func TestBatchSSHUsesPlinkOption(t *testing.T) {
	for _, tc := range []struct{ command, variant, want string }{
		{"ssh -i key", "", "ssh -i key -oBatchMode=yes"},
		{`"C:/Program Files/PuTTY/plink.exe" -P 2222`, "", `"C:/Program Files/PuTTY/plink.exe" -P 2222 -batch`},
		{`C:\PuTTY\TortoisePlink.exe`, "", `C:\PuTTY\TortoisePlink.exe -batch`},
		{"/usr/local/bin/wrapper", "plink", "/usr/local/bin/wrapper -batch"},
		{"/usr/bin/plink", "ssh", "/usr/bin/plink -oBatchMode=yes"},
	} {
		env := []string{"GIT_SSH_COMMAND=" + tc.command}
		if tc.variant != "" {
			env = append(env, "GIT_SSH_VARIANT="+tc.variant)
		}
		require.Contains(t, batchSSHEnv(context.Background(), "", env, []string{"fetch"}), "GIT_SSH_COMMAND="+tc.want, tc.command)
	}
}

func TestNetworkSubcommand(t *testing.T) {
	for args, want := range map[string]bool{
		"push -- origin main": true, "-c core.sshCommand=ssh fetch origin": true, "-C /work ls-remote origin": true, "clone url": true,
		"rev-parse HEAD": false, "-c credential.helper=tea config --get core.sshCommand": false, "remote get-url origin": false, "": false,
	} {
		require.Equal(t, want, networkSubcommand(strings.Fields(args)), args)
	}
}

func TestNoInputKeepsGitSSHProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell executable")
	}
	directory := t.TempDir()
	log := filepath.Join(directory, "ssh-arguments")
	ssh := filepath.Join(directory, "plink")
	require.NoError(t, os.WriteFile(ssh, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellquote.Join(log)+"\nexit 1\n"), 0o700))
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_SSH_COMMAND=") {
			env = append(env, entry)
		}
	}
	env = setEnv(env, "GIT_CONFIG_GLOBAL", filepath.Join(directory, "gitconfig"))
	env = setEnv(env, "GIT_CONFIG_NOSYSTEM", "1")
	env = setEnv(env, "GIT_SSH_VARIANT", "ssh")
	env = setEnv(env, "GIT_SSH", ssh)
	_, err := (System{Env: env}).Output(context.Background(), directory, false, "ls-remote", "ssh://git@example.invalid/project.git")
	require.Error(t, err)
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Contains(t, string(data), "example.invalid")
}
