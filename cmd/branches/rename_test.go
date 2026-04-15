// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package branches

import (
	"testing"
)

func TestBranchesRenameArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "valid args",
			args:    []string{"main", "develop"},
			wantErr: false,
		},
		{
			name:    "missing both args",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "missing new branch name",
			args:    []string{"main"},
			wantErr: true,
		},
		{
			name:    "too many args",
			args:    []string{"main", "develop", "extra"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRenameArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRenameArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
