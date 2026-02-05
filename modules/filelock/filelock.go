// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package filelock

import (
	"fmt"
	"os"
	"time"
)

const (
	// DefaultTimeout is the default timeout for acquiring a file lock.
	DefaultTimeout = 5 * time.Second

	// FileLockPollInterval is how often to retry acquiring the file lock.
	FileLockPollInterval = 50 * time.Millisecond
)

// Locker provides file-based locking with timeout.
type Locker struct {
	path    string
	timeout time.Duration
}

// New creates a Locker for the given lock file path.
func New(lockPath string, timeout time.Duration) *Locker {
	return &Locker{
		path:    lockPath,
		timeout: timeout,
	}
}

// WithLock executes fn while holding the lock.
func (l *Locker) WithLock(fn func() error) (retErr error) {
	unlock, err := l.Acquire()
	if err != nil {
		return err
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil && retErr == nil {
			retErr = fmt.Errorf("failed to release file lock: %w", unlockErr)
		}
	}()

	return fn()
}

// Acquire acquires the file lock and returns an unlock function.
// The caller must call the unlock function to release the lock.
func (l *Locker) Acquire() (unlock func() error, err error) {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	if err := lockFile(file, l.timeout); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to acquire file lock: %w", err)
	}

	return func() error {
		unlockErr := unlockFile(file)
		closeErr := file.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}
