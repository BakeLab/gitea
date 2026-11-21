// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransientMatchesWindowsResets(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.WSAECONNRESET, syscall.WSAECONNABORTED} {
		require.True(t, transient(&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("wsarecv", errno)}), errno.Error())
	}
}
