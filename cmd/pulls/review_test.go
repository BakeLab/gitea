// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReview(t *testing.T) {
	if os.Getenv("GITEA_TEA_TEST_URL") == "" {
		t.Skip("GITEA_TEA_TEST_URL is not set, skipping test")
	}

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
			errContains: "must specify at least one PR index",
		},
		{
			name:    "one argument",
			args:    []string{"1"},
			wantErr: false,
		},
		{
			name:    "two arguments",
			args:    []string{"1", "2"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := CmdPullsReview
			args := append(tt.args, "--repo", "user/repo")
			err := cmd.Run(context.Background(), args)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			// Don't assert no error, because we expect an error about the missing
			// remote. Just assert that the error is not the one we're looking for.
			if err != nil {
				assert.NotContains(t, err.Error(), "must specify at least one PR index")
			}
		})
	}
}
