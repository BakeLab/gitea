// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

// maxDryRunBody bounds the request body a dry run keeps for its report.
const maxDryRunBody = 64 << 10

// maxDryRunRead bounds the response a dry run keeps to answer a later write to the same path.
const maxDryRunRead = 1 << 20

// DryRunRequest is a write request that a dry run withheld.
type DryRunRequest struct {
	Method string          `json:"method"`
	URL    string          `json:"url"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// DryRunRecorder collects the write requests that clients made with DryRun withheld.
type DryRunRecorder struct {
	mu       sync.Mutex
	requests []DryRunRequest
	reads    map[string][]byte
}

// Requests returns the withheld requests in the order they were made.
func (r *DryRunRecorder) Requests() []DryRunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]DryRunRequest{}, r.requests...)
}

// DryRun returns a client that sends reads but records every other request in rec and answers it
// with a success instead of sending it, so that a command runs through to its last write.
func (c *Client) DryRun(rec *DryRunRecorder) *Client {
	return c.wrapTransport(func(rt http.RoundTripper) http.RoundTripper { return dryRunTransport{base: rt, rec: rec} })
}

type dryRunTransport struct {
	base http.RoundTripper
	rec  *DryRunRecorder
}

func (t dryRunTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		resp, err := t.base.RoundTrip(req)
		if err == nil && req.Method == http.MethodGet && resp.StatusCode/100 == 2 {
			t.rec.keepRead(req.URL.Path, resp)
		}
		return resp, err
	}
	record := DryRunRequest{Method: req.Method, URL: RedactSecrets(req.Context(), redactURL(req.URL))}
	if req.Body != nil {
		data, err := io.ReadAll(io.LimitReader(req.Body, maxDryRunBody+1))
		_ = req.Body.Close()
		if body := []byte(RedactSecrets(req.Context(), string(data))); err == nil && len(data) <= maxDryRunBody && json.Valid(body) {
			record.Body = body
		}
	}
	t.rec.mu.Lock()
	defer t.rec.mu.Unlock()
	t.rec.requests = append(t.rec.requests, record)
	return t.rec.answer(req), nil
}

// keepRead keeps the JSON object of a read response, leaving resp readable.
func (r *DryRunRecorder) keepRead(path string, resp *http.Response) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDryRunRead+1))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), resp.Body), resp.Body}
	var object map[string]json.RawMessage
	if err != nil || len(data) > maxDryRunRead || json.Unmarshal(data, &object) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reads == nil {
		r.reads = map[string][]byte{}
	}
	r.reads[path] = data
}

// answer is the success of a withheld write. An edit answers with the resource as it was read, whose
// other fields a command goes on to use; r.mu is held.
func (r *DryRunRecorder) answer(req *http.Request) *http.Response {
	status, body := http.StatusOK, []byte("{}")
	switch req.Method {
	case http.MethodDelete:
		status, body = http.StatusNoContent, nil
	case http.MethodPost:
		status = http.StatusCreated
	default:
		if read, ok := r.reads[req.URL.Path]; ok {
			body = read
		}
	}
	return &http.Response{
		Status: http.StatusText(status), StatusCode: status, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)), Request: req,
	}
}
