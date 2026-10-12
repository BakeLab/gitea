// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package clierr

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExitCodeDistinguishesCancellationAndInterruption(t *testing.T) {
	for _, test := range []struct {
		err  error
		code int
	}{
		{nil, 0},
		{errors.New("operation failed"), Failure},
		{Wrap(Usage, errors.New("missing input")), Usage},
		{Wrap(Authentication, errors.New("login required")), Authentication},
		{context.Canceled, Canceled},
		{context.DeadlineExceeded, Canceled},
		{Wrap(Authentication, context.Canceled), Canceled},
		{fmt.Errorf("draft preserved: %w", context.DeadlineExceeded), Canceled},
		{Wrap(Interrupted, context.Canceled), Interrupted},
		{errors.Join(errors.New("partial failure"), context.Canceled), Canceled},
		{Quiet(Pending), Pending},
		{fmt.Errorf("checks: %w", Quiet(Failure)), Failure},
	} {
		assert.Equal(t, test.code, ExitCode(test.err), "%v", test.err)
	}
}
