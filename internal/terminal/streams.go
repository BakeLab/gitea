// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
}

// System binds streams to the current process and detects each terminal.
func System() Streams {
	return Streams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr, InTTY: term.IsTerminal(int(os.Stdin.Fd())), OutTTY: term.IsTerminal(int(os.Stdout.Fd())), ErrTTY: term.IsTerminal(int(os.Stderr.Fd()))}
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

// Prompter abstracts cancellable input collection and confirmation.
type Prompter interface {
	Input(context.Context, string) (string, error)
	SecretInput(context.Context, string) (string, error)
	Confirm(context.Context, string) (bool, error)
}

// FormPrompter uses cancellable terminal forms with explicitly injected streams.
type FormPrompter struct{ Streams Streams }

func promptError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return clierr.Wrap(clierr.Canceled, errors.New("input canceled"))
	}
	return err
}

func (p *FormPrompter) runForm(ctx context.Context, form *huh.Form) error {
	return promptError(ctx, form.WithInput(p.Streams.In).WithOutput(p.Streams.ErrOut).RunWithContext(ctx))
}

// Input collects one field using the configured terminal streams.
func (p *FormPrompter) Input(ctx context.Context, title string) (string, error) {
	var value string
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewInput().Title(title).Value(&value))))
	return strings.TrimSpace(value), err
}

// SecretInput collects a value without displaying it on the terminal.
func (p *FormPrompter) SecretInput(ctx context.Context, title string) (string, error) {
	var value string
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&value))))
	return value, err
}

// Confirm collects a yes/no choice through cancellable terminal input.
func (p *FormPrompter) Confirm(ctx context.Context, title string) (bool, error) {
	var accepted bool
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&accepted))))
	return accepted, err
}

// Require fills missing input or returns an actionable usage error.
func Require(ctx context.Context, value *string, flag string, streams Streams, policy Policy, prompt Prompter) (bool, error) {
	if *value != "" {
		return false, nil
	}
	if !policy.CanPrompt(streams) {
		return false, clierr.Wrap(clierr.Usage, fmt.Errorf("missing required input: supply --%s", flag))
	}
	entered, err := prompt.Input(ctx, flag)
	if err != nil {
		return false, err
	}
	if entered == "" {
		return false, clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be empty", flag))
	}
	*value = entered
	return true, nil
}

// Confirm requires explicit acceptance unless confirmation was supplied by flag.
func Confirm(ctx context.Context, description string, yes bool, streams Streams, policy Policy, prompt Prompter) error {
	if yes {
		return nil
	}
	if !policy.CanPrompt(streams) {
		return clierr.Wrap(clierr.Usage, errors.New("confirmation required: supply --yes"))
	}
	accepted, err := prompt.Confirm(ctx, description)
	if err != nil {
		return err
	}
	if !accepted {
		return clierr.Wrap(clierr.Canceled, errors.New("operation canceled"))
	}
	return nil
}
