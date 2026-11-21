// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !darwin && !linux

package transport

import "errors"

// queuedBytes cannot read the send queue of a socket on this system.
func queuedBytes(uintptr) (int, error) {
	return 0, errors.ErrUnsupported
}
