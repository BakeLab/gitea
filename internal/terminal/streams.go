// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"gitea.dev/cli/internal/clierr"
	"golang.org/x/term"
)

// Streams records injected IO and independently detected terminal capabilities.
type Streams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
	InTTY  bool
	OutTTY bool
	ErrTTY bool
	// Width is the number of columns human output on Out fits in; 0 means unlimited.
	Width int
}

// System binds streams to the current process and detects each terminal.
func System() Streams {
	return Streams{
		In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr,
		InTTY: term.IsTerminal(int(os.Stdin.Fd())), OutTTY: term.IsTerminal(int(os.Stdout.Fd())), ErrTTY: term.IsTerminal(int(os.Stderr.Fd())),
		Width: terminalWidth(int(os.Stdout.Fd()), os.Getenv),
	}
}

// terminalWidth returns the width of the terminal at fd, then $COLUMNS, then 80. A pty without a
// window size, such as `script` run without a terminal, reports a width of 0.
func terminalWidth(fd int, getenv func(string) string) int {
	if width, _, err := term.GetSize(fd); err == nil && width > 0 {
		return width
	}
	if width, err := strconv.Atoi(getenv("COLUMNS")); err == nil && width > 0 {
		return width
	}
	return 80
}

// Policy controls input interaction independently of supplied flag count.
type Policy struct{ Enabled, NoInput, Machine, Interactive bool }

// CanPrompt requires terminal input and diagnostics, without requiring terminal stdout.
func (p Policy) CanPrompt(s Streams) bool {
	return p.Enabled && !p.NoInput && (!p.Machine || p.Interactive) && s.InTTY && s.ErrTTY
}

// Validate rejects an explicit interactive request incompatible with terminal policy.
func (p Policy) Validate(s Streams) error {
	if p.Interactive && !p.CanPrompt(s) {
		return clierr.Wrap(clierr.Usage, errors.New("--interactive requires terminal stdin/stderr, enabled prompting, and no --no-input"))
	}
	return nil
}

// ErrCanceled reports a declined confirmation or an aborted prompt. It exits with code 3 and is
// meant to be reported as "Canceled." rather than as an error.
var ErrCanceled = clierr.Wrap(clierr.Canceled, errors.New("canceled"))

// Prompter abstracts cancellable input collection and confirmation.
type Prompter interface {
	Input(context.Context, string) (string, error)
	SecretInput(context.Context, string) (string, error)
	Confirm(context.Context, string) (bool, error)
}

// Asker is the richer prompt API behind ConfirmAction and RequireInput: inline validation,
// multi-line text and confirmations with a default answer. FormPrompter implements it; a
// Prompter without it gets plain Input and Confirm behavior.
type Asker interface {
	// Ask collects one line, re-prompting inline until validate accepts it.
	Ask(ctx context.Context, title string, validate func(string) error) (string, error)
	// AskText collects multi-line markdown, starting from initial.
	AskText(ctx context.Context, title, initial string) (string, error)
	// ConfirmDefault asks a yes/no question that Enter answers with defaultYes.
	ConfirmDefault(ctx context.Context, title string, defaultYes bool) (bool, error)
}

// FormPrompter uses cancellable terminal forms with explicitly injected streams.
type FormPrompter struct {
	Streams Streams
	// size reports the size of the terminal that forms render on; nil queries ErrOut.
	size func() (width, height int, err error)
}

func promptError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCanceled
	}
	return err
}

// errNoTerminalSize refuses forms on a terminal without a size, such as a pty that `script` opens
// without a terminal. Forms size their fields from it, and a zero size panics in bubbles; nobody
// could see the form there anyway.
var errNoTerminalSize = clierr.Wrap(clierr.Usage, errors.New("terminal size unknown; pass the values as flags or use --no-input"))

func (p *FormPrompter) runForm(ctx context.Context, form *huh.Form) error {
	size := p.size
	if file, ok := p.Streams.ErrOut.(*os.File); ok && size == nil {
		size = func() (int, int, error) { return term.GetSize(int(file.Fd())) }
	}
	if size != nil {
		if width, height, err := size(); err == nil && (width <= 0 || height <= 0) {
			return errNoTerminalSize
		}
	}
	return promptError(ctx, form.WithInput(p.Streams.In).WithOutput(p.Streams.ErrOut).RunWithContext(ctx))
}

// Input collects one field using the configured terminal streams.
func (p *FormPrompter) Input(ctx context.Context, title string) (string, error) {
	return p.Ask(ctx, title, nil)
}

// Ask collects one line, re-prompting inline until validate accepts the trimmed answer.
func (p *FormPrompter) Ask(ctx context.Context, title string, validate func(string) error) (string, error) {
	var value string
	input := huh.NewInput().Title(title).Value(&value)
	if validate != nil {
		input = input.Validate(func(s string) error { return validate(strings.TrimSpace(s)) })
	}
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(input)))
	return strings.TrimSpace(value), err
}

