// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
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
		"config --show-scope --get-all clone.defaultRemoteName",
		prefix + "clone -- " + remote + " destination",
		"remote",
		"remote add -- upstream " + parent,
		prefix + "fetch --quiet -- upstream",
		"config branch.main.remote upstream",
		"config branch.main.merge refs/heads/main",
		"config branch.main.pushRemote origin",
	}, runner.calls)
	clone := filepath.Join(cwd, "destination")
	require.Equal(t, []string{cwd, cwd, clone, clone, clone, clone, clone, clone}, runner.dirs)
	require.JSONEq(t, `{"id":8,"full_name":"bob/project","fork":true}`, out.String())
}

func TestSSHCloneTrustsAPIEndpointUnlessSSHHostIsConfigured(t *testing.T) {
	remote := "ssh://git@ssh.example.test:2222/alice/project.git"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":7,"full_name":"alice/project","ssh_url":%q}`, remote)
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
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
	require.Empty(t, diagnostics.String())
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "ssh.example.test:2222", snapshot.Hosts.Hosts[server.URL].SSHHost, "GITEA_TOKEN does not keep a stored instance from saving it")
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

func TestCloneUsesTheInstanceTLSSettings(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":9,"name":"project","full_name":"alice/project","clone_url":"http://%s/alice/project.git"}`, r.Host)
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		host := s.Hosts.Hosts[server.URL]
		host.Insecure, host.ClientCert = true, "/certs/client.pem"
		s.Hosts.Hosts[server.URL] = host
		return nil
	}))
	prefix := "-c http." + server.URL + ".sslCert=/certs/client.pem -c http." + server.URL + ".sslKey=/certs/client.pem -c http." + server.URL + ".sslVerify=false "
	clone := prefix + credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/alice/project.git", "project")
	persist := "config --local --replace-all http." + server.URL + "."
	record := "config --local --replace-all gt." + server.URL + "."
	persisted := []string{persist + "sslCert /certs/client.pem", record + "sslCert /certs/client.pem", persist + "sslKey /certs/client.pem", record + "sslKey /certs/client.pem", persist + "sslVerify false"}
	runner := &fakeGit{results: map[string]string{clone: ""}}
	for _, call := range persisted {
		runner.results[call] = ""
	}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "alice/project"}))
	require.Equal(t, append([]string{clone}, persisted...), runner.calls, "plain git fetch and push in the clone use the same TLS settings")
	require.Equal(t, "project", filepath.Base(runner.dirs[1]))

	require.NoError(t, deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		host := s.Hosts.Hosts[server.URL]
		host.Insecure = false
		s.Hosts.Hosts[server.URL] = host
		return nil
	}))
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_INSTANCE_INSECURE": "true"})
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "alice/project"}))
	require.Equal(t, append([]string{clone}, persisted[:4]...), runner.calls, "GITEA_INSTANCE_INSECURE applies to the clone only")
}

func TestSSHURLCloneWithOnlyAnEnvironmentToken(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","clone_url":"http://%[1]s/alice/project.git","ssh_url":"ssh://git@127.0.0.1:2222/alice/project.git"}`, r.Host)
	}))
	defer server.Close()
	deps, _, diagnostics := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL, "GITEA_TOKEN": "environment-token"})
	sshArgs, err := git.SSHArguments("", false)
	require.NoError(t, err)
	call := strings.Join(append(sshArgs, "clone", "--", "ssh://git@127.0.0.1:2222/alice/project.git", "destination"), " ")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "ssh://git@127.0.0.1:2222/alice/project.git", "destination", "--no-input"}))
	require.Equal(t, []string{call}, runner.calls)
	require.Equal(t, "! gt commands in the clone need the SSH host of "+server.URL+": set GITEA_INSTANCE_SSH_HOST=127.0.0.1:2222\n", diagnostics.String())
	require.NoFileExists(t, filepath.Join(deps.Settings.Dir, "hosts.yml"), "an instance only in the environment leaves the configuration alone")
	runner.calls = nil
	err = Execute(context.Background(), deps, []string{"repo", "clone", "ssh://git@127.0.0.1:2223/alice/project.git", "--no-input"})
	require.EqualError(t, err, "SSH remote does not match a configured instance; if 127.0.0.1:2223 is the SSH host of "+server.URL+", set GITEA_INSTANCE_SSH_HOST=127.0.0.1:2223")
	require.Empty(t, runner.calls)
}

