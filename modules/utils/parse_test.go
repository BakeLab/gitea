// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import "testing"

func TestArgToIndex(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    int64
		wantErr bool
	}{
		{
			name:    "Valid argument",
			arg:     "#123",
			want:    123,
			wantErr: false,
		},
		{
			name:    "Invalid argument",
			arg:     "abc",
			want:    0,
			wantErr: true,
		},
		{
			name:    "Empty argument",
			arg:     "",
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ArgToIndex(tt.arg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ArgToIndex() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ArgToIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}
