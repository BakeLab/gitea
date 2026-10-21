// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package terminal

import "os"

// enableEscapes reports that terminals interpret escape sequences, as they all do outside Windows,
// and changes nothing that needs restoring.
func enableEscapes(*os.File) (bool, func()) { return true, nil }
