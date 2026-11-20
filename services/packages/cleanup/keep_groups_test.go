// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	"regexp"
	"testing"

	packages_model "gitea.dev/models/packages"

	"github.com/stretchr/testify/assert"
)

func TestCleanupCandidates(t *testing.T) {
	versions := []*packages_model.PackageVersion{
		{ID: 1, LowerVersion: "1+target.cuda.znver4.4"},
		{ID: 2, LowerVersion: "1+target.cuda.znver4.3"},
		{ID: 3, LowerVersion: "1+target.cuda.znver4.2"},
		{ID: 4, LowerVersion: "1+target.cuda.arm64.3"},
		{ID: 5, LowerVersion: "1+target.cuda.znver4.1"},
		{ID: 6, LowerVersion: "1+target.cuda.arm64.2"},
		{ID: 7, LowerVersion: "1+target.cuda.arm64.1"},
		{ID: 8, LowerVersion: "1+target.cuda.arm64.0"},
		{ID: 9, LowerVersion: "legacy3"},
		{ID: 10, LowerVersion: "legacy2"},
		{ID: 11, LowerVersion: "legacy1"},
		{ID: 12, LowerVersion: "legacy0"},
	}
	pattern := regexp.MustCompile(`^pypi/breadboard-guest-image/.*[.+]target\.(.+)\.[0-9]+$`)
	assert.Equal(t, []*packages_model.PackageVersion{versions[4], versions[7], versions[11]}, cleanupCandidates(versions, 3, "pypi/breadboard-guest-image/", pattern))
	// No configuration and unrelated packages preserve the previous global behavior.
	assert.Equal(t, versions[3:], cleanupCandidates(versions, 3, "pypi/breadboard/", pattern))
	assert.Equal(t, versions[3:], cleanupCandidates(versions, 3, "pypi/breadboard-guest-image/", nil))
	assert.Equal(t, versions, cleanupCandidates(versions, 0, "pypi/breadboard-guest-image/", pattern))
	assert.Empty(t, cleanupCandidates(nil, 3, "pypi/breadboard-guest-image/", pattern))
}
