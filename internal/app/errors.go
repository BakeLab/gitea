// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/transport"
)

// explainedError replaces the message of err and keeps err reachable for exit codes.
type explainedError struct {
	text string
	err  error
}

func (e *explainedError) Error() string { return e.text }
func (e *explainedError) Unwrap() error { return e.err }

// explain gives err the message text.
func explain(err error, format string, args ...any) error {
	return &explainedError{text: fmt.Sprintf(format, args...), err: err}
}

// Explain words a command failure for people. It adds the next step to refusals Gitea explains,
// and words rejected credentials, unreachable servers and expired timeouts. Exit codes are
// unchanged.
func Explain(ctx context.Context, deps Dependencies, err error) error {
	if err == nil || errors.As(err, new(*explainedError)) {
		return err
	}
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
		return explainAPIError(deps, err, apiErr)
	}
	if failure, ok := errors.AsType[*transport.RequestFailure](err); ok {
		if reason := unreachable(failure.Err); reason != "" && failure.Server != "" {
			return explain(err, "cannot reach %s (%s); check --host or your network", failure.Server, reason)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return explain(err, "timed out before the operation finished")
	}
	return err
}

func explainAPIError(deps Dependencies, err error, apiErr *transport.APIError) error {
	if !explainable(err, apiErr) {
		return err
	}
	if apiErr.Status == http.StatusUnauthorized {
		instance := cmp.Or(apiErr.Instance, "the instance")
		if deps.Getenv != nil && deps.Getenv("GITEA_TOKEN") != "" {
			return explain(err, "GITEA_TOKEN was rejected by %s; fix or unset GITEA_TOKEN", instance)
		}
		return explain(err, "authentication rejected by %s; run gt auth login", instance)
	}
	if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Hint == "" {
		if hint := conflictHint(apiErr, conflict); hint != "" {
			return explain(err, "%s; %s", conflict.Error(), hint)
		}
	}
	return err
}

// explainable reports whether err says no more than the failed request: its text is the API
// error's or a classification of it. Context a caller added ("release deleted, but tag
// deletion failed: ...") is kept, and so are raw responses, such as those of gt api, which
// were not recorded from one of gt's requests.
func explainable(err error, apiErr *transport.APIError) bool {
	if apiErr.Instance == "" {
		return false
	}
	text := err.Error()
	if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Error() == text {
		return true
	}
	if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Error() == text {
		return true
	}
	return apiErr.Error() == text
}

// conflictHint returns the next step for a refused request, or "".
func conflictHint(apiErr *transport.APIError, conflict *services.ConflictError) string {
	number := strings.TrimPrefix(conflict.Key, "#")
	switch conflict.Reason {
	case services.ReasonArchived:
		if conflict.Scope != "" {
			return "unarchive it with: gt repo unarchive " + conflict.Scope
		}
	case services.ReasonExists:
		if conflict.Kind == "wiki page" {
			return "edit it with: gt wiki edit " + conflict.Key
		}
	case services.ReasonMerged:
		if apiErr.Method == http.MethodPatch {
			return "its state can no longer change"
		}
	case services.ReasonDraft:
		if number != conflict.Key {
			return "mark it ready with: gt pr ready " + number
		}
	case services.ReasonOwnReview:
		return "leave a comment instead"
	case services.ReasonNoChanges:
		return "check out your feature branch or pass --head"
	case services.ReasonDisabled:
		if conflict.Key != "" {
			return "enable it with: gt workflow enable " + conflict.Key
		}
	case services.ReasonNotDone:
		if conflict.Kind == "run" && conflict.Key != "" {
			return "cancel it first: gt run cancel " + conflict.Key
		}
	case services.ReasonOwnsRepos:
		return "transfer or delete them first"
	}
	return ""
}

// unreachable words a failure to connect, or returns "" for other request failures.
func unreachable(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return "no such host"
	case errors.As(err, &opErr) && opErr.Op == "dial" && opErr.Timeout():
		return "connection timed out"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return opErr.Err.Error()
	}
	return ""
}

// timeoutError words an expired --timeout as "timed out after 4s waiting for run 34" instead
// of "context deadline exceeded". Other errors are returned unchanged; the result still matches
// context.DeadlineExceeded, so the exit code stays the same.
func timeoutError(err error, d time.Duration, format string, args ...any) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	text := "timed out after " + shortDuration(d)
	if format != "" {
		text += " " + fmt.Sprintf(format, args...)
	}
	return &explainedError{text: text, err: err}
}

// shortDuration formats d without zero units, such as 1h, 1h30m or 45s.
func shortDuration(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}
