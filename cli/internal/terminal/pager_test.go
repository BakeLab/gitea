// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

type recordingPager struct {
	command string
	input   string
	calls   int
}

func (p *recordingPager) Page(_ context.Context, command string, input io.Reader, streams Streams) error {
	p.calls++
	p.command = command
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	p.input = string(data)
	_, err = streams.Out.Write(append([]byte("PAGED:"), data...))
	return err
}

func TestWriteHumanPagesMultiLineTTYOutput(t *testing.T) {
	out := &bytes.Buffer{}
	pager := &recordingPager{}
	streams := Streams{Out: out, OutTTY: true}
	require.NoError(t, WriteHuman(context.Background(), streams, "less -R", pager, "one\ntwo\n"))
	require.Equal(t, 1, pager.calls)
	require.Equal(t, "less -R", pager.command)
	require.Equal(t, "one\ntwo\n", pager.input)
	require.Equal(t, "PAGED:one\ntwo\n", out.String())
}

func TestWriteHumanSkipsPagerWhenNotNeeded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		streams Streams
		command string
		text    string
	}{
		{"non-tty", Streams{OutTTY: false}, "less", "one\ntwo\n"},
		{"empty pager", Streams{OutTTY: true}, "", "one\ntwo\n"},
		{"cat disables", Streams{OutTTY: true}, "cat", "one\ntwo\n"},
		{"single line", Streams{OutTTY: true}, "less", "one\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			tc.streams.Out = out
			pager := &recordingPager{}
			require.NoError(t, WriteHuman(context.Background(), tc.streams, tc.command, pager, tc.text))
			require.Zero(t, pager.calls)
			require.Equal(t, tc.text, out.String())
		})
	}
}

func TestSystemPagerUsesLiteralArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pager fixture uses a POSIX executable")
	}
	directory := t.TempDir()
	program := filepath.Join(directory, "pager with spaces")
	capture := filepath.Join(directory, "captured")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\ncat > \"$1\"\n"), 0o700))
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	command := shellquote.Join(program, capture)
	require.NoError(t, (SystemPager{}).Page(context.Background(), command, strings.NewReader("paged body\n"), Streams{Out: out, ErrOut: diagnostics}))
	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	require.Equal(t, "paged body\n", string(data))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}
