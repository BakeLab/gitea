// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import "golang.org/x/sys/unix"

// queuedBytes returns the bytes the socket fd holds that the peer has not acknowledged.
func queuedBytes(fd uintptr) (int, error) {
	return unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_NWRITE)
}
