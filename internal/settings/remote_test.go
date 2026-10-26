// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryURL(t *testing.T) {
	ports := map[string]Host{"https://example.org:3000/base": {}, "https://example.org:3000": {}}
	bases := map[string]Host{"https://example.org": {}, "https://example.org/base": {}}
	sshPorts := map[string]Host{
		"https://example.org/first":  {SSHHost: "git.example.org:2222"},
		"https://example.org/second": {SSHHost: "git.example.org:2223"},
	}
	for _, test := range []struct {
		raw                string
		hosts              map[string]Host
		wantHost, wantRepo string
		wantErr            string
	}{
		{raw: "https://example.org:3000/base/alice/project.git", hosts: ports, wantHost: "https://example.org:3000/base", wantRepo: "alice/project"},
		{raw: "https://unconfigured.example.org/group/alice/project.git", wantHost: "https://unconfigured.example.org/group", wantRepo: "alice/project"},
		{raw: "ssh://git@git.example.org:2223/alice/project.git", hosts: sshPorts, wantHost: "https://example.org/second", wantRepo: "alice/project"},
		{raw: "git@git.example.org:alice/project.git", hosts: sshPorts, wantErr: "if git.example.org:22 is an instance's SSH host, run gt config set ssh_host git.example.org:22 --host URL"},
		{raw: "ssh://git@example.org:2222/alice/project.git", hosts: map[string]Host{"https://example.org": {}}, wantErr: "does not match a configured instance; if example.org:2222 is the SSH host of https://example.org, run gt config set ssh_host example.org:2222 --host https://example.org"},
		{raw: "git@example.org:owner/repo.git", hosts: bases, wantErr: "multiple instances"},
		{raw: "git.example.org:alice/project.git", hosts: map[string]Host{"https://example.org": {SSHHost: "git.example.org"}}, wantHost: "https://example.org", wantRepo: "alice/project"},
		{raw: "https://example.org/alice/project/issues/7", hosts: bases, wantErr: "ambiguous"},
		{raw: "https://example.org/alice/project/pulls/3", hosts: bases, wantErr: "ambiguous"},
		{raw: "https://example.org/base/alice/project/src/branch/main", hosts: bases, wantErr: "ambiguous"},
		{raw: "https://example.org/alice/project/actions/runs/9", hosts: bases, wantErr: "ambiguous"},
		{raw: "https://unconfigured.example.org/alice/project/issues/7", wantErr: "ambiguous"},
		{raw: "https://unconfigured.example.org/alice/project/issues", wantErr: "ambiguous"},
		{raw: "https://unconfigured.example.org/alice/project/pulls", wantErr: "ambiguous"},
		{raw: "https://unconfigured.example.org/alice/project/src/branch/main", wantErr: "ambiguous"},
		{raw: "https://unconfigured.example.org/group/alice/issues.git", wantHost: "https://unconfigured.example.org/group", wantRepo: "alice/issues"},
		{raw: "ssh://git:private-test-password@example.org/alice/project.git", wantErr: "must not embed credentials"},
		{raw: "https://alice:private-test-password@example.org/alice/project.git", wantErr: "must not embed credentials"},
	} {
		host, repo, err := RepositoryURL(test.raw, test.hosts)
		if test.wantErr != "" {
			require.ErrorContains(t, err, test.wantErr, test.raw)
			require.NotContains(t, err.Error(), "private-test-password")
			continue
		}
		require.NoError(t, err, test.raw)
		require.Equal(t, test.wantHost, host, test.raw)
		require.Equal(t, test.wantRepo, repo, test.raw)
	}
}

func TestRemoteIsSSH(t *testing.T) {
	for remote, want := range map[string]bool{
		"ssh://git@example.org/alice/project.git": true,
		"git@example.org:alice/project.git":       true,
		"example.org:alice/project.git":           true,
		"git@example.org:22/project.git":          true,
		"localhost:3001/alice/project":            false,
		"./a:b":                                   false,
		`C:\repos\project.git`:                    false,
		"C:/repos/project.git":                    false,
		"https://example.org/alice/project.git":   false,
		"http://example.org/alice/project.git":    false,
		"file:///tmp/repo.git":                    false,
	} {
		require.Equal(t, want, RemoteIsSSH(remote), remote)
	}
}

func TestWithoutCredentials(t *testing.T) {
	for raw, want := range map[string]string{
		"https://alice:token@example.org/alice/project.git?token=x#f": "https://example.org/alice/project.git",
		"https://token@example.org/alice/project.git":                 "https://example.org/alice/project.git",
		"ssh://git:secret@example.org:2222/alice/project.git":         "ssh://git@example.org:2222/alice/project.git",
		"git@example.org:alice/project.git":                           "git@example.org:alice/project.git",
		"/srv/project.git":                                            "/srv/project.git",
		"https://alice:to ken@exa mple.org/x":                         "",
		"https://exa mple.org/x":                                      "https://exa mple.org/x",
	} {
		require.Equal(t, want, WithoutCredentials(raw), raw)
	}
}

