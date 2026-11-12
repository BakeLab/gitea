// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

const maxBBSecretFileSize = 64 * 1024

// readBBSecretFile accepts only private regular files and verifies that the
// file opened is the same file whose permissions and size were checked.
func readBBSecretFile(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("BB executor credential file path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxBBSecretFileSize {
		return nil, errors.New("BB executor credential file must be a private regular file of at most 64 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("BB executor credential file could not be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm()&0o077 != 0 || opened.Size() > maxBBSecretFileSize || !os.SameFile(info, opened) {
		return nil, errors.New("BB executor credential file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBBSecretFileSize+1))
	if err != nil || len(data) > maxBBSecretFileSize {
		return nil, errors.New("BB executor credential file is unreadable or too large")
	}
	return data, nil
}
