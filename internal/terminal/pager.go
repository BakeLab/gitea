// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Pager renders multi-line human output through an external command.
type Pager interface {
	Page(context.Context, string, io.Reader, Streams) error
}

// SystemPager runs the pager without a shell and routes diagnostics to stderr.
type SystemPager struct{}

// Page validates the command and feeds content to the pager stdin. The pager
// handles interrupts itself; killing it on cancellation would leave the terminal raw.
func (SystemPager) Page(ctx context.Context, command string, input io.Reader, streams Streams) error {
	args, ok := commandArgs(command)
	if !ok {
		return errors.New("pager command contains malformed quoted arguments")
	}
	process := exec.Command(args[0], args[1:]...)
	process.Stdin = input
	process.Stdout = streams.Out
	process.Stderr = streams.ErrOut
	env := os.Environ()
	if os.Getenv("LESS") == "" {
		env = append(env, "LESS=FRX")
	}
	process.Env = env
	if err := process.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("pager failed; configure pager or PAGER: %w", err)
	}
	return nil
}

// WriteHuman writes human text directly, or through a pager when stdout is a TTY,
// a pager command is configured, and the content spans multiple lines.
// Prompting policy such as --no-input does not affect paging.
func WriteHuman(ctx context.Context, streams Streams, command string, pager Pager, text string) error {
	if !shouldPage(streams, command, text) {
		_, err := io.WriteString(streams.Out, text)
		return err
	}
	if pager == nil {
		pager = SystemPager{}
	}
	return pager.Page(ctx, command, strings.NewReader(text), streams)
}

func shouldPage(streams Streams, command, text string) bool {
	if !streams.OutTTY || command == "" || command == "cat" {
		return false
	}
	trimmed := strings.TrimSuffix(text, "\n")
	return strings.Contains(trimmed, "\n")
}
