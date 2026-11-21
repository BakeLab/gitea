// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// PolicyPath is where the system policy is read from: gitea-cli\policy.yml in the ProgramData
// folder, which Windows reports independently of the user's environment.
func PolicyPath() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil || !filepath.IsAbs(dir) {
		dir = `C:\ProgramData`
	}
	return filepath.Join(dir, "gitea-cli", "policy.yml")
}
