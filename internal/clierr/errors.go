// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package clierr

import (
	"context"
	"errors"
)

// Public exit codes for errors returned by commands.
const (
	Failure        = 1
	Usage          = 2
	Canceled       = 3
	Authentication = 4
	// Unsuccessful reports a run that completed without success, unlike a Failure of gt itself.
	Unsuccessful = 5
	// HeadMismatch reports a run for another commit than gt run watch --expect-head names.
	HeadMismatch = 6
	// Pending reports checks that have not finished, like gh pr checks.
	Pending     = 8
	Interrupted = 130
)

// errReported is the message of Quiet errors.
var errReported = errors.New("the command reported the outcome")

// Quiet exits with code without an error message, for an outcome the command already reported,
// such as failing checks.
func Quiet(code int) error {
	return &Error{Code: code, Err: errReported}
}

// IsQuiet reports whether err is a Quiet exit code.
func IsQuiet(err error) bool {
	return errors.Is(err, errReported)
}

// Error associates an operation error with a public CLI exit code.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Wrap preserves the underlying error while assigning an exit code.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// ExitCode translates returned errors into the executable exit contract.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	typed, ok := errors.AsType[*Error](err)
	if ok && typed.Code == Interrupted {
		return Interrupted
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Canceled
	}
	if ok {
		return typed.Code
	}
	return Failure
}
