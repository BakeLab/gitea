// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
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
		// A request canceled for running out of time, by WithResponseTimeout, says so in its cause.
		if cause := context.Cause(req.Context()); cause != nil && !errors.Is(err, cause) {
			outcome = "error: " + cause.Error()
		} else {
			outcome = "error: " + err.Error()
		}
	} else {
		outcome = plainText(resp.Status)
		message = failureMessage(req, resp)
	}
	line := fmt.Sprintf("[debug] %s %s -> %s (%s)", req.Method, redactURL(req.URL), outcome, time.Since(start).Round(time.Millisecond))
	if message != "" {
		line += ": " + message
	}
	_, _ = fmt.Fprintln(t.out, RedactSecrets(req.Context(), line))
	return resp, err
}

// plainText drops control and format characters, which a server can put in the reason phrase
// to drive the terminal the log is printed to.
func plainText(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) }), " ")
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
	return newAPIError(req.Context(), resp.StatusCode, req.Method, req.URL.Path, resp.Header.Get("Content-Type"), token, data).Message
}

type secrets struct{}

// WithSecrets marks ctx with secrets that a request carries in its body, such as source
// credentials of a migration, so that whatever echoes them is redacted with RedactSecrets.
func WithSecrets(ctx context.Context, values ...string) context.Context {
	known, _ := ctx.Value(secrets{}).([]string)
	for _, value := range values {
		if value != "" {
			known = append(known[:len(known):len(known)], value)
		}
	}
	return context.WithValue(ctx, secrets{}, known)
}

// RedactSecrets replaces each secret marked on ctx in message, as is or escaped for a URL query,
// path or userinfo, and the password of each URL with [REDACTED].
func RedactSecrets(ctx context.Context, message string) string {
	known, _ := ctx.Value(secrets{}).([]string)
	for _, secret := range known {
		for _, form := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), url.User(secret).String()} {
			message = strings.ReplaceAll(message, form, "[REDACTED]")
		}
	}
	return urlPassword.ReplaceAllString(message, "${1}[REDACTED]@")
}

// urlPassword matches the user and password of a URL, such as "https://user:password@".
var urlPassword = regexp.MustCompile(`(\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/?#@:]*:)[^\s/?#@]+@`)

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

// RedactHeader returns the value of a response header named name with the URLs it carries, such
// as a Location of signed storage or the pages of a Link header, redacted like the URLs --debug
// logs. Other headers are returned unchanged.
func RedactHeader(name, value string) string {
	redact := func(raw string) string {
		if u, err := url.Parse(raw); err == nil {
			return redactURL(u)
		}
		return raw
	}
	switch http.CanonicalHeaderKey(name) {
	case "Location", "Content-Location":
		return redact(value)
	case "Link":
		return linkTarget.ReplaceAllStringFunc(value, func(target string) string {
			return "<" + redact(target[1:len(target)-1]) + ">"
		})
	}
	return value
}

// linkTarget matches the target URL of a Link header entry, such as "<https://…?page=2>".
var linkTarget = regexp.MustCompile(`<[^>]*>`)

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
// request to w, plus the sanitized server message of a failed response, with the secrets marked
// by WithSecrets redacted. Headers and bodies are never logged.
func (c *Client) Debug(w io.Writer) *Client {
	return c.wrapTransport(func(rt http.RoundTripper) http.RoundTripper { return debugTransport{base: rt, out: w} })
}
