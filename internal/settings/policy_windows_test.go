// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolicyPathIgnoresTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ProgramData", dir)
	path := PolicyPath()
	assert.True(t, filepath.IsAbs(path), path)
	assert.False(t, strings.HasPrefix(strings.ToLower(path), strings.ToLower(dir)), "a user must not move the system policy: %s", path)
}
