// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package version

import (
	"fmt"
	"runtime"
	"strings"
)

// Version holds the current tea version.
// This is set at build time via ldflags.
// If the Version is moved to another package or name changed,
// build flags in .goreleaser.yaml or Makefile need to be updated accordingly.
var Version = "development"

// Tags holds the build tags used
var Tags = ""

// Format returns a human-readable version string including
// Go version and build tags when available.
func Format() string {
	s := fmt.Sprintf("Version: %s\tgolang: %s",
		Version,
		strings.ReplaceAll(runtime.Version(), "go", ""))

	if len(Tags) != 0 {
		s += fmt.Sprintf("\tbuilt with: %s", strings.ReplaceAll(Tags, " ", ", "))
	}

	return s
}
