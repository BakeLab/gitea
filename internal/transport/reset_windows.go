// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"errors"
	"syscall"
)

// connectionReset reports whether the peer reset the connection, which Windows reports as
// WSAECONNRESET, or as WSAECONNABORTED when the connection was dropped while in use.
func connectionReset(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED) || errors.Is(err, syscall.ECONNRESET)
}
