// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package graceful

import (
	"net"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProvidedFDsRetainsFailure(t *testing.T) {
	once = sync.Once{}
	providedFDsErr = nil
	t.Cleanup(func() { once = sync.Once{}; providedFDsErr = nil })
	t.Setenv(notifySocketEnv, "")
	t.Setenv("LISTEN_PID", "")
	t.Setenv(listenFDsEnv, "invalid")
	first := getProvidedFDs()
	require.Error(t, first)
	require.ErrorIs(t, getProvidedFDs(), first)
}

func TestUnixListenerPreservesExistingPath(t *testing.T) {
	once = sync.Once{}
	providedFDsErr = nil
	t.Cleanup(func() { once = sync.Once{}; providedFDsErr = nil })
	t.Setenv(notifySocketEnv, "")
	t.Setenv(listenFDsEnv, "")
	t.Chdir(t.TempDir())
	path := "socket"
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0o600))
	_, err := GetListenerUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.Error(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "keep", string(data))
	require.NoError(t, os.Remove(path))
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	defer listener.Close()
	_, err = GetListenerUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.Error(t, err)
	connection, err := net.Dial("unix", path)
	require.NoError(t, err)
	require.NoError(t, connection.Close())
}