// AskText collects multi-line markdown in an inline field, starting from initial.
func (p *FormPrompter) AskText(ctx context.Context, title, initial string) (string, error) {
	value := initial
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(bodyField(title, &value))))
	return value, err
}

// SecretInput collects a value without displaying it on the terminal.
func (p *FormPrompter) SecretInput(ctx context.Context, title string) (string, error) {
	var value string
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&value))))
	return value, err
}

// Confirm asks a yes/no question that Enter answers with no.
func (p *FormPrompter) Confirm(ctx context.Context, title string) (bool, error) {
	return p.ConfirmDefault(ctx, title, false)
}

// ConfirmDefault asks a yes/no question that Enter answers with defaultYes.
func (p *FormPrompter) ConfirmDefault(ctx context.Context, title string, defaultYes bool) (bool, error) {
	accepted := defaultYes
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&accepted))))
	return accepted, err
}

// RequireInput fills a missing value from a prompt titled label, re-prompting inline until the
// answer is non-empty and validate, when set, accepts it. When prompts are unavailable it returns
// a usage error naming --flag. It reports whether it prompted.
func RequireInput(ctx context.Context, value *string, flag, label string, validate func(string) error, streams Streams, policy Policy, prompt Prompter) (bool, error) {
	if *value != "" {
		return false, nil
	}
	if !policy.CanPrompt(streams) {
		return false, clierr.Wrap(clierr.Usage, fmt.Errorf("missing required input: supply --%s", flag))
	}
	var entered string
	var err error
	if asker, ok := prompt.(Asker); ok {
		entered, err = asker.Ask(ctx, label, func(s string) error {
			if s == "" {
				return errors.New("a value is required")
			}
			if validate != nil {
				return validate(s)
			}
			return nil
		})
	} else if entered, err = prompt.Input(ctx, label); err == nil {
		switch {
		case entered == "":
			err = clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be empty", flag))
		case validate != nil:
			if invalid := validate(entered); invalid != nil {
				err = clierr.Wrap(clierr.Usage, fmt.Errorf("--%s: %w", flag, invalid))
			}
		}
	}
	if err != nil {
		return false, err
	}
	*value = entered
	return true, nil
}

// Require is RequireInput titled by the flag name, for callers without a human label.
func Require(ctx context.Context, value *string, flag string, streams Streams, policy Policy, prompt Prompter) (bool, error) {
	return RequireInput(ctx, value, flag, flag, nil, streams, policy, prompt)
}

// Confirmation describes the question asked before an action.
type Confirmation struct {
	// Prompt asks about the action in sentence case: `Delete label "bug" from alice/x?`.
	Prompt string
	// DefaultYes lets Enter accept, for constructive actions the user just asked for, such as
	// submitting a form they filled in. Destructive actions keep the default no.
	DefaultYes bool
	// Typed, when set, must be typed to confirm, for irreversible actions such as deleting a repository.
	Typed string
	// NonTTY names the action in the error returned when no prompt is possible and --yes is
	// missing: `delete label "bug" from alice/x`.
	NonTTY string
}

// ConfirmAction asks c unless yes is set. A declined or aborted confirmation returns ErrCanceled.
func ConfirmAction(ctx context.Context, c Confirmation, yes bool, streams Streams, policy Policy, prompt Prompter) error {
	if yes {
		return nil
	}
	if !policy.CanPrompt(streams) {
		action := ""
		if c.NonTTY != "" {
			action = " to " + c.NonTTY
		}
		return clierr.Wrap(clierr.Usage, fmt.Errorf("confirmation required%s; rerun with --yes", action))
	}
	asker, rich := prompt.(Asker)
	if c.Typed != "" {
		title := c.Prompt + " Type " + c.Typed + " to confirm:"
		matches := func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), c.Typed) }
		if rich {
			_, err := asker.Ask(ctx, title, func(s string) error {
				if !matches(s) {
					return fmt.Errorf("type %s to confirm", c.Typed)
				}
				return nil
			})
			return err
		}
		typed, err := prompt.Input(ctx, title)
		if err == nil && !matches(typed) {
			err = ErrCanceled
		}
		return err
	}
	var accepted bool
	var err error
	if rich {
		accepted, err = asker.ConfirmDefault(ctx, c.Prompt, c.DefaultYes)
	} else {
		accepted, err = prompt.Confirm(ctx, c.Prompt)
	}
	if err == nil && !accepted {
		err = ErrCanceled
	}
	return err
}

// Confirm requires explicit acceptance unless confirmation was supplied by flag.
// It is ConfirmAction for callers that predate Confirmation.
func Confirm(ctx context.Context, description string, yes bool, streams Streams, policy Policy, prompt Prompter) error {
	return ConfirmAction(ctx, Confirmation{Prompt: description}, yes, streams, policy, prompt)
}
