// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package gittest shields tests that run Git from the developer's Git configuration.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// config is the global configuration of an isolated test: an identity for commits, and no
// signing, which a developer's own configuration may require.
const config = `[user]
	name = gt
	email = gt@example.invalid
[commit]
	gpgsign = false
[tag]
	gpgsign = false
`

// Isolate makes the Git commands of test t read a fresh global configuration and no system one,
// through the environment, so that settings such as commit signing cannot fail them. It skips t
// when Git is not installed. Like t.Setenv, it cannot be used in parallel tests.
func Isolate(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	path := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", path)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}
