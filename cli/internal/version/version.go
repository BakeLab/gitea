// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package version reports the version of the gt build.
package version

import (
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Version holds the current gt version.
// This is set at build time via ldflags.
// If the Version is moved to another package or name changed,
// build flags in the root CI workflow or Makefile need to be updated accordingly.
var Version = "development"

// Tags holds the build tags used
var Tags = ""

// release matches the versions of tagged releases, such as 1.0.0 or 1.1.0-rc1; builds between
// releases carry a +commit suffix.
var release = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

// IsRelease reports whether version is that of a tagged release rather than a build between releases.
func IsRelease(version string) bool { return release.MatchString(version) }

// Format describes the build like gh version: the version and its date, the release notes of a
// release, and the Go version and build tags:
//
//	gt version 1.0.0 (2026-10-01)
//	https://gitea.com/gitea/cli/releases/tag/v1.0.0
//	built with go1.27.2
func Format() string {
	info, _ := debug.ReadBuildInfo()
	return format(Version, Tags, runtime.Version(), info)
}

// Current returns the version of this build: the ldflags stamp, else the module version recorded
// by go install, else "development".
func Current() string {
	info, _ := debug.ReadBuildInfo()
	return resolve(Version, info)
}

func resolve(version string, info *debug.BuildInfo) string {
	// A build without a checkout (a source archive) stamps an empty version.
	if version != "" && version != "development" {
		return version
	}
	// go install gitea.dev/cli@v1.0.0 stamps the module version instead of the ldflags.
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "development"
}

func format(version, tags, goVersion string, info *debug.BuildInfo) string {
	version = resolve(version, info)
	date := ""
	if info != nil {
		for _, setting := range info.Settings {
			if setting.Key != "vcs.time" {
				continue
			}
			if built, err := time.Parse(time.RFC3339, setting.Value); err == nil {
				date = " (" + built.UTC().Format(time.DateOnly) + ")"
			}
		}
	}
	text := "gt version " + version + date + "\n"
	if strings.HasPrefix(version, "trim-") {
		text += "https://gitea.int.bb.pub/BakeLab/gitea/releases/tag/" + version + "\n"
	} else if IsRelease(version) {
		text += "https://gitea.com/gitea/cli/releases/tag/v" + version + "\n"
	}
	text += "built with " + goVersion
	if tags = strings.Join(strings.Fields(tags), ", "); tags != "" {
		text += " (tags: " + tags + ")"
	}
	return text
}
