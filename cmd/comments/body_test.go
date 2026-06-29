// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package comments

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveBody(t *testing.T) {
	testCases := []struct {
		name        string
		positional  string
		description string
		stdinPiped  bool
		stdin       string
		expected    string
	}{
		{
			name:       "positional only",
			positional: "from positional",
			expected:   "from positional",
		},
		{
			name:        "description flag only",
			description: "from -d",
			expected:    "from -d",
		},
		{
			name:        "positional wins over description for back-compat",
			positional:  "from positional",
			description: "from -d",
			expected:    "from positional",
		},
		{
			name:        "description wins over piped stdin",
			description: "from -d",
			stdinPiped:  true,
			stdin:       "from stdin",
			expected:    "from -d",
		},
		{
			name:       "piped stdin used when nothing else given",
			stdinPiped: true,
			stdin:      "from stdin",
			expected:   "from stdin",
		},
		{
			name:       "stdin ignored when not piped (interactive terminal)",
			stdinPiped: false,
			stdin:      "should never be read",
			expected:   "",
		},
		{
			name:       "empty when no source provided",
			stdinPiped: false,
			expected:   "",
		},
		{
			name:       "piped but empty stdin yields empty body",
			stdinPiped: true,
			stdin:      "",
			expected:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := resolveBody(tc.positional, tc.description, tc.stdinPiped, strings.NewReader(tc.stdin))
			require.NoError(t, err)
			assert.Equal(t, tc.expected, body)
		})
	}
}

// TestResolveBodyDoesNotReadStdinWhenBodyGiven guards the original bug: when a
// body is supplied positionally (or via -d), stdin must not be consumed, so the
// command can never block on a non-TTY stdin under CI / agent harnesses.
func TestResolveBodyDoesNotReadStdinWhenBodyGiven(t *testing.T) {
	reader := &trackingReader{}

	body, err := resolveBody("positional body", "", true, reader)
	require.NoError(t, err)
	assert.Equal(t, "positional body", body)
	assert.False(t, reader.read, "stdin must not be read when a body is supplied")
}

// trackingReader records whether Read was ever called.
type trackingReader struct {
	read bool
}

func (r *trackingReader) Read(p []byte) (int, error) {
	r.read = true
	return 0, io.EOF
}
