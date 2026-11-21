// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package app

import (
	"errors"
	"syscall"
)

// connectionRefused reports whether nothing listens at the address.
func connectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
