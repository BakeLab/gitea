// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestBrowserRejectsInvalidURLsBeforeLaunchingCommand(t *testing.T) {
	for _, resource := range []string{"javascript:alert(1)", "file:///tmp/file", "https://user:secret@example.org", "https://example.org\n"} {
		err := (SystemBrowser{}).Open(context.Background(), resource, "missing-browser-command")
		require.ErrorContains(t, err, "resource URL without credentials")
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestBrowserUsesLiteralArgumentsAndDiagnosticWriter(t *testing.T) {
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Skip("printf executable is unavailable")
	}
	var diagnostics bytes.Buffer
	resource := "https://example.org/$(echo-secret)"
	command := shellquote.Join(printf, "%s")
	require.NoError(t, (SystemBrowser{ErrOut: &diagnostics}).Open(context.Background(), resource, command))
	require.Equal(t, resource, diagnostics.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, (SystemBrowser{}).Open(ctx, resource, command), context.Canceled)
}
