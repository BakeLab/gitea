// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package transport provides scoped, cancellable Gitea API requests.
package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
	"unicode"

	"gitea.dev/cli/internal/version"
)

// RequestSigner attaches HTTP signature headers for SSH/HTTPSign authentication.
type RequestSigner interface {
	SignRequest(r *http.Request, contents []byte) error
}

// APIError reports request failure without including credentials or raw response bodies.
// Message holds only the sanitized "message" field of a JSON error body.
type APIError struct {
	Status                    int
	Method, Endpoint, Message string
	// Instance is the installation URL the failed request was sent to, when it was recorded
	// from a request.
	Instance string
}

func (e *APIError) Error() string {
	var text string
	switch e.Status {
	case 401:
		text = "authentication rejected; run gt auth login"
	case 403:
		text = "permission denied by the Gitea instance"
	case 404:
		// Gitea's 404 message is usually just "not found"; the request names what was missing.
		text = "not found (" + e.request() + ")"
		if strings.EqualFold(e.Message, "not found") {
			return text
		}
	case 429:
		text = "Gitea rate limit exceeded; try again later"
	default:
		text = fmt.Sprintf("Gitea API returned HTTP %d for %s %s", e.Status, e.Method, e.Endpoint)
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// request describes the failed request, such as "HTTP 404 GET /api/v1/repos/alice/x".
func (e *APIError) request() string {
	return strings.TrimSpace(fmt.Sprintf("HTTP %d %s %s", e.Status, e.Method, e.Endpoint))
}

// bind records the instance of the request to u.
func (e *APIError) bind(u *url.URL) {
	if prefix, _, ok := strings.Cut(u.Path, "/api/v1/"); ok {
		e.Instance = (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: prefix}).String()
	}
}

const (
	maxErrorBody    = 4 << 10
	maxErrorMessage = 300
)

// newAPIError keeps only a bounded, printable JSON "message" field of a failed response body.
// Authentication failures and messages echoing the token keep no server text.
func newAPIError(status int, method, endpoint, token string, data []byte) *APIError {
	failure := &APIError{Status: status, Method: method, Endpoint: endpoint}
	if status == http.StatusUnauthorized {
		return failure
	}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) != nil || (token != "" && strings.Contains(body.Message, token)) {
		return failure
	}
	message := strings.Join(strings.FieldsFunc(body.Message, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) }), " ")
	if runes := []rune(message); len(runes) > maxErrorMessage {
		message = string(runes[:maxErrorMessage]) + "..."
	}
	failure.Message = message
	return failure
}

// Client binds HTTP requests to one instance and credential.
type Client struct {
	BaseURL string
	Token   string
	// Sudo asks the instance to perform requests as this user (administrators only).
	Sudo    string
	Signer  RequestSigner
	HTTP    *http.Client
	Retries int
}

// UserAgent identifies gt to instances, proxies, and server logs.
var UserAgent = "gt/" + version.Current() + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"

type userAgentTransport struct{ base http.RoundTripper }

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent)
	}
	return t.base.RoundTrip(req)
}

// New constructs an instance-scoped client. Its HTTP client follows no redirects itself; API
// requests follow only those within the instance API. Only connection setup and the wait for
// response headers are bounded, so slow but progressing transfers are not cut off.
func New(instance, token string, insecure bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 120 * time.Second
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} // user-controlled, instance-scoped
	return &Client{BaseURL: instance, Token: token, Retries: 2, HTTP: &http.Client{Transport: userAgentTransport{tr}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

// WithTimeout returns an independent HTTP policy while sharing the connection transport.
func (c *Client) WithTimeout(timeout time.Duration) *Client {
	clone := *c
	httpClient := *c.HTTP
	httpClient.Timeout = timeout
	clone.HTTP = &httpClient
	return &clone
}

// scoped parses raw for this instance, rejecting userinfo, fragments, dot
// segments, and backslashes. kind names the reference in error messages.
func (c *Client) scoped(raw, kind string) (*url.URL, *url.URL, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, nil, errors.New("invalid instance URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return nil, nil, errors.New("invalid " + kind)
	}
	for segment := range strings.SplitSeq(u.Path, "/") {
		if segment == ".." || segment == "." {
			return nil, nil, errors.New(kind + " cannot contain path traversal")
		}
	}
	if strings.Contains(u.Path, "\\") {
		return nil, nil, errors.New(kind + " cannot contain backslashes")
	}
	return base, u, nil
}

// within reports whether u uses the instance scheme and host and lies below its base path plus under.
func within(base, u *url.URL, under string) bool {
	return u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) && strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+under)
}

// URL accepts only endpoints within the selected installation API.
func (c *Client) URL(endpoint string) (string, error) {
	base, u, err := c.scoped(endpoint, "API endpoint")
	if err != nil {
		return "", err
	}
	if u.IsAbs() || u.Host != "" {
		if !within(base, u, "/api/") {
			return "", errors.New("API URL must belong to the selected instance and its API base path")
		}
		return u.String(), nil
	}
	endpointPath := u.Path
	escapedEndpointPath := u.EscapedPath()
	if !strings.HasPrefix(endpointPath, "/api/") {
		endpointPath = "/api/v1/" + strings.TrimLeft(endpointPath, "/")
		escapedEndpointPath = "/api/v1/" + strings.TrimLeft(escapedEndpointPath, "/")
	}
	base.RawPath = strings.TrimRight(base.EscapedPath(), "/") + escapedEndpointPath
	base.Path = strings.TrimRight(base.Path, "/") + endpointPath
	base.RawQuery = u.RawQuery
	return base.String(), nil
}

// RequestFailure reports a request that received no HTTP response, such as a refused
// connection or an untrusted certificate.
type RequestFailure struct {
	// Action names what failed, such as "API request failed".
	Action string
	// Server is the scheme and host the request was sent to, without path or query.
	Server string
	// Err is the network or TLS cause.
	Err error
}

func (e *RequestFailure) Error() string { return e.Action + ": " + e.Err.Error() }
func (e *RequestFailure) Unwrap() error { return e.Err }

// RequestError keeps the network or TLS cause of a failed request. The cause of a *url.Error
// omits the request URL, whose query may carry secrets.
func RequestError(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		failure := &RequestFailure{Action: action, Err: urlErr.Err}
		if u, parseErr := url.Parse(urlErr.URL); parseErr == nil {
			failure.Server = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
		}
		return failure
	}
	return errors.New(action + "; check connectivity and TLS settings")
}

// Do executes a cancellable request, retrying only safe body-free reads.
// A response outside 2xx is closed and returned as an *APIError.
func (c *Client) Do(ctx context.Context, method, endpoint string, body io.Reader, headers http.Header) (*http.Response, error) {
	ctx, failure := CaptureErrors(ctx)
	resp, err := c.Send(ctx, method, endpoint, body, headers)
	if err != nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return resp, err
	}
	_ = resp.Body.Close()
	return nil, failure()
}

// Send is Do without converting responses outside 2xx into errors.
func (c *Client) Send(ctx context.Context, method, endpoint string, body io.Reader, headers http.Header) (*http.Response, error) {
	requestURL, err := c.URL(endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, errors.New("cannot construct API request")
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := c.API().Do(req)
	if err != nil {
		return nil, RequestError(ctx, "API request failed", err)
	}
	return resp, nil
}
