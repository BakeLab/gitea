// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// API returns an HTTP client for requests to this instance. It attaches the credential and Sudo
// header to every attempt, retries only body-free reads, follows redirects (a renamed or
// transferred repository) only within this instance's API, and records
// the sanitized error of a failed response for CaptureErrors.
func (c *Client) API() *http.Client {
	base := c.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	chain := captureTransport{
		base:  retryTransport{base: authTransport{base: base, token: c.Token, sudo: c.Sudo, signer: c.Signer}, retries: c.Retries},
		token: c.Token,
	}
	return &http.Client{
		Transport:     chain,
		CheckRedirect: c.checkRedirect,
		Jar:           c.HTTP.Jar,
		Timeout:       c.HTTP.Timeout,
	}
}

// maxRedirects bounds the redirects followed for one request.
const maxRedirects = 5

// checkRedirect follows a redirect when it stays within the API of this instance and keeps the
// method, as the 301 of a renamed repository does for a read. Anything else returns the 3xx
// response, so a credential is never sent elsewhere and a POST that Go would turn into a GET is
// not silently dropped.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects || req.Method != via[0].Method || req.Context().Value(noRedirectsKey{}) != nil {
		return http.ErrUseLastResponse
	}
	if _, err := c.URL(req.URL.String()); err != nil {
		return http.ErrUseLastResponse
	}
	return nil
}

type noRedirectsKey struct{}

// WithoutRedirects returns a context under which API requests return a redirect rather than
// follow it.
func WithoutRedirects(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRedirectsKey{}, true)
}

type capturedErrorKey struct{}

// CaptureErrors returns a context under which API clients record the sanitized error of each
// response outside 2xx, and a function returning the last one recorded.
func CaptureErrors(ctx context.Context) (context.Context, func() *APIError) {
	holder := new(atomic.Pointer[APIError])
	return context.WithValue(ctx, capturedErrorKey{}, holder), holder.Load
}

type captureTransport struct {
	base  http.RoundTripper
	token string
}

func (t captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	holder, _ := req.Context().Value(capturedErrorKey{}).(*atomic.Pointer[APIError])
	if err != nil || holder == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return resp, err
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	// The request as sent carries the secrets that the transports below marked on it, such as
	// the values of configured headers.
	sent := req
	if resp.Request != nil {
		sent = resp.Request
	}
	failure := newAPIError(sent.Context(), resp.StatusCode, req.Method, req.URL.Path, resp.Header.Get("Content-Type"), t.token, data)
	failure.bind(req.URL)
	if redirect(resp.StatusCode) {
		failure.Moved = movedRepository(req.URL, resp.Header.Get("Location"))
	}
	holder.Store(failure)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), resp.Body), resp.Body}
	return resp, nil
}

// movedRepository returns the OWNER/REPO that location moves the repository of an API request
// at from to, or "" when location moves no repository.
func movedRepository(from *url.URL, location string) string {
	to, err := from.Parse(location)
	if err != nil || !strings.EqualFold(to.Host, from.Host) {
		return ""
	}
	repository := func(u *url.URL) string {
		_, rest, ok := strings.Cut(u.EscapedPath(), "/api/v1/repos/")
		parts := strings.SplitN(rest, "/", 3)
		if !ok || len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return ""
		}
		owner, ownerErr := url.PathUnescape(parts[0])
		name, nameErr := url.PathUnescape(parts[1])
		if ownerErr != nil || nameErr != nil {
			return ""
		}
		return owner + "/" + name
	}
	old, moved := repository(from), repository(to)
	if old == "" || moved == "" || old == moved {
		return ""
	}
	return moved
}

type retryTransport struct {
	base    http.RoundTripper
	retries int
}

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	safe := (req.Method == http.MethodGet || req.Method == http.MethodHead) && (req.Body == nil || req.Body == http.NoBody)
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if !safe || attempt >= t.retries || (err != nil && !transient(err)) || (err == nil && resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusGatewayTimeout) {
			return resp, err
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

// transient reports whether a request failed in a way that another attempt may not: a refused
// connection or one dropped before the answer. A timeout, which another attempt would only
// multiply, and a TLS or client certificate failure or a host name that does not exist, which
// recur, are not transient.
func transient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return false
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound {
		return false
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
		return true
	}
	return connectionReset(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

type authTransport struct {
	base        http.RoundTripper
	token, sudo string
	signer      RequestSigner
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	signed := req.Clone(req.Context())
	if t.sudo != "" {
		signed.Header.Set("Sudo", t.sudo)
	}
	if t.token != "" {
		signed.Header.Set("Authorization", "token "+t.token)
	} else if t.signer != nil {
		body, err := signingBody(signed)
		if err == nil {
			err = t.signer.SignRequest(signed, body)
		}
		if err != nil {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, err
		}
	}
	return t.base.RoundTrip(signed)
}

// signingBody returns the request body to sign. A body that can be read again is hashed as it is
// read into the SHA-512 Digest header that HTTPSign signs, so that a large upload is not held in
// memory, and nil is returned; any other body is buffered and returned.
func signingBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, errors.New("cannot read API request body for signing")
		}
		digest := sha512.New()
		_, err = io.Copy(digest, body)
		_ = body.Close()
		if err != nil {
			return nil, errors.New("cannot read API request body for signing")
		}
		req.Header.Set("Digest", "SHA-512="+base64.StdEncoding.EncodeToString(digest.Sum(nil)))
		return nil, nil
	}
	data, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, errors.New("cannot read API request body for signing")
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	return data, nil
}
