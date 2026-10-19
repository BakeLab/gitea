// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package filelock

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocker_WithLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	locker := New(lockPath, lockTimeout)

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

	locker := New(lockPath, lockTimeout)

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
	var counter, active atomic.Int32
	start := make(chan struct{})
	numGoroutines := 10

	for range numGoroutines {
		wg.Go(func() {
			<-start
			err := locker.WithLock(func() error {
				// OS file locks are not synchronization events for the race detector.
				if active.Add(1) != 1 {
					t.Error("lock allowed overlapping critical sections")
				}
				time.Sleep(time.Millisecond)
				counter.Add(1)
				active.Add(-1)
				return nil
			})
			if err != nil {
				t.Errorf("WithLock failed: %v", err)
			}
		})
	}

	close(start)
	wg.Wait()

	if counter.Load() != int32(numGoroutines) {
		t.Errorf("Expected counter to be %d, got %d", numGoroutines, counter.Load())
	}
}

func TestLocker_Timeout(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	unlock, err := New(lockPath, lockTimeout).Acquire()
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer unlock()

	start := time.Now()
	err = New(lockPath, 100*time.Millisecond).WithLock(func() error {
		t.Error("lock acquired while another locker held it")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timeout waiting for file lock") {
		t.Fatalf("expected a lock timeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("timeout took %v", elapsed)
	}
}

const lockTimeout = 5 * time.Second
