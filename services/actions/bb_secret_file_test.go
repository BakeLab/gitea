// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBBSecretFileRequiresPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(path, []byte("secret\n"), 0o600))

	got, err := readBBSecretFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("secret\n"), got)

	require.NoError(t, os.Chmod(path, 0o644))
	_, err = readBBSecretFile(path)
	assert.Error(t, err)

	require.NoError(t, os.Chmod(path, 0o600))
	link := filepath.Join(dir, "secret-link")
	require.NoError(t, os.Symlink(path, link))
	_, err = readBBSecretFile(link)
	assert.Error(t, err)
}

func TestReadBBSecretFileRequiresAbsoluteBoundedPath(t *testing.T) {
	_, err := readBBSecretFile("relative-secret")
	assert.Error(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(path, make([]byte, maxBBSecretFileSize+1), 0o600))
	_, err = readBBSecretFile(path)
	assert.Error(t, err)
}