func TestSSHCloneSavesTheAPIEndpointAsSSHHost(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","clone_url":"http://%[1]s/alice/project.git","ssh_url":"ssh://git@127.0.0.1:2222/alice/project.git"}`, r.Host)
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	sshArgs, err := git.SSHArguments("", false)
	require.NoError(t, err)
	call := strings.Join(append(sshArgs, "clone", "--", "ssh://git@127.0.0.1:2222/alice/project.git", "destination"), " ")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "ssh://git@127.0.0.1:2222/alice/project.git", "destination", "--no-input"}))
	require.Equal(t, []string{call}, runner.calls)
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:2222", snapshot.Hosts.Hosts[server.URL].SSHHost)
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "ssh://git@127.0.0.1:2222/alice/project.git", "destination", "--no-input"}))
	require.Equal(t, []string{call}, runner.calls)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "git_protocol", "ssh"}))
	https := credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/alice/project.git", "destination")
	runner.results[https], runner.calls = "", nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", server.URL + "/alice/project", "destination", "--no-input"}))
	require.Equal(t, []string{https}, runner.calls)
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
	require.Contains(t, out.String(), `"original_url": "https://github.com/upstream/project.git"`)
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "--no-upstream")))
	require.Equal(t, []string{clone}, runner.calls)
	for _, unsafe := range []string{"https://user:secret@github.com/upstream/project.git", "file:///srv/project.git", "ext::sh -c touch% /tmp/pwned"} {
		original, runner.calls = unsafe, nil
		require.NoError(t, Execute(context.Background(), deps, args), unsafe)
		require.Equal(t, []string{clone}, runner.calls, unsafe)
	}
	out.Reset()
	original = "https://user:secret@github.com/upstream/project.git"
	require.NoError(t, Execute(context.Background(), deps, append(args, "--no-upstream", "--json=original_url")))
	require.JSONEq(t, `{"original_url":"https://github.com/upstream/project.git"}`, out.String(), "credentials stay out of the output")
}

