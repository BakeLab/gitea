// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gitea.dev/cli/internal/transport"
)

// pollOutage bounds how long a poll loop, such as a watch of checks or of a run, polls on
// through failures that a later poll may not meet, such as an instance restart or a dropped
// connection, before it reports the failure.
var pollOutage = 5 * time.Minute

// outage tracks the failed polls of a poll loop.
type outage struct {
	// reached is set once a request of the loop succeeded.
	reached bool
	// since is when the polls started failing, or zero while they pass.
	since time.Time
}

// retry records the outcome err of a request of a poll and reports whether the loop polls again
// at its next interval: err is a failure that a later poll may not meet, an earlier request
// succeeded, and the polls started failing less than pollOutage ago. Other failures, such as a
// 4xx answer or a failure of the first request, fail the loop at once.
func (o *outage) retry(ctx context.Context, err error) bool {
	if err == nil {
		o.reached = true
		return false
	}
	if !o.reached || ctx.Err() != nil || !passingFailure(err) {
		return false
	}
	if o.since.IsZero() {
		o.since = time.Now()
	}
	return time.Since(o.since) < pollOutage
}

// passed records a poll whose every request succeeded, which ends an outage.
func (o *outage) passed() { o.since = time.Time{} }

// passingFailure reports whether err is a failure that a later poll may not meet: no answer
// from the instance, or a 502, 503 or 504 from a proxy in front of it.
func passingFailure(err error) bool {
	if _, ok := errors.AsType[*transport.RequestFailure](err); ok {
		return true
	}
	apiErr, ok := errors.AsType[*transport.APIError](err)
	return ok && apiErr.Storage == "" && (apiErr.Status == http.StatusBadGateway || apiErr.Status == http.StatusServiceUnavailable || apiErr.Status == http.StatusGatewayTimeout)
}
