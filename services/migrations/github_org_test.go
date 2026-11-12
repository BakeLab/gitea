// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"net/url"
	"testing"
)

func TestGithubMirrorRemoteAddress(t *testing.T) {
	const token = "new-token"
	tests := []struct {
		name       string
		remote     string
		want       string
		wantUpdate bool
		wantError  bool
	}{
		{"missing credentials", "https://github.com/BakeLab/breadkernel.git", "https://x-access-token:new-token@github.com/BakeLab/breadkernel.git", true, false},
		{"current credentials", "https://x-access-token:new-token@github.com/BakeLab/breadkernel.git", "", false, false},
		{"rotated credentials", "https://x-access-token:old-token@github.com/BakeLab/breadkernel.git", "https://x-access-token:new-token@github.com/BakeLab/breadkernel.git", true, false},
		{"wrong repository", "https://github.com/BakeLab/other.git", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote, err := url.Parse(tt.remote)
			if err != nil {
				t.Fatal(err)
			}
			got, update, err := githubMirrorRemoteAddress(remote, "BakeLab", "breadkernel", token)
			if (err != nil) != tt.wantError || got != tt.want || update != tt.wantUpdate {
				t.Fatalf("githubMirrorRemoteAddress() = (%q, %t, %v), want (%q, %t, error=%t)", got, update, err, tt.want, tt.wantUpdate, tt.wantError)
			}
		})
	}
}
