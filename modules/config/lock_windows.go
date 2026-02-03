// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build windows

package config

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// lockFile acquires an exclusive lock on the file using LockFileEx.
// It polls with non-blocking LockFileEx until timeout.
func lockFile(file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	handle := windows.Handle(file.Fd())

	// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY

	for {
		// Lock the first byte (advisory lock)
		var overlapped windows.Overlapped
		err := windows.LockFileEx(handle, flags, 0, 1, 0, &overlapped)
		if err == nil {
			return nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
			return fmt.Errorf("LockFileEx failed: %w", err)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for file lock")
		}
		time.Sleep(fileLockPollInterval)
	}
}

// unlockFile releases the lock on the file.
func unlockFile(file *os.File) error {
	handle := windows.Handle(file.Fd())
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
}
