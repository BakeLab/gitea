// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// Browser opens an explicitly requested resource using a configured command.
type Browser interface {
	Open(context.Context, string, string) error
}

// SystemBrowser runs the browser without a shell and routes diagnostics to stderr.
type SystemBrowser struct{ ErrOut io.Writer }

// Open validates the resource and invokes the selected browser directly.
func (b SystemBrowser) Open(ctx context.Context, resource, command string) error {
	u, err := url.Parse(resource)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.ContainsAny(resource, "\x00\r\n") {
		return errors.New("browser requires an HTTP or HTTPS resource URL without credentials")
	}
	var args []string
	if command == "" || command == "auto" {
		switch runtime.GOOS {
		case "darwin":
			args = []string{"open"}
		case "windows":
			args = []string{"rundll32", "url.dll,FileProtocolHandler"}
		default:
			args = []string{"xdg-open"}
		}
	} else {
		if command == "never" {
			return errors.New("browser opening is disabled; configure browser or BROWSER")
		}
		var ok bool
		if args, ok = commandArgs(command); !ok {
			return errors.New("browser command contains malformed quoted arguments")
		}
	}
	process := exec.CommandContext(ctx, args[0], append(args[1:], resource)...)
	process.Stdout, process.Stderr = b.ErrOut, b.ErrOut
	if err := process.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("browser could not be opened; configure browser or BROWSER: %w", err)
	}
	return nil
}
