// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSSHPubkeySelection(t *testing.T) {
	tests := []struct {
		name               string
		display            string
		wantSSHKey         string
		wantCertPrincipal  string
		wantKeyFingerprint string
		wantSSHAgent       bool
		wantErr            bool
	}{
		{
			name:               "local ed25519 key",
			display:            "SHA256:abc ssh-ed25519 comment (/home/user/.ssh/id_ed25519.pub)",
			wantSSHKey:         "/home/user/.ssh/id_ed25519",
			wantKeyFingerprint: "SHA256:abc",
		},
		{
			name:               "agent ed25519 key",
			display:            "SHA256:abc ssh-ed25519 comment (ssh-agent)",
			wantKeyFingerprint: "SHA256:abc",
			wantSSHAgent:       true,
		},
		{
			name:              "local certificate",
			display:           "SHA256:abc ssh-ed25519-cert-v01@openssh.com comment - principals: user1,user2 (/home/user/.ssh/id_ed25519-cert.pub)",
			wantSSHKey:        "/home/user/.ssh/id_ed25519",
			wantCertPrincipal: "user1",
		},
		{
			name:              "agent certificate",
			display:           "SHA256:abc ssh-ed25519-cert-v01@openssh.com comment - principals: user1 (ssh-agent)",
			wantCertPrincipal: "user1",
			wantSSHAgent:      true,
		},
		{
			name:    "unexpected display",
			display: "ssh-ed25519 comment",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sshKey, certPrincipal, keyFingerprint, sshAgent, err := parseSSHPubkeySelection(tt.display)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantSSHKey, sshKey)
			assert.Equal(t, tt.wantCertPrincipal, certPrincipal)
			assert.Equal(t, tt.wantKeyFingerprint, keyFingerprint)
			assert.Equal(t, tt.wantSSHAgent, sshAgent)
		})
	}
}
