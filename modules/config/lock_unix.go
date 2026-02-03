// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build unix

package config

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockFile acquires an exclusive lock on the file using flock.
// It polls with non-blocking flock until timeout.
func lockFile(file *os.File, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("flock failed: %w", err)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for file lock")
		}
		time.Sleep(fileLockPollInterval)
	}
}

// unlockFile releases the lock on the file.
func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
