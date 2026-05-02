// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build testtools

package config

import "time"

// AcquireConfigLockForTesting exposes the internal lock helper to integration tests.
func AcquireConfigLockForTesting(lockPath string, timeout time.Duration) (func() error, error) {
	return acquireConfigLock(lockPath, timeout)
}
