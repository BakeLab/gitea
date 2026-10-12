// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"gitea.dev/cli/internal/clierr"
	"github.com/spf13/cobra"
)

func writeDownload(cmd *cobra.Command, body io.Reader, output string) error {
	return writeOutput(cmd, output, func(destination io.Writer) error { _, err := io.Copy(destination, body); return err })
}

// writeOutput writes to stdout when output is empty or -; otherwise it publishes a complete
// private file without replacing another writer.
func writeOutput(cmd *cobra.Command, output string, write func(io.Writer) error) error {
	if output == "" || output == "-" {
		return write(cmd.OutOrStdout())
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".gt-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	copyErr := write(file)
	closeErr := file.Close()
	if copyErr != nil {
		if cmd.Context().Err() != nil {
			return cmd.Context().Err()
		}
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Linking never replaces a file created while the download ran.
	err = os.Link(file.Name(), output)
	if errors.Is(err, os.ErrExist) {
		return clierr.Wrap(clierr.Usage, errors.New("output file already exists"))
	}
	return err
}

func validateDownloadDestination(output string) error {
	if output == "" {
		return clierr.Wrap(clierr.Usage, errors.New("supply --output-file, or --output-file - for stdout"))
	}
	if output == "-" {
		return nil
	}
	if _, err := os.Lstat(output); err == nil {
		return clierr.Wrap(clierr.Usage, errors.New("output file already exists"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
