// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/require"
)

type failedUploadReader struct{}

func (failedUploadReader) Read([]byte) (int, error) {
	return 0, errors.New("upload interrupted")
}

func TestLocalStorageFailedUploadClosesFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStorage(t.Context(), &setting.Storage{Path: root})
	require.NoError(t, err)
	_, err = store.Save("object", failedUploadReader{}, -1)
	require.ErrorContains(t, err, "upload interrupted")
	entries, err := os.ReadDir(filepath.Join(root, "tmp"))
	require.NoError(t, err)
	require.Empty(t, entries)
	fds, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.False(t, strings.HasPrefix(target, root+"/"), "upload descriptor remains open: %s", target)
	}
}
