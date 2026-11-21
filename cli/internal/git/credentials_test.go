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

	"gitea.dev/cli/internal/git/gittest"

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
	setup.User = "bob"
	require.NoError(t, SetupCredentials(ctx, runner, setup))
	data, err = runner.Output(ctx, cwd, false, "config", "--local", "--get-all", key)
	require.NoError(t, err)
	helpers = strings.Split(data, "\n")
	require.Len(t, helpers, 2)
	require.Equal(t, "existing-helper", helpers[0])
	require.Contains(t, helpers[1], " --user bob")
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

func TestRejectCredentialsAsksHelpersToEraseTheAccount(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home := t.TempDir()
	log := filepath.Join(home, "helper.log")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	ctx := context.Background()
	runner := System{}
	_, err := runner.Output(ctx, home, false, "config", "--global", "credential.helper", "!f() { echo \"op=$1\" >> '"+log+"'; cat >> '"+log+"'; }; f")
	require.NoError(t, err)
	store := filepath.Join(home, "git-credentials")
	_, err = runner.Output(ctx, home, false, "config", "--global", "--add", "credential.helper", "store --file="+filepath.ToSlash(store)) // the shell would eat the backslashes of a Windows path
	require.NoError(t, err)
	own := "http://alice:own-password@localhost:3001\n"
	require.NoError(t, os.WriteFile(store, []byte(own+"http://alice:gt-token@localhost:3001/gitea/alice/project.git\n"), 0o600))
	require.Empty(t, HelperScope(ctx, runner, home, "http://localhost:3001"))
	require.NoError(t, RejectCredentials(ctx, runner, home, "http://localhost:3001/gitea", "alice", "gt-token"))
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, "op=erase\nprotocol=http\nhost=localhost:3001\nusername=alice\npassword=gt-token\n", string(data))
	data, err = os.ReadFile(store)
	require.NoError(t, err)
	require.Equal(t, own, string(data), "only the copy of gt's token is erased")
	require.NoError(t, RejectCredentials(ctx, runner, home, "http://localhost:3001", "alice", ""))
	data, err = os.ReadFile(store)
	require.NoError(t, err)
	require.Equal(t, own, string(data), "without a token nothing is erased")
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, SetupCredentials(ctx, runner, CredentialSetup{Directory: home, Instance: "http://localhost:3001", Executable: executable}))
	require.Equal(t, "global", HelperScope(ctx, runner, home, "http://localhost:3001"))
	require.Empty(t, HelperScope(ctx, runner, home, "http://localhost:3002"))
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

func TestPerOperationCredentialAndTLSArguments(t *testing.T) {
	executable, err := filepath.Abs("/usr/bin/gt") // gains a drive letter on Windows
	require.NoError(t, err)
	args, err := CredentialArguments("HTTPS://Example.test:443/gitea/", executable, "alice", "")
	require.NoError(t, err)
	prefix := "credential.https://example.test/gitea."
	require.Equal(t, []string{"-c", prefix + "helper=", "-c", prefix + "helper=!" + shellquote.Join(executable) + " auth git-credential --host https://example.test/gitea --user alice", "-c", prefix + "useHttpPath=true"}, args)
	_, err = CredentialArguments("https://example.test", "gt", "", "")
	require.Error(t, err)

	const section = "http.https://example.test/gitea."
	for _, tc := range []struct {
		name, cert, key string
		insecure        bool
		want            []string
	}{
		{name: "nothing"},
		{name: "key in certificate file", cert: "/c.pem", want: []string{"-c", section + "sslCert=/c.pem", "-c", section + "sslKey=/c.pem"}},
		{name: "separate key", cert: "/c.pem", key: "/k.pem", want: []string{"-c", section + "sslCert=/c.pem", "-c", section + "sslKey=/k.pem"}},
		{name: "insecure", insecure: true, want: []string{"-c", section + "sslVerify=false"}},
	} {
		args, err := TLSArguments("https://example.test/gitea/", tc.cert, tc.key, tc.insecure)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, args, tc.name)
	}
	_, err = TLSArguments("https://example.test", "/c.pem\n[core]", "", false)
	require.ErrorContains(t, err, "cannot contain NUL or line breaks")
	_, err = TLSArguments("ssh://example.test", "/c.pem", "", false)
	require.Error(t, err)
}

func TestSetupTLSIsUndoneOnlyWhereGtWroteIt(t *testing.T) {
	gittest.Isolate(t)
	ctx := context.Background()
	dir := t.TempDir()
	runner := System{}
	_, err := runner.Output(ctx, dir, false, "init", "--quiet")
	require.NoError(t, err)
	const instance = "https://example.test/gitea"
	get := func(key string) string {
		value, _ := runner.Output(ctx, dir, false, "config", "--local", "--get-all", key)
		return value
	}
	urlmatch := func(key, url string) string {
		value, _ := runner.Output(ctx, dir, false, "config", "--get-urlmatch", key, url)
		return value
	}
	for range 2 {
		require.NoError(t, SetupTLS(ctx, runner, dir, true, instance+"/", "/certs/client.pem", "", true))
	}
	repo := instance + "/owner/repo.git"
	require.Equal(t, "/certs/client.pem", urlmatch("http.sslCert", repo))
	require.Equal(t, "/certs/client.pem", urlmatch("http.sslKey", repo))
	require.Equal(t, "false", urlmatch("http.sslVerify", repo))
	require.Empty(t, urlmatch("http.sslCert", "https://example.test/other/repo.git"))
	require.Equal(t, "/certs/client.pem", get("gt."+instance+".sslCert"))
	require.Empty(t, get("gt."+instance+".sslVerify"))

	_, err = runner.Output(ctx, dir, false, "config", "--local", "http."+instance+".sslKey", "/certs/own-key.pem")
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, UnsetTLSCertificate(ctx, runner, dir, true, instance))
	}
	require.Empty(t, get("http."+instance+".sslCert"))
	require.Equal(t, "/certs/own-key.pem", get("http."+instance+".sslKey"), "a key changed since setup is kept")
	require.Empty(t, get("gt."+instance+".sslCert"))
	require.Equal(t, "false", get("http."+instance+".sslVerify"))

	for range 2 {
		require.NoError(t, UnsetInsecureTLS(ctx, runner, dir, true, instance))
	}
	require.Empty(t, get("http."+instance+".sslVerify"))
	_, err = runner.Output(ctx, dir, false, "config", "--local", "http."+instance+".sslVerify", "true")
	require.NoError(t, err)
	require.NoError(t, UnsetInsecureTLS(ctx, runner, dir, true, instance))
	require.Equal(t, "true", get("http."+instance+".sslVerify"))
}
