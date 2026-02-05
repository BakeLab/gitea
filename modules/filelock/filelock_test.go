// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package filelock

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLocker_WithLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	locker := New(lockPath, DefaultTimeout)

	counter := 0
	err := locker.WithLock(func() error {
		counter++
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock failed: %v", err)
	}
	if counter != 1 {
		t.Errorf("Expected counter to be 1, got %d", counter)
	}

	// Lock file should have been created
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Error("Lock file should have been created")
	}
}

func TestLocker_Acquire(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	locker := New(lockPath, DefaultTimeout)

	unlock, err := locker.Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	// Lock should be held
	if unlock == nil {
		t.Fatal("unlock function should not be nil")
	}

	// Release the lock
	if err := unlock(); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}
}

func TestLocker_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	locker := New(lockPath, 5*time.Second)

	var wg sync.WaitGroup
	counter := 0
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := locker.WithLock(func() error {
				// Read-modify-write to check for race conditions
				tmp := counter
				time.Sleep(1 * time.Millisecond)
				counter = tmp + 1
				return nil
			})
			if err != nil {
				t.Errorf("WithLock failed: %v", err)
			}
		}()
	}

	wg.Wait()

	if counter != numGoroutines {
		t.Errorf("Expected counter to be %d, got %d (possible race condition)", numGoroutines, counter)
	}
}
