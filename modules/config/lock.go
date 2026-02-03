// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	// LockTimeout is the default timeout for acquiring the config file lock.
	LockTimeout = 5 * time.Second

	// mutexPollInterval is how often to retry acquiring the in-process mutex.
	mutexPollInterval = 10 * time.Millisecond

	// fileLockPollInterval is how often to retry acquiring the file lock.
	fileLockPollInterval = 50 * time.Millisecond
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
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		configMutex.Unlock()
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}

	// Try to acquire file lock with remaining timeout
	remaining := max(time.Until(deadline), 0)

	if err := lockFile(file, remaining); err != nil {
		file.Close()
		configMutex.Unlock()
		return nil, fmt.Errorf("failed to acquire file lock: %w", err)
	}

	// Return unlock function
	return func() error {
		unlockErr := unlockFile(file)
		closeErr := file.Close()
		configMutex.Unlock()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
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
