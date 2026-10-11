// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type failingDownload struct{}

func (failingDownload) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestDownloadsPublishOnlyCompletePrivateFiles(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	require.NoError(t, writeDownload(cmd, bytes.NewBufferString("archive"), "-"))
	require.Equal(t, "archive", stdout.String())
	directory := t.TempDir()
	output := filepath.Join(directory, "logs.zip")
	require.ErrorIs(t, writeDownload(cmd, failingDownload{}, output), io.ErrUnexpectedEOF)
	_, err := os.Stat(output)
	require.True(t, errors.Is(err, os.ErrNotExist))
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, writeDownload(cmd, bytes.NewBufferString("archive"), output))
	info, err := os.Stat(output)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.ErrorContains(t, writeDownload(cmd, bytes.NewBufferString("replacement"), output), "already exists")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "archive", string(data))
	entries, err = os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}
