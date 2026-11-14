// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"testing"

	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
)

func TestAnonymousGenericDownloads(t *testing.T) {
	defer test.MockVariableValue(&setting.Packages.AnonymousGenericDownloads, []string{"BakeLab/bb-download-probe"})()
	for _, method := range []string{"GET", "HEAD"} {
		assert.True(t, anonymousGenericDownloadAllowed(method, "bakelab", "bb-download-probe"))
	}
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		assert.False(t, anonymousGenericDownloadAllowed(method, "bakelab", "bb-download-probe"))
	}
	assert.False(t, anonymousGenericDownloadAllowed("GET", "other-owner", "bb-download-probe"))
	assert.False(t, anonymousGenericDownloadAllowed("GET", "bakelab", "private-package"))
	setting.Packages.AnonymousGenericDownloads = []string{"BakeLab/*"}
	assert.False(t, anonymousGenericDownloadAllowed("GET", "bakelab", "bb-download-probe"))
	setting.Packages.AnonymousGenericDownloads = nil
	assert.False(t, anonymousGenericDownloadAllowed("GET", "bakelab", "bb-download-probe"))
}
