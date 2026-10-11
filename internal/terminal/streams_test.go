// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// testPrompter returns a form prompter reading in and the buffer its forms render to.
func testPrompter(in io.Reader) (*FormPrompter, *bytes.Buffer) {
	rendered := &bytes.Buffer{}
	return &FormPrompter{Streams: Streams{In: in, Out: &bytes.Buffer{}, ErrOut: rendered, InTTY: true, ErrTTY: true}}, rendered
}

// accessiblePrompter is testPrompter in accessible mode, which renders field titles as plain text.
func accessiblePrompter(t *testing.T, input string) (*FormPrompter, *bytes.Buffer) {
	t.Helper()
	t.Setenv("TERM", "dumb")
	return testPrompter(strings.NewReader(input))
}

func noEdit(context.Context, string) (Draft, error) { return Draft{}, nil }

type fakePrompt struct{ inputs int }

func (f *fakePrompt) Input(context.Context, string) (string, error) {
	f.inputs++
	return "provided", nil
}
func (f *fakePrompt) SecretInput(context.Context, string) (string, error) { return "", nil }
func (f *fakePrompt) Confirm(context.Context, string) (bool, error)       { return false, nil }

func TestPromptPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy   Policy
		streams  Streams
		expected bool
	}{
		{Policy{Enabled: true}, Streams{InTTY: true, ErrTTY: true}, true},
		{Policy{Enabled: true}, Streams{InTTY: true, OutTTY: true}, false},
		{Policy{Enabled: true, NoInput: true}, Streams{InTTY: true, ErrTTY: true}, false},
		{Policy{Enabled: true, Machine: true}, Streams{InTTY: true, ErrTTY: true}, false},
		{Policy{Enabled: true, Machine: true, Interactive: true}, Streams{InTTY: true, ErrTTY: true}, true},
	} {
		require.Equal(t, tc.expected, tc.policy.CanPrompt(tc.streams))
	}
	prompt := &fakePrompt{}
	value := "supplied"
	asked, err := Require(context.Background(), &value, "title", Streams{}, Policy{}, prompt)
	require.NoError(t, err)
	require.False(t, asked)
	require.Zero(t, prompt.inputs)
	value = ""
	_, err = Require(context.Background(), &value, "title", Streams{}, Policy{}, prompt)
	require.ErrorContains(t, err, "--title")
}
