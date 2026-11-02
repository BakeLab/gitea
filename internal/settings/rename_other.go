// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package settings

import "os"

func rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
