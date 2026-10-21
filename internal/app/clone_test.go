// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/settings"

	"github.com/stretchr/testify/require"
)

func TestCloneValidatesRemoteBeforeGitAndReturnsJSON(t *testing.T) {
	remote := ""
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project", r.URL.Path)
		_, _ = fmt.Fprintf(w, `{"id":7,"full_name":"alice/project","clone_url":%q}`, remote)
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	prefix := credentialCall(t, deps, server.URL, "alice") + " "
	runner := &fakeGit{results: map[string]string{prefix + "clone -- " + server.URL + "/alice/project.git destination": ""}}
	deps.Git = runner
	args := []string{"repo", "clone", "alice/project", "destination", "--no-input", "--json=id,full_name"}
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	for _, invalid := range []string{"ssh://git@" + parsed.Hostname() + "/alice/project.git", "https://unrelated.example.org/alice/project.git", server.URL + "/bob/project.git", server.URL + "/alice/different.git"} {
		remote = invalid
		require.ErrorContains(t, Execute(context.Background(), deps, args), "clone URL does not match")
		require.Empty(t, runner.calls)
		require.Empty(t, out.String())
	}
	remote = server.URL + "/alice/project.git"
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []string{prefix + "clone -- " + remote + " destination"}, runner.calls)
	require.False(t, runner.prompt)
	require.JSONEq(t, `{"id":7,"full_name":"alice/project"}`, out.String())
	require.Empty(t, diagnostics.String())
	out.Reset()
	runner.calls = nil
	runner.results[prefix+"clone --depth 2 -- "+remote+" destination"] = ""
	require.NoError(t, Execute(context.Background(), deps, append(args, "--depth", "2")))
	require.Equal(t, []string{prefix + "clone --depth 2 -- " + remote + " destination"}, runner.calls)
	runner.calls = nil
	require.ErrorContains(t, Execute(context.Background(), deps, append(args, "--depth", "-1")), "--depth must be nonnegative")
	require.Empty(t, runner.calls)
}

func TestSSHCloneUsesSelectedAccountKeyWithoutPersistingIt(t *testing.T) {
	remote := ""
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":7,"full_name":"alice/project","ssh_url":%q}`, remote)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	remote = "git@" + parsed.Hostname() + ":alice/project.git"
	deps, _, _ := serverDeps(t, server.URL)
	configureHost(t, deps, server.URL, settings.Host{User: "alice", GitProtocol: "ssh", Users: map[string]settings.Account{
		"alice": {AuthMethod: "token", CredentialStore: "file", SSHKey: "/keys/alice key"},
		"bob":   {AuthMethod: "token", CredentialStore: "file", SSHKey: "/keys/bob"},
	}})
	sshArgs, err := git.SSHArguments("/keys/alice key", false)
	require.NoError(t, err)
	call := strings.Join(append(sshArgs, "clone", "--", remote, "destination"), " ")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "alice/project", "destination", "--no-input"}))
	require.Equal(t, []string{call}, runner.calls)
	require.False(t, runner.prompt)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "ssh_agent", "true", "--host", server.URL, "--user", "alice"}))
	runner.calls = nil
	call = "clone -- " + remote + " destination"
	runner.results[call] = ""
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "alice/project", "destination", "--no-input"}))
	require.Equal(t, []string{call}, runner.calls)
}

func TestCloneAddsUpstreamRemoteForForks(t *testing.T) {
	remote, parent := "", ""
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/bob/project", r.URL.Path)
		_, _ = fmt.Fprintf(w, `{"id":8,"name":"project","full_name":"bob/project","fork":true,"clone_url":%q,"parent":{"id":7,"full_name":"alice/project","default_branch":"main","clone_url":%q}}`, remote, parent)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	prefix := credentialCall(t, deps, server.URL, "alice") + " "
	remote = server.URL + "/bob/project.git"
	parent = server.URL + "/alice/project.git"
	runner := &fakeGit{results: map[string]string{
		prefix + "clone -- " + remote + " destination": "",
		"remote":                                   "origin",
		"remote add -- upstream " + parent:         "",
		prefix + "fetch --quiet -- upstream":       "",
		"config branch.main.remote upstream":       "",
		"config branch.main.merge refs/heads/main": "",
		"config branch.main.pushRemote origin":     "",
	}}
	deps.Git = runner
	cwd := t.TempDir()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "destination", "--cwd", cwd, "--no-input", "--json=id,full_name,fork"}))
	require.Equal(t, []string{
		prefix + "clone -- " + remote + " destination",
		"remote",
		"remote add -- upstream " + parent,
		prefix + "fetch --quiet -- upstream",
		"config branch.main.remote upstream",
		"config branch.main.merge refs/heads/main",
		"config branch.main.pushRemote origin",
	}, runner.calls)
	clone := filepath.Join(cwd, "destination")
	require.Equal(t, []string{cwd, clone, clone, clone, clone, clone, clone}, runner.dirs)
	require.JSONEq(t, `{"id":8,"full_name":"bob/project","fork":true}`, out.String())
}

