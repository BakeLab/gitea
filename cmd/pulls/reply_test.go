// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"context"
	"testing"

	"gitea.dev/tea/modules/config"

	"github.com/stretchr/testify/assert"
)

func TestReply(t *testing.T) {
	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "testLogin",
			URL:     "https://gitea.example.com",
			Token:   "test-token",
			User:    "testUser",
			Default: true,
		}},
	})
	t.Cleanup(func() {
		config.SetConfigForTesting(config.LocalConfig{})
	})

	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
	}{
		{
			name:        "no arguments",
			args:        []string{},
			wantErr:     true,
			errContains: "pull request index and comment ID are required",
		},
		{
			name:        "missing comment id",
			args:        []string{"1"},
			wantErr:     true,
			errContains: "pull request index and comment ID are required",
		},
		{
			name:        "pull index and comment id",
			args:        []string{"1", "2"},
			wantErr:     true,
			errContains: "no reply content provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := CmdPullsReply
			args := append([]string{"reply"}, tt.args...)
			args = append(args, "--login", "testLogin", "--repo", "user/repo")
			err := cmd.Run(context.Background(), args)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
		})
	}
}
