// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHKeyValidationPrecedesTargetResolution(t *testing.T) {
	for _, content := range []string{"", "not a public key", "-----BEGIN OPENSSH PRIVATE KEY-----"} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.pub")
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			requireUsageBeforeRequests(t, "exactly one valid SSH public key", "ssh-key", "create", path)
		})
	}
}