func TestSSHCloneTrustsAPIEndpointUnlessSSHHostIsConfigured(t *testing.T) {
	remote := "ssh://git@ssh.example.test:2222/alice/project.git"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":7,"full_name":"alice/project","ssh_url":%q}`, remote)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	configure := func(sshHost string) {
		configureHost(t, deps, server.URL, settings.Host{User: "alice", GitProtocol: "ssh", SSHHost: sshHost, Users: tokenAccounts(settings.StoreFile, "alice")})
	}
	sshArgs, err := git.SSHArguments("", false)
	require.NoError(t, err)
	call := strings.Join(append(sshArgs, "clone", "--", remote, "destination"), " ")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	args := []string{"repo", "clone", "alice/project", "destination", "--no-input"}
	configure("")
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []string{call}, runner.calls)
	runner.calls = nil
	configure("git.example.test")
	require.ErrorContains(t, Execute(context.Background(), deps, args), "clone URL does not match")
	require.Empty(t, runner.calls)
	configure("ssh.example.test:2222")
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []string{call}, runner.calls)
	runner.calls = nil
	remote = "ssh://git@ssh.example.test:2222/alice/other.git"
	configure("")
	require.ErrorContains(t, Execute(context.Background(), deps, args), "clone URL does not match")
	require.Empty(t, runner.calls)
}

func TestCloneAddsSourceRemoteForMirrors(t *testing.T) {
	original := ""
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":9,"name":"project","full_name":"alice/project","mirror":true,"clone_url":"http://%s/alice/project.git","original_url":%q}`, r.Host, original)
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	clone := credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/alice/project.git", "destination")
	original = "https://github.com/upstream/project.git"
	runner := &fakeGit{results: map[string]string{clone: "", "remote add -- source " + original: ""}}
	deps.Git = runner
	args := []string{"repo", "clone", "alice/project", "destination"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []string{clone, "remote add -- source " + original}, runner.calls)
	require.Equal(t, "✓ Cloned alice/project into destination\n✓ Added remote source for "+original+"\n", diagnostics.String())
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "--json")))
	require.Equal(t, []string{clone, "remote add -- source " + original}, runner.calls)
	require.NotContains(t, out.String(), "github.com")
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "--no-upstream")))
	require.Equal(t, []string{clone}, runner.calls)
	for _, unsafe := range []string{"https://user:secret@github.com/upstream/project.git", "file:///srv/project.git", "ext::sh -c touch% /tmp/pwned"} {
		original, runner.calls = unsafe, nil
		require.NoError(t, Execute(context.Background(), deps, args), unsafe)
		require.Equal(t, []string{clone}, runner.calls, unsafe)
	}
}

func TestClonePassesGitFlagsAndNamesTheUpstreamRemote(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"name":"project","full_name":"bob/project","fork":true,"clone_url":"http://%[1]s/bob/project.git","parent":{"full_name":"alice/project","default_branch":"main","clone_url":"http://%[1]s/alice/project.git"}}`, r.Host)
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	clone := credentialCall(t, deps, server.URL, "alice", "clone", "--branch", "dev", "--", server.URL+"/bob/project.git")
	parent := server.URL + "/alice/project.git"
	fetch := credentialCall(t, deps, server.URL, "alice", "fetch", "--quiet", "--depth", "1", "--", "parent")
	shallow := credentialCall(t, deps, server.URL, "alice", "clone", "--depth", "1", "--branch", "dev", "--", server.URL+"/bob/project.git")
	runner := &fakeGit{results: map[string]string{
		clone: "", shallow: "", "remote": "fork", "remote add -- parent " + parent: "", fetch: "", "config branch.main.remote parent": "",
		"config branch.main.merge refs/heads/main": "", "config branch.main.pushRemote fork": "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "-u", "parent", "--depth", "1", "--", "--branch", "dev"}))
	require.Equal(t, []string{shallow, "remote", "remote add -- parent " + parent, fetch, "config branch.main.remote parent", "config branch.main.merge refs/heads/main", "config branch.main.pushRemote fork"}, runner.calls)
	require.Equal(t, "✓ Cloned bob/project into project\n✓ Added remote parent for alice/project; gt commands in the clone target it (change with gt repo set-default)\n", diagnostics.String())
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "--no-upstream", "--", "--branch", "dev"}))
	require.Equal(t, []string{clone}, runner.calls)
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "a", "b"}), "too many arguments")
}
