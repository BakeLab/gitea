// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBBGiteaSandbox(t *testing.T) {
	for _, test := range []struct {
		name, address, service string
	}{
		{"default HTTPS port", "https://gitea.int.bb.pub/", "gitea.int.bb.pub:443"},
		{"explicit port", "https://gitea.int.bb.pub:28300/", "gitea.int.bb.pub:28300"},
		{"subpath", "https://gitea.int.bb.pub/forge/", "gitea.int.bb.pub:443"},
		{"IPv6", "https://[fd00::1]:28300/", "[fd00::1]:28300"},
	} {
		t.Run(test.name, func(t *testing.T) {
			origin, err := bbHTTPS(test.address)
			require.NoError(t, err)
			original := map[string]any{
				"allowed_hosts":              []any{"*", "bb-pypi.int.bb.pub"},
				"required_internal_services": []any{"bb-pypi.int.bb.pub:443"},
			}
			before := bbMergeSandbox(original, nil)
			sandbox, err := bbGiteaSandbox(original, origin)
			require.NoError(t, err)
			assert.Equal(t, []any{"*", "bb-pypi.int.bb.pub", origin.Hostname()}, sandbox["allowed_hosts"])
			assert.Equal(t, []any{"bb-pypi.int.bb.pub:443", test.service}, sandbox["required_internal_services"])
			assert.Equal(t, before, original)
			again, err := bbGiteaSandbox(sandbox, origin)
			require.NoError(t, err)
			assert.Equal(t, sandbox, again)
		})
	}
}

func TestBBGiteaSandboxAfterInheritance(t *testing.T) {
	origin, err := bbHTTPS("https://gitea.int.bb.pub/")
	require.NoError(t, err)
	profiles, err := resolveBBProfileExtends([]bbExecutorProfile{
		{Name: "base", Labels: []string{"linux:host"}, Sandbox: map[string]any{
			"allowed_hosts": []any{"base.example"},
		}},
		{Name: "child", Extends: "base", Labels: []string{"arm64:host"}, Sandbox: map[string]any{
			"allowed_hosts": []any{"child.example"},
		}},
	})
	require.NoError(t, err)
	sandbox, err := bbGiteaSandbox(profiles[1].Sandbox, origin)
	require.NoError(t, err)
	assert.Equal(t, []any{"child.example", "gitea.int.bb.pub"}, sandbox["allowed_hosts"])
	assert.Equal(t, []any{"gitea.int.bb.pub:443"}, sandbox["required_internal_services"])
	assert.Equal(t, []any{"base.example"}, profiles[0].Sandbox["allowed_hosts"])
}

func TestBBGiteaSandboxRejectsNonArrayDeclarations(t *testing.T) {
	origin, err := bbHTTPS("https://gitea.int.bb.pub/")
	require.NoError(t, err)
	for _, key := range []string{"allowed_hosts", "required_internal_services"} {
		t.Run(key, func(t *testing.T) {
			_, err := bbGiteaSandbox(map[string]any{key: "invalid"}, origin)
			require.Error(t, err)
		})
	}
}
