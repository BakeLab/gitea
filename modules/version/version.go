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

// SDK holds the sdk version from go.mod
var SDK = ""

// Format returns a human-readable version string including
// go version, build tags, and SDK version when available.
func Format() string {
	s := fmt.Sprintf("Version: %s\tgolang: %s",
		bold(Version),
		strings.ReplaceAll(runtime.Version(), "go", ""))

	if len(Tags) != 0 {
		s += fmt.Sprintf("\tbuilt with: %s", strings.ReplaceAll(Tags, " ", ", "))
	}

	if len(SDK) != 0 {
		s += fmt.Sprintf("\tgo-sdk: %s", SDK)
	}

	return s
}

func bold(t string) string {
	return fmt.Sprintf("\033[1m%s\033[0m", t)
}
