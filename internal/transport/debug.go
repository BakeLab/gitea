// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type debugTransport struct {
	base http.RoundTripper
	out  io.Writer
}

func (t debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	var outcome, message string
	if err != nil {
		outcome = "error: " + err.Error()
	} else {
		outcome = resp.Status
		message = failureMessage(req, resp)
	}
	line := fmt.Sprintf("[debug] %s %s -> %s (%s)", req.Method, redactURL(req.URL), outcome, time.Since(start).Round(time.Millisecond))
	if message != "" {
		line += ": " + message
	}
	_, _ = fmt.Fprintln(t.out, line)
	return resp, err
}

// failureMessage returns the sanitized server message of a failed response and keeps its body
// readable for the caller.
func failureMessage(req *http.Request, resp *http.Response) string {
	if resp.StatusCode < 400 {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), resp.Body), resp.Body}
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "token ")
	return newAPIError(resp.StatusCode, req.Method, req.URL.Path, token, data).Message
}

// redactURL drops userinfo and the values of query parameters that can carry credentials or
// signatures, keeping filters such as limit, page and state readable.
func redactURL(u *url.URL) string {
	clean := *u
	clean.User = nil
	if clean.RawQuery != "" {
		query := clean.Query()
		for key := range query {
			if sensitiveQuery(clean.Path, key) {
				query[key] = []string{"REDACTED"}
			}
		}
		clean.RawQuery = query.Encode()
	}
	return clean.String()
}

// sensitiveQuery reports query parameters whose values can authenticate a request: tokens,
// passwords, secrets, signatures and credentials of signed storage URLs, and OAuth codes and state.
func sensitiveQuery(path, key string) bool {
	key = strings.ToLower(key)
	switch {
	case key == "code" || key == "key" || strings.HasPrefix(key, "sig"):
		return true
	case key == "state":
		return strings.Contains(path, "/login/oauth/")
	}
	for _, word := range []string{"token", "password", "secret", "signature", "credential"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	return false
}

// Debug returns a copy of c that logs the method, redacted URL, status, and duration of each
// request to w, plus the sanitized server message of a failed response. Headers and bodies are
// never logged.
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
