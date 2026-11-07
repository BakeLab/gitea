// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh/agent"
)

func TestAgentConnectionClosesWhenNoKeyMatches(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	defer listener.Close()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyring := agent.NewKeyring()
	require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: key}))
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// ServeAgent returns once the client closes its connection.
		_ = agent.ServeAgent(keyring, conn)
	}()
	t.Setenv("SSH_AUTH_SOCK", socket)
	_, err = newHTTPSign("", false, "SHA256:missing", nil)
	require.ErrorContains(t, err, "no public key found in ssh-agent")
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("ssh-agent connection stayed open after the lookup failed")
	}
}
