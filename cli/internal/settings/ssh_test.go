// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHEndpointNormalization(t *testing.T) {
	for input, want := range map[string]string{"Git.Example.org": "git.example.org:22", "git.example.org:2222": "git.example.org:2222", "[::1]": "[::1]:22", "[::1]:2222": "[::1]:2222"} {
		got, err := SSHEndpoint(input)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	for _, input := range []string{"", "user@host", "host/path", "host?secret=x", "host#secret", "host:0", "host:65536", "host:bad", "host\\path", "host:22:", "::1", "host:"} {
		_, err := SSHEndpoint(input)
		require.Error(t, err, input)
	}
}

func TestInvalidSSHSettingsCannotBePublished(t *testing.T) {
	store := New(t.TempDir())
	err := store.Update(TargetHosts, func(s *Snapshot) error {
		s.Hosts.Hosts["https://example.org"] = Host{SSHHost: "user@host:2222"}
		return nil
	})
	require.ErrorContains(t, err, "invalid ssh_host")
	snapshot, err := store.Load()
	require.NoError(t, err)
	require.Empty(t, snapshot.Hosts.Hosts)
}
