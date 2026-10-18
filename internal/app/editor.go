// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func finishDraft(draft *terminal.Draft, result *error) {
	if draft.Path == "" {
		return
	}
	if *result == nil {
		if err := os.Remove(draft.Path); err != nil {
			*result = errors.New("operation completed, but its draft could not be removed")
		}
	}
	if *result != nil {
		*result = &draftError{err: *result, path: draft.Path}
	}
}

// draftError reports where the text of a failed or canceled command was saved.
type draftError struct {
	err  error
	path string
}

func (e *draftError) Error() string { return fmt.Sprintf("%v; draft saved at %s", e.err, e.path) }
func (e *draftError) Unwrap() error { return e.err }

// DraftPath returns the file that holds the unsubmitted text of a failed command, or "".
func DraftPath(err error) string {
	if draft, ok := errors.AsType[*draftError](err); ok {
		return draft.path
	}
	return ""
}

func (a *application) editor() (terminal.Editor, string, error) {
	preferences, err := a.preferences()
	if err != nil {
		return nil, "", err
	}
	if preferences.Editor == "" {
		return nil, "", clierr.Wrap(clierr.Usage, errors.New("configure an editor using gt config set editor or EDITOR"))
	}
	return a.deps.Editor, preferences.Editor, nil
}

func (a *application) editDraft(cmd *cobra.Command, body string) (terminal.Draft, error) {
	editor, command, err := a.editor()
	if err != nil {
		return terminal.Draft{}, err
	}
	return editor.Edit(cmd.Context(), command, body)
}

// bodyEditor collects the body of an interactive form after the form ran. The editor is resolved
// first: without one, the body is typed into an inline multi-line field instead, so the input
// already given is never lost to a missing editor.
func (a *application) bodyEditor(cmd *cobra.Command) terminal.BodyEditor {
	preferences, err := a.preferences()
	if err == nil && preferences.Editor == "" {
		return func(ctx context.Context, initial string) (terminal.Draft, error) {
			body, err := a.deps.Prompter.AskText(ctx, terminal.BodyTitle, initial)
			return terminal.Draft{Body: body}, err
		}
	}
	return func(_ context.Context, initial string) (terminal.Draft, error) { return a.editDraft(cmd, initial) }
}

func (a *application) editPath(cmd *cobra.Command, path string) error {
	editor, command, err := a.editor()
	if err != nil {
		return err
	}
	return editor.EditPath(cmd.Context(), command, path)
}

// readBody checks source conflicts before reading stdin or invoking any service.
func (a *application) readBody(cmd *cobra.Command, body, file string) (string, error) {
	return a.readTextSource(cmd, body, file, "body", "body-file")
}

func (a *application) readTextSource(cmd *cobra.Command, text, file, textFlag, fileFlag string) (string, error) {
	if err := validateTextSources(cmd, textFlag, fileFlag); err != nil {
		return "", err
	}
	if !cmd.Flags().Changed(fileFlag) {
		return text, nil
	}
	data, err := readFileOrStdin(a.deps.Streams.In, file)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", textFlag, err)
	}
	return string(data), nil
}

// readSecret is readTextSource for credentials: one trailing line ending from a file is dropped.
func (a *application) readSecret(cmd *cobra.Command, text, file, textFlag, fileFlag string) (string, error) {
	value, err := a.readTextSource(cmd, text, file, textFlag, fileFlag)
	if cmd.Flags().Changed(fileFlag) {
		value = strings.TrimSuffix(strings.TrimSuffix(value, "\n"), "\r")
	}
	return value, err
}

// readFileOrStdin reads a file, or stdin when path is "-".
func readFileOrStdin(stdin io.Reader, path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

func validateBodySources(cmd *cobra.Command) error {
	return validateTextSources(cmd, "body", "body-file")
}

func validateTextSources(cmd *cobra.Command, textFlag, fileFlag string) error {
	text, file := cmd.Flags().Changed(textFlag), cmd.Flags().Changed(fileFlag)
	if flag := cmd.Flags().Lookup("editor"); flag != nil {
		editor, err := cmd.Flags().GetBool("editor")
		if err != nil {
			return err
		}
		if editor && (text || file) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--editor, --%s, and --%s are mutually exclusive", textFlag, fileFlag))
		}
	}
	if text && file {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s and --%s are mutually exclusive", textFlag, fileFlag))
	}
	if file {
		path, err := cmd.Flags().GetString(fileFlag)
		if err != nil {
			return err
		}
		if path == "" {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s must name a file or -", fileFlag))
		}
	}
	return nil
}
