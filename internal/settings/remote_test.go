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
		{raw: "git@git.example.org:alice/project.git", hosts: sshPorts, wantErr: "does not match"},
		{raw: "git@example.org:owner/repo.git", hosts: bases, wantErr: "multiple instances"},
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
		"https://example.org/alice/project.git":   false,
		"http://example.org/alice/project.git":    false,
		"file:///tmp/repo.git":                    false,
	} {
		require.Equal(t, want, RemoteIsSSH(remote), remote)
	}
}

func TestParseRemoteReadsSCPStyleRemotes(t *testing.T) {
	u, err := ParseRemote("git@git.example.org:alice/project.git")
	require.NoError(t, err)
	require.Equal(t, "ssh://git@git.example.org/alice/project.git", u.String())
}

func TestValidSlugRejectsSCPStyleRemotes(t *testing.T) {
	require.True(t, ValidSlug("alice/project"))
	require.False(t, ValidSlug("git@example.org:alice/project.git"))
}
