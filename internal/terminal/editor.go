// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/kballard/go-shellquote"
)

// Draft holds edited text and the private file retained until successful submission.
type Draft struct {
	Path string
	Body string
}

// Editor edits drafts or existing files using an explicitly selected command.
type Editor interface {
	Edit(context.Context, string, string) (Draft, error)
	EditPath(context.Context, string, string) error
}

// SystemEditor uses private temporary files and the application's terminal streams.
type SystemEditor struct {
	Directory string
	Streams   Streams
}

// Edit preserves the draft on failure; the caller removes it after submission.
func (e SystemEditor) Edit(ctx context.Context, command, body string) (Draft, error) {
	if _, err := editorArgs(command); err != nil {
		return Draft{}, err
	}
	if err := ctx.Err(); err != nil {
		return Draft{}, err
	}
	file, err := os.CreateTemp(e.Directory, "tea-draft-*.md")
	if err != nil {
		return Draft{}, errors.New("cannot create a private editor draft")
	}
	draft := Draft{Path: file.Name(), Body: body}
	_, writeErr := file.WriteString(body)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return draft, errors.New("cannot write editor draft")
	}
	runErr := e.EditPath(ctx, command, draft.Path)
	info, err := os.Lstat(draft.Path)
	if err != nil || !info.Mode().IsRegular() {
		// A failed editor can remove its buffer or replace it with a non-file.
		// Recover the original input separately without following that replacement.
		recovery, recoveryErr := os.CreateTemp(e.Directory, "tea-draft-recovery-*.md")
		if recoveryErr != nil {
			return Draft{Body: body}, errors.Join(ctx.Err(), errors.New("editor draft is unavailable and original input could not be saved"))
		}
		draft.Path = recovery.Name()
		_, writeErr := recovery.WriteString(body)
		closeErr := recovery.Close()
		if errors.Join(writeErr, closeErr) != nil {
			return draft, errors.Join(ctx.Err(), errors.New("cannot save original editor input"))
		}
		return draft, errors.Join(ctx.Err(), errors.New("editor draft is unavailable; original input was saved separately"))
	}
	// Editors may replace the file rather than writing it in place, even on failure.
	if err := os.Chmod(draft.Path, 0o600); err != nil {
		return draft, errors.Join(ctx.Err(), errors.New("cannot restore private draft permissions"))
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return draft, ctx.Err()
		}
		return draft, fmt.Errorf("%w; draft has been preserved", runErr)
	}
	data, err := os.ReadFile(draft.Path)
	if err != nil {
		return draft, errors.New("cannot read edited draft")
	}
	draft.Body = string(data)
	return draft, nil
}

// EditPath opens an existing file in the configured editor and blocks until it exits.
// Like Git, it waits for the editor instead of killing it on cancellation, so the
// editor can restore the terminal.
func (e SystemEditor) EditPath(ctx context.Context, command, path string) error {
	args, err := editorArgs(command)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	process := exec.Command(args[0], append(args[1:], path)...)
	process.Stdin, process.Stdout, process.Stderr = e.Streams.In, e.Streams.ErrOut, e.Streams.ErrOut
	if err := process.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("editor failed: %w", err)
	}
	return ctx.Err()
}

func editorArgs(command string) ([]string, error) {
	args, ok := commandArgs(command)
	if !ok {
		return nil, errors.New("configure an editor command using tea config set editor or EDITOR")
	}
	return args, nil
}

// commandArgs splits a configured command line without a shell. A value that names an
// executable as a whole, such as an unquoted Windows path, is kept intact.
func commandArgs(command string) ([]string, bool) {
	if _, err := exec.LookPath(command); err == nil {
		return []string{command}, true
	}
	args, err := shellquote.Split(command)
	return args, err == nil && len(args) > 0 && args[0] != ""
}
