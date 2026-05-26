// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppIncludesWikiCommand(t *testing.T) {
	app := App()
	found := false
	for _, c := range app.Commands {
		if c.Name == "wiki" {
			found = true
			break
		}
	}
	assert.True(t, found)
}
