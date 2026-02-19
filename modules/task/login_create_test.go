// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import "testing"

func TestShouldCheckTokenUniqueness(t *testing.T) {
	tests := []struct {
		name                string
		token               string
		sshAgent            bool
		sshKey              string
		sshCertPrincipal    string
		sshKeyFingerprint   string
		wantCheckUniqueness bool
	}{
		{
			name:                "token only",
			token:               "token",
			wantCheckUniqueness: true,
		},
		{
			name:                "token with ssh agent",
			token:               "token",
			sshAgent:            true,
			wantCheckUniqueness: false,
		},
		{
			name:                "token with ssh key path",
			token:               "token",
			sshKey:              "~/.ssh/id_ed25519",
			wantCheckUniqueness: false,
		},
		{
			name:                "token with ssh cert principal",
			token:               "token",
			sshCertPrincipal:    "principal",
			wantCheckUniqueness: false,
		},
		{
			name:                "token with ssh key fingerprint",
			token:               "token",
			sshKeyFingerprint:   "SHA256:example",
			wantCheckUniqueness: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldCheckTokenUniqueness(tt.token, tt.sshAgent, tt.sshKey, tt.sshCertPrincipal, tt.sshKeyFingerprint)
			if got != tt.wantCheckUniqueness {
				t.Fatalf("expected %v, got %v", tt.wantCheckUniqueness, got)
			}
		})
	}
}
