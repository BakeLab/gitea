// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestEditorRetainsPrivateDraftAfterSuccessAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("editor fixture uses a POSIX executable")
	}
	for _, fail := range []bool{false, true} {
		directory := t.TempDir()
		program := filepath.Join(directory, "editor with spaces")
		text := "#!/bin/sh\nprintf 'edited draft' > \"$1\"\nchmod 644 \"$1\"\nprintf 'editor diagnostic'\n"
		if fail {
			text += "exit 7\n"
		}
		require.NoError(t, os.WriteFile(program, []byte(text), 0o700))
		out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
		draft, err := (SystemEditor{Directory: directory, Streams: Streams{Out: out, ErrOut: diagnostics}}).Edit(context.Background(), shellquote.Join(program), "initial text")
		if fail {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, "edited draft", draft.Body)
		}
		data, readErr := os.ReadFile(draft.Path)
		require.NoError(t, readErr)
		require.Equal(t, "edited draft", string(data))
		info, statErr := os.Stat(draft.Path)
		require.NoError(t, statErr)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		require.Empty(t, out.String())
		require.Equal(t, "editor diagnostic", diagnostics.String())
	}
}

func TestEditorCanceledBeforeLaunchDoesNotCreateDraft(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	draft, err := (SystemEditor{Directory: directory}).Edit(ctx, "editor", "text")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, draft.Path)
	files, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestEditorIsNotKilledOnCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("editor fixture uses a POSIX executable")
	}
	directory := t.TempDir()
	program := filepath.Join(directory, "slow-editor")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nsleep 0.3\necho finished > \"$1\"\n"), 0o700))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	draft, err := (SystemEditor{Directory: directory}).Edit(ctx, shellquote.Join(program), "text")
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(draft.Path)
	require.NoError(t, err)
	require.Equal(t, "finished\n", string(data))
}

func TestEditorRecoversOriginalInputWhenBufferIsRemoved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("editor fixture uses a POSIX executable")
	}
	directory := t.TempDir()
	program := filepath.Join(directory, "remove-editor")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nrm \"$1\"\nexit 1\n"), 0o700))
	draft, err := (SystemEditor{Directory: directory}).Edit(context.Background(), shellquote.Join(program), "original draft")
	require.ErrorContains(t, err, "original input was saved separately")
	data, err := os.ReadFile(draft.Path)
	require.NoError(t, err)
	require.Equal(t, "original draft", string(data))
	info, err := os.Stat(draft.Path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestCommandArgsKeepsUnquotedExecutablePaths(t *testing.T) {
	name := `editor with spaces\and backslash`
	if runtime.GOOS == "windows" {
		name = "editor with spaces.bat"
	}
	program := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\n"), 0o700))
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{program, []string{program}},
		{shellquote.Join(program) + " --wait", []string{program, "--wait"}},
		{"code --wait", []string{"code", "--wait"}},
	} {
		args, ok := commandArgs(tc.command)
		require.True(t, ok, tc.command)
		require.Equal(t, tc.want, args)
	}
	for _, command := range []string{"", `"unterminated`} {
		_, ok := commandArgs(command)
		require.False(t, ok, command)
	}
}

func TestSubprocessFailuresKeepTheirCause(t *testing.T) {
	const missing = "gt-test-missing-command"
	streams := Streams{Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}}
	require.ErrorIs(t, (SystemEditor{Streams: streams}).EditPath(context.Background(), missing, filepath.Join(t.TempDir(), "draft")), exec.ErrNotFound)
	require.ErrorIs(t, (SystemPager{}).Page(context.Background(), missing, strings.NewReader("text\n"), streams), exec.ErrNotFound)
	require.ErrorIs(t, (SystemBrowser{ErrOut: streams.ErrOut}).Open(context.Background(), "https://example.org", missing), exec.ErrNotFound)
	require.ErrorContains(t, (SystemBrowser{}).Open(context.Background(), "https://example.org", "never"), "browser opening is disabled")
}