func TestClonePassesGitFlagsAndNamesTheUpstreamRemote(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"name":"project","full_name":"bob/project","fork":true,"clone_url":"http://%[1]s/bob/project.git","parent":{"full_name":"alice/project","default_branch":"main","clone_url":"http://%[1]s/alice/project.git"}}`, r.Host)
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	clone := credentialCall(t, deps, server.URL, "alice", "clone", "--branch", "dev", "--", server.URL+"/bob/project.git", "project")
	parent := server.URL + "/alice/project.git"
	fetch := credentialCall(t, deps, server.URL, "alice", "fetch", "--quiet", "--depth", "1", "--", "parent")
	shallow := credentialCall(t, deps, server.URL, "alice", "clone", "--depth", "1", "--branch", "dev", "--", server.URL+"/bob/project.git", "project")
	runner := &fakeGit{results: map[string]string{
		clone: "", shallow: "", "remote": "fork", "remote add -- parent " + parent: "", fetch: "", "config branch.main.remote parent": "",
		"config branch.main.merge refs/heads/main": "", "config branch.main.pushRemote fork": "", "config --local gt.repository " + server.URL + "/alice/project": "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "-u", "parent", "--depth", "1", "--", "--branch", "dev"}))
	require.Equal(t, []string{"config --show-scope --get-all clone.defaultRemoteName", shallow, "remote", "remote add -- parent " + parent, fetch, "config branch.main.remote parent", "config branch.main.merge refs/heads/main", "config branch.main.pushRemote fork", "config --local gt.repository " + server.URL + "/alice/project"}, runner.calls)
	require.Equal(t, "✓ Cloned bob/project into project\n✓ Added remote parent for alice/project; gt commands in the clone target it (change with gt repo set-default)\n", diagnostics.String())
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "--no-upstream", "--", "--branch", "dev"}))
	require.Equal(t, []string{clone}, runner.calls)
	// A bare clone is named project.git, so its remotes must be set up there.
	bare := credentialCall(t, deps, server.URL, "alice", "clone", "--bare", "--", server.URL+"/bob/project.git", "project.git")
	fetch = credentialCall(t, deps, server.URL, "alice", "fetch", "--quiet", "--", "parent")
	runner.results[bare], runner.results[fetch] = "", ""
	runner.calls, runner.dirs = nil, nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "-u", "parent", "--", "--bare"}))
	require.Equal(t, bare, runner.calls[1])
	for _, dir := range runner.dirs[2:] {
		require.Equal(t, "project.git", filepath.Base(dir))
	}
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "a", "b"}), "too many arguments")
}

// localClone clones by initializing the clone with its origin and fetches nothing, so that the
// clone's remaining Git configuration runs for real.
type localClone struct{ git.System }

func (r localClone) Output(ctx context.Context, dir string, prompt bool, args ...string) (string, error) {
	switch {
	case slices.Contains(args, "clone"):
		clone := filepath.Join(dir, args[len(args)-1])
		if _, err := r.System.Output(ctx, dir, false, "init", "--quiet", "--", clone); err != nil {
			return "", err
		}
		return r.System.Output(ctx, clone, false, "remote", "add", "origin", args[len(args)-2])
	case slices.Contains(args, "fetch"):
		return "", nil
	}
	return r.System.Output(ctx, dir, prompt, args...)
}

func TestCloneWithAnotherUpstreamNameTargetsTheParent(t *testing.T) {
	gittest.Isolate(t)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"name":"project","full_name":"bob/project","fork":true,"clone_url":"http://%[1]s/bob/project.git","parent":{"full_name":"alice/project","default_branch":"main","clone_url":"http://%[1]s/alice/project.git"}}`, r.Host)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = localClone{}
	cwd := t.TempDir()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", "bob/project", "-u", "parent", "--cwd", cwd}))
	clone := filepath.Join(cwd, "project")
	// A branch pushed to the fork tracks origin.
	for _, args := range [][]string{{"config", "branch.feature.remote", "origin"}, {"symbolic-ref", "HEAD", "refs/heads/feature"}} {
		_, err := deps.Git.Output(context.Background(), clone, false, args...)
		require.NoError(t, err)
	}
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "view", "--cwd", clone, "--json=full_name"}))
	require.JSONEq(t, `{"full_name":"alice/project"}`, out.String())
}

func TestCloneChecksTheForkRemoteNameOnceTheForkIsKnown(t *testing.T) {
	gittest.Isolate(t)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = fmt.Fprintf(w, `{"name":"project","full_name":"alice/project","clone_url":"http://%s/alice/project.git"}`, r.Host)
			return
		}
		_, _ = fmt.Fprintf(w, `{"name":"project","full_name":"bob/project","fork":true,"clone_url":"http://%[1]s/bob/project.git","parent":{"full_name":"alice/project","default_branch":"main","clone_url":"http://%[1]s/alice/project.git"}}`, r.Host)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = localClone{}
	cwd := t.TempDir()
	clone := func(repo, directory string, gitFlags ...string) error {
		return Execute(context.Background(), deps, append([]string{"repo", "clone", repo, directory, "--cwd", cwd, "--"}, gitFlags...))
	}
	require.NoError(t, clone("alice/project", "plain", "-o", "upstream"))
	gitIn := func(args ...string) {
		_, err := deps.Git.Output(context.Background(), cwd, false, args...)
		require.NoError(t, err)
	}
	gitIn("init", "--quiet")
	gitIn("config", "--local", "clone.defaultRemoteName", "upstream")
	require.NoError(t, clone("bob/project", "fork"), "git clone ignores the configuration of the repository it runs in")
	gitIn("config", "--global", "clone.defaultRemoteName", "upstream")
	for directory, gitFlags := range map[string][]string{"flag": {"-o", "upstream"}, "config-flag": {"-c", "clone.defaultRemoteName=upstream"}, "configured": nil} {
		err := clone("bob/project", directory, gitFlags...)
		require.EqualError(t, err, "cloning the fork as Git remote upstream requires another --upstream-remote-name", directory)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.NoDirExists(t, filepath.Join(cwd, directory))
	}
}
