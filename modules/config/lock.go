// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"sync"
	"time"

	"code.gitea.io/tea/modules/filelock"
)

const (
	// LockTimeout is the default timeout for acquiring the config file lock.
	LockTimeout = 5 * time.Second

	// mutexPollInterval is how often to retry acquiring the in-process mutex.
	mutexPollInterval = 10 * time.Millisecond
)

// configMutex protects in-process concurrent access to the config.
var configMutex sync.Mutex

// acquireConfigLock acquires both the in-process mutex and a file lock.
// Returns an unlock function that must be called to release both locks.
// The timeout applies to acquiring the file lock; the mutex acquisition
// uses the same timeout via a TryLock loop.
func acquireConfigLock(lockPath string, timeout time.Duration) (unlock func() error, err error) {
	// Try to acquire mutex with timeout
	deadline := time.Now().Add(timeout)
	for {
		if configMutex.TryLock() {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for config mutex")
		}
		time.Sleep(mutexPollInterval)
	}

	// Mutex acquired, now try file lock
	remaining := max(time.Until(deadline), 0)
	locker := filelock.New(lockPath, remaining)

	fileUnlock, err := locker.Acquire()
	if err != nil {
		configMutex.Unlock()
		return nil, err
	}

	// Return unlock function
	return func() error {
		unlockErr := fileUnlock()
		configMutex.Unlock()
		return unlockErr
	}, nil
}

// getConfigLockPath returns the path to the lock file for the config.
func getConfigLockPath() string {
	return GetConfigPath() + ".lock"
}

// withConfigLock executes the given function while holding the config lock.
// It acquires the lock, reloads the config from disk, executes fn, and releases the lock.
func withConfigLock(fn func() error) (retErr error) {
	lockPath := getConfigLockPath()
	unlock, err := acquireConfigLock(lockPath, LockTimeout)
	if err != nil {
		return fmt.Errorf("failed to acquire config lock: %w", err)
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil && retErr == nil {
			retErr = fmt.Errorf("failed to release config lock: %w", unlockErr)
		}
	}()

	// Reload config from disk to get latest state
	if err := reloadConfigFromDisk(); err != nil {
		return err
	}

	return fn()
}
