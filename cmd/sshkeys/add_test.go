// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package sshkeys

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyTitleFromFilename(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"id_ed25519.pub", "id_ed25519"},
		{"id_rsa.pub", "id_rsa"},
		{"/home/user/.ssh/id_ed25519.pub", "id_ed25519"},
		{"mykey", "mykey"}, // no extension
		{"my.key.pub", "my.key"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			base := filepath.Base(tc.input)
			title := strings.TrimSuffix(base, filepath.Ext(base))
			assert.Equal(t, tc.expected, title)
		})
	}
}
