// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yml")
	policy, err := LoadPolicy(path)
	require.NoError(t, err)
	assert.Equal(t, Policy{}, policy, "a missing policy restricts nothing")
	require.NoError(t, os.WriteFile(path, []byte("extensions: disabled\nextension_hosts: [https://gitea.com]\nextension_owners: [https://gitea.com/gitea]\nextension_credentials: denied\n"), 0o600))
	policy, err = LoadPolicy(path)
	require.NoError(t, err)
	assert.Equal(t, Policy{Path: path, Extensions: "disabled", ExtensionHosts: []string{"https://gitea.com"}, ExtensionOwners: []string{"https://gitea.com/gitea"}, ExtensionCredentials: "denied"}, policy)
	for content, want := range map[string]string{
		"extension: disabled\n":                    "field extension not found",
		"extensions: off\n":                        "extensions must be enabled or disabled",
		"extension_credentials: maybe\n":           "extension_credentials must be allowed or denied",
		"extension_hosts: [gitea.com]\n":           "must be a normalized instance URL",
		"extension_owners: [https://gitea.com/]\n": "use HOST/OWNER",
	} {
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		_, err := LoadPolicy(path)
		require.ErrorContains(t, err, want, content)
	}
}

func TestParseExtensionOwner(t *testing.T) {
	host, owner, err := ParseExtensionOwner("HTTPS://Gitea.com:443/gitea")
	require.NoError(t, err)
	assert.Equal(t, "https://gitea.com", host)
	assert.Equal(t, "gitea", owner)
	host, owner, err = ParseExtensionOwner("https://example.com/git/team")
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/git", host)
	assert.Equal(t, "team", owner)
	_, _, err = ParseExtensionOwner("gitea")
	require.Error(t, err)
}
