// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build unix

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"code.gitea.io/tea/modules/config"
)

func TestConfigLock_CrossProcess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tea-lock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lockPath := filepath.Join(tmpDir, "config.yml.lock")

	unlock, err := config.AcquireConfigLockForTesting(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Fatalf("failed to release lock: %v", err)
		}
	}()

	script := fmt.Sprintf(`
package main

import (
	"os"
	"syscall"
)

func main() {
	file, err := os.OpenFile(%q, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		os.Exit(2)
	}
	defer file.Close()

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		os.Exit(0)
	}

	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	os.Exit(1)
}
`, lockPath)

	scriptPath := filepath.Join(tmpDir, "locktest.go")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("failed to write test script: %v", err)
	}

	cmd := exec.Command("go", "run", scriptPath)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				t.Error("subprocess acquired lock when it should have been held")
			} else if exitErr.ExitCode() == 2 {
				t.Errorf("subprocess failed to open lock file: %v", err)
			}
		} else {
			t.Errorf("subprocess execution failed: %v", err)
		}
	}
}
