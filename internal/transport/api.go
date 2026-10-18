// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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
	if len(via) > maxRedirects || req.Method != via[0].Method {
		return http.ErrUseLastResponse
	}
	if _, err := c.URL(req.URL.String()); err != nil {
		return http.ErrUseLastResponse
	}
	return nil
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
	failure := newAPIError(resp.StatusCode, req.Method, req.URL.Path, t.token, data)
	failure.bind(req.URL)
	holder.Store(failure)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), resp.Body), resp.Body}
	return resp, nil
}

type retryTransport struct {
	base    http.RoundTripper
	retries int
}

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	safe := (req.Method == http.MethodGet || req.Method == http.MethodHead) && (req.Body == nil || req.Body == http.NoBody)
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if !safe || attempt >= t.retries || (err == nil && resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusGatewayTimeout) {
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

// signingBody returns the request body to sign, buffering a body that cannot be replayed.
func signingBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	body := req.Body
	if req.GetBody != nil {
		var err error
		if body, err = req.GetBody(); err != nil {
			return nil, errors.New("cannot read API request body for signing")
		}
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil {
		return nil, errors.New("cannot read API request body for signing")
	}
	if req.GetBody == nil {
		req.Body = io.NopCloser(bytes.NewReader(data))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	}
	return data, nil
}
