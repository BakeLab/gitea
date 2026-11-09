// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestReadFileWaitsOutASharingViolation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yml")
	require.NoError(t, os.WriteFile(file, []byte("version: \"1\"\n"), 0o600))
	name, err := windows.UTF16PtrFromString(file)
	require.NoError(t, err)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	_, err = os.ReadFile(file)
	require.ErrorIs(t, err, windows.ERROR_SHARING_VIOLATION)

	time.AfterFunc(50*time.Millisecond, func() { _ = windows.CloseHandle(handle) })
	data, err := ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "version: \"1\"\n", string(data))
}
