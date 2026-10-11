// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type debugTransport struct {
	base http.RoundTripper
	out  io.Writer
}

func (t debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	var outcome string
	if err != nil {
		outcome = "error: " + err.Error()
	} else {
		outcome = resp.Status
	}
	_, _ = fmt.Fprintf(t.out, "[debug] %s %s -> %s (%s)\n", req.Method, redactURL(req.URL), outcome, time.Since(start).Round(time.Millisecond))
	return resp, err
}

// redactURL drops userinfo and query values, which can carry tokens or signatures.
func redactURL(u *url.URL) string {
	clean := *u
	clean.User = nil
	if clean.RawQuery != "" {
		query := clean.Query()
		for key := range query {
			query[key] = []string{"REDACTED"}
		}
		clean.RawQuery = query.Encode()
	}
	return clean.String()
}

// Debug returns a copy of c that logs the method, redacted URL, status, and duration of each
// request to w. Headers and bodies are never logged.
func (c *Client) Debug(w io.Writer) *Client {
	clone := *c
	httpClient := *c.HTTP
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = debugTransport{base: base, out: w}
	clone.HTTP = &httpClient
	return &clone
}
