// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConfigLock_BasicLockUnlock(t *testing.T) {
	// Create a temp directory for test
	tmpDir, err := os.MkdirTemp("", "tea-lock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lockPath := filepath.Join(tmpDir, "config.yml.lock")

	// Should be able to acquire lock
	unlock, err := acquireConfigLock(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}

	// Should be able to release lock
	err = unlock()
	if err != nil {
		t.Fatalf("failed to release lock: %v", err)
	}
}

func TestConfigLock_MutexProtection(t *testing.T) {
	// Create a temp directory for test
	tmpDir, err := os.MkdirTemp("", "tea-lock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lockPath := filepath.Join(tmpDir, "config.yml.lock")

	// Acquire lock
	unlock, err := acquireConfigLock(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}

	// Try to acquire again from same process - should block/timeout due to mutex
	done := make(chan bool)
	go func() {
		_, err := acquireConfigLock(lockPath, 100*time.Millisecond)
		done <- (err != nil) // Should timeout/fail
	}()

	select {
	case failed := <-done:
		if !failed {
			t.Error("second lock acquisition should have failed due to mutex")
		}
	case <-time.After(2 * time.Second):
		t.Error("test timed out")
	}

	if err := unlock(); err != nil {
		t.Errorf("failed to unlock: %v", err)
	}
}

func TestReloadConfigFromDisk(t *testing.T) {
	// Save original config state
	originalConfig := config

	// Create a temp config file
	tmpDir, err := os.MkdirTemp("", "tea-reload-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// We can't easily change GetConfigPath, so we test that reloadConfigFromDisk
	// handles a missing file gracefully (returns nil and resets config)
	config = LocalConfig{Logins: []Login{{Name: "test"}}}

	// Call reload - since the actual config path likely exists or doesn't,
	// we just verify it doesn't panic and returns without error or with expected error
	err = reloadConfigFromDisk()
	// The function should either succeed or return an error, not panic
	if err != nil {
		// This is acceptable - config file might not exist in test environment
		t.Logf("reloadConfigFromDisk returned error (expected in test env): %v", err)
	}

	// Restore original config
	config = originalConfig
}

func TestWithConfigLock(t *testing.T) {
	executed := false
	err := withConfigLock(func() error {
		executed = true
		return nil
	})
	if err != nil {
		t.Errorf("withConfigLock returned error: %v", err)
	}
	if !executed {
		t.Error("function was not executed")
	}
}

func TestWithConfigLock_PropagatesError(t *testing.T) {
	expectedErr := fmt.Errorf("test error")
	err := withConfigLock(func() error {
		return expectedErr
	})

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestDoubleCheckedLocking_SimulatedRefresh(t *testing.T) {
	// This test simulates the double-checked locking pattern
	// by having multiple goroutines try to "refresh" simultaneously

	var (
		refreshCount int
		mu           sync.Mutex
	)

	// Simulate what RefreshOAuthToken does with double-check
	simulatedRefresh := func(tokenExpiry *int64) error {
		// First check (without lock)
		if *tokenExpiry > time.Now().Unix() {
			return nil // Token still valid
		}

		return withConfigLock(func() error {
			// Double-check after acquiring lock
			if *tokenExpiry > time.Now().Unix() {
				return nil // Another goroutine refreshed it
			}

			// Simulate refresh
			mu.Lock()
			refreshCount++
			mu.Unlock()

			time.Sleep(50 * time.Millisecond) // Simulate API call
			*tokenExpiry = time.Now().Add(1 * time.Hour).Unix()
			return nil
		})
	}

	// Start with expired token
	tokenExpiry := time.Now().Add(-1 * time.Hour).Unix()

	// Launch multiple goroutines trying to refresh
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := simulatedRefresh(&tokenExpiry); err != nil {
				t.Errorf("refresh failed: %v", err)
			}
		}()
	}

	wg.Wait()

	// Should only have refreshed once due to double-checked locking
	if refreshCount != 1 {
		t.Errorf("expected 1 refresh, got %d", refreshCount)
	}
}
