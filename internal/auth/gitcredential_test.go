// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"strings"
	"testing"

	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestGitCredentialRoutingSeparatesInstallations(t *testing.T) {
	hosts := map[string]settings.Host{"https://example.test": {}, "https://example.test/gitea": {}, "https://example.test/gitea/nested": {}, "https://example.test:8443/gitea": {}}
	for _, tc := range []struct{ host, path, pinned, want string }{
		{"example.test", "gitea/owner/repo.git", "", "https://example.test/gitea"},
		{"example.test:443", "gitea/owner/repo.git", "", "https://example.test/gitea"},
		{"example.test", "gitea/nested/owner/repo.git", "", "https://example.test/gitea/nested"},
		{"example.test", "gitea/nested/owner/repo.git", "https://example.test/gitea", ""},
		{"example.test", "gitea-other/owner/repo.git", "", "https://example.test"},
		{"example.test", "", "", ""},
		{"example.test:8443", "gitea/owner/repo.git", "", "https://example.test:8443/gitea"},
		{"other.test", "gitea/owner/repo.git", "https://example.test/gitea", ""},
	} {
		got, err := MatchGitInstance(GitRequest{Protocol: "https", Host: tc.host, Path: tc.path}, hosts, tc.pinned, "")
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, path := range []string{"gitea/../other", "gitea/%2e%2e/other", "gitea%2fother", "gitea/%252e%252e/other", `gitea\other`} { //nolint:misspell // The backslash is an intentionally invalid path separator.
		_, err := MatchGitInstance(GitRequest{Protocol: "https", Host: "example.test", Path: path}, hosts, "", "")
		require.Error(t, err, path)
	}
	got, err := MatchGitInstance(GitRequest{Protocol: "http", Host: "example.test", Path: "gitea/owner/repo.git"}, hosts, "", "")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestReadGitCredentialRequestRejectsAmbiguityWithoutEchoingSecrets(t *testing.T) {
	request, err := ReadGitRequest(strings.NewReader("protocol=https\nhost=example.test:8443\npath=gitea/owner/repo.git\nusername=alice\npassword=discarded-secret\n\n"))
	require.NoError(t, err)
	require.Equal(t, GitRequest{"https", "example.test:8443", "gitea/owner/repo.git", "alice"}, request)
	request, err = ReadGitRequest(strings.NewReader("url=https://example.test/gitea/owner/repo.git\n\n"))
	require.NoError(t, err)
	require.Equal(t, "gitea/owner/repo.git", request.Path)
	request, err = ReadGitRequest(strings.NewReader("url=https://alice@example.test/gitea/owner/repo.git\n\n"))
	require.NoError(t, err)
	require.Equal(t, "alice", request.Username)
	for _, input := range []string{"host=secret-one\nhost=secret-two\n", "url=https://alice:secret@example.test/repo\n", "url=https://example.test/repo\nhost=secret.test\n", "host=secret\x00value\n", "password=" + strings.Repeat("secret", 12000) + "\n"} {
		_, err := ReadGitRequest(strings.NewReader(input))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}
