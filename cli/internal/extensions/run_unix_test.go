// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package extensions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunReportsSignalAsShellExitCode(t *testing.T) {
	script := filepath.Join(t.TempDir(), "gt-killed")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nkill -TERM $$\n"), 0o755))
	code, err := Run(Command{Path: script})
	require.NoError(t, err)
	assert.Equal(t, 128+15, code)
}