func TestParseRemoteReadsSCPStyleRemotes(t *testing.T) {
	u, err := ParseRemote("git@git.example.org:alice/project.git")
	require.NoError(t, err)
	require.Equal(t, "ssh://git@git.example.org/alice/project.git", u.String())
}

func TestParseRepositoryAcceptsShorthands(t *testing.T) {
	hosts := map[string]Host{"http://localhost:3001": {}, "https://example.org/gitea": {}, "https://example.org": {}}
	for _, tc := range []struct {
		raw  string
		want RepositoryRef
		err  string
	}{
		{raw: "project", want: RepositoryRef{Name: "project"}},
		{raw: "alice/project", want: RepositoryRef{Owner: "alice", Name: "project"}},
		{raw: "alice/project.git", want: RepositoryRef{Owner: "alice", Name: "project"}},
		{raw: " alice/project ", want: RepositoryRef{Owner: "alice", Name: "project"}},
		{raw: "localhost:alice/project.git", want: RepositoryRef{Host: "http://localhost:3001", Owner: "alice", Name: "project"}},
		{raw: "localhost:3001/alice/project", want: RepositoryRef{Host: "http://localhost:3001", Owner: "alice", Name: "project"}},
		{raw: "LOCALHOST:3001/alice/project.git", want: RepositoryRef{Host: "http://localhost:3001", Owner: "alice", Name: "project"}},
		{raw: "example.org/gitea/alice/project", want: RepositoryRef{Host: "https://example.org/gitea", Owner: "alice", Name: "project"}},
		{raw: "example.org/alice/project", want: RepositoryRef{Host: "https://example.org", Owner: "alice", Name: "project"}},
		{raw: "gitea.com/gitea/cli", want: RepositoryRef{Host: "https://gitea.com", Owner: "gitea", Name: "cli"}},
		{raw: "http://localhost:3001/alice/project.git", want: RepositoryRef{Host: "http://localhost:3001", Owner: "alice", Name: "project"}},
		{raw: "a/b/c/d", err: `invalid repository "a/b/c/d": use OWNER/REPO, HOST/OWNER/REPO or a URL`},
		{raw: "alice/", err: "invalid repository"},
		{raw: "../x", err: "invalid repository"},
		{raw: "", err: "invalid repository"},
	} {
		got, err := ParseRepository(tc.raw, hosts)
		if tc.err != "" {
			require.ErrorContains(t, err, tc.err, tc.raw)
			continue
		}
		require.NoError(t, err, tc.raw)
		require.Equal(t, tc.want, got, tc.raw)
	}
	require.Equal(t, "project", RepositoryRef{Name: "project"}.Slug())
	require.Equal(t, "alice/project", RepositoryRef{Owner: "alice", Name: "project"}.Slug())
}

func TestParseRepositoryRejectsAnInstanceConfiguredForBothSchemes(t *testing.T) {
	hosts := map[string]Host{"http://localhost:3001": {}, "https://localhost:3001": {}}
	for range 20 { // Map iteration order is random.
		_, err := ParseRepository("localhost:3001/alice/project", hosts)
		require.ErrorContains(t, err, "include the scheme")
	}
	got, err := ParseRepository("https://localhost:3001/alice/project", hosts)
	require.NoError(t, err)
	require.Equal(t, "https://localhost:3001", got.Host)
}

func TestResolveHostAcceptsBareHosts(t *testing.T) {
	hosts := map[string]Host{"http://localhost:3001": {}, "https://example.org/gitea": {}}
	for raw, want := range map[string]string{
		"localhost:3001":            "http://localhost:3001",
		"localhost:3001/":           "http://localhost:3001",
		"example.org/gitea":         "https://example.org/gitea",
		"gitea.com":                 "https://gitea.com",
		"https://localhost:3001":    "https://localhost:3001",
		"https://gitea.com:443/x/":  "https://gitea.com/x",
		"http://example.org/gitea/": "http://example.org/gitea",
	} {
		got, err := ResolveHost(raw, hosts)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	_, err := ResolveHost("localhost:3001", map[string]Host{"http://localhost:3001": {}, "https://localhost:3001": {}})
	require.ErrorContains(t, err, "include the scheme")
	_, err = ResolveHost("bad host", hosts)
	require.ErrorContains(t, err, `invalid instance "bad host"`)
}

func TestValidSlugRejectsSCPStyleRemotes(t *testing.T) {
	require.True(t, ValidSlug("alice/project"))
	require.False(t, ValidSlug("git@example.org:alice/project.git"))
}
