// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package flags

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveCreateBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))

	tests := []struct {
		name               string
		description        string
		descriptionFile    string
		descriptionFileSet bool
		stdinPiped         bool
		stdin              string
		want               string
	}{
		{
			name:        "description flag",
			description: "from -d",
			want:        "from -d",
		},
		{
			name:               "description file",
			descriptionFile:    file,
			descriptionFileSet: true,
			want:               "from file",
		},
		{
			name:               "description file wins over description",
			description:        "from -d",
			descriptionFile:    file,
			descriptionFileSet: true,
			want:               "from file",
		},
		{
			name:               "dash reads stdin",
			descriptionFile:    "-",
			descriptionFileSet: true,
			stdin:              "from stdin",
			want:               "from stdin",
		},
		{
			name:        "description wins over piped stdin",
			description: "from -d",
			stdinPiped:  true,
			stdin:       "from stdin",
			want:        "from -d",
		},
		{
			name:       "piped stdin",
			stdinPiped: true,
			stdin:      "from stdin",
			want:       "from stdin",
		},
		{
			name:        "empty description falls back to piped stdin",
			description: "",
			stdinPiped:  true,
			stdin:       "from stdin",
			want:        "from stdin",
		},
		{
			name: "empty when no source provided",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCreateBody(tt.description, tt.descriptionFile, tt.descriptionFileSet, tt.stdinPiped, strings.NewReader(tt.stdin))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveEditBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "body.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))

	tests := []struct {
		name               string
		description        string
		descriptionSet     bool
		descriptionFile    string
		descriptionFileSet bool
		stdin              string
		wantBody           string
		wantSet            bool
	}{
		{
			name: "no description flag",
		},
		{
			name:           "description flag",
			description:    "from -d",
			descriptionSet: true,
			wantBody:       "from -d",
			wantSet:        true,
		},
		{
			name:           "empty description clears body",
			descriptionSet: true,
			wantSet:        true,
		},
		{
			name:               "description file",
			descriptionFile:    file,
			descriptionFileSet: true,
			wantBody:           "from file",
			wantSet:            true,
		},
		{
			name:               "description file wins over description",
			description:        "from -d",
			descriptionSet:     true,
			descriptionFile:    file,
			descriptionFileSet: true,
			wantBody:           "from file",
			wantSet:            true,
		},
		{
			name:               "dash reads stdin",
			descriptionFile:    "-",
			descriptionFileSet: true,
			stdin:              "from stdin",
			wantBody:           "from stdin",
			wantSet:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveEditBody(tt.description, tt.descriptionSet, tt.descriptionFile, tt.descriptionFileSet, strings.NewReader(tt.stdin))
			require.NoError(t, err)
			if !tt.wantSet {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantBody, *got)
		})
	}
}

func TestResolveDescriptionSourceError(t *testing.T) {
	_, err := resolveCreateBody("", filepath.Join(t.TempDir(), "missing.md"), true, false, strings.NewReader(""))
	require.ErrorContains(t, err, "could not read description file")
}
