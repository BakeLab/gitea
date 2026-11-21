// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package transport

import (
	"errors"
	"syscall"
)

// connectionReset reports whether the peer reset the connection.
func connectionReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
