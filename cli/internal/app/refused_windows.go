// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"syscall"
)

// wsaConnRefused is WSAECONNREFUSED, which package syscall does not name.
const wsaConnRefused = syscall.Errno(10061)

// connectionRefused reports whether nothing listens at the address.
func connectionRefused(err error) bool {
	return errors.Is(err, wsaConnRefused) || errors.Is(err, syscall.ECONNREFUSED)
}
