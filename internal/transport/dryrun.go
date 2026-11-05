// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
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
			record.Body = maskSecrets(req.URL.Path, body)
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

// answer is the success of a withheld write, with the status that Gitea answers that endpoint with.
// An edit answers with the resource as it was read, whose other fields a command goes on to use;
// r.mu is held.
func (r *DryRunRecorder) answer(req *http.Request) *http.Response {
	status, body := successStatus(req.Method, req.URL.Path), []byte("{}")
	switch {
	case status == http.StatusNoContent:
		body = nil
	case answersWithList(req.Method, req.URL.Path):
		body = []byte("[]")
	case req.Method != http.MethodPost && req.Method != http.MethodDelete:
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

// answersWithList tells the writes whose answer the SDK reads as a list: the labels of an issue and
// the threads that a notification change marked.
func answersWithList(method, path string) bool {
	path = apiPath(path)
	last := path[strings.LastIndex(path, "/")+1:]
	return (method == http.MethodPut && last == "notifications") || ((method == http.MethodPut || method == http.MethodPost) && last == "labels" && strings.Contains(path, "/issues/"))
}

// secretKeyWords are the parts of a JSON key whose value a dry run does not print: what Gitea or a
// webhook takes as a password, a token, a key or a signing secret. A plain "key" is left out, as an
// SSH key is public.
var secretKeyWords = []string{"password", "passwd", "secret", "token", "credential", "private_key", "api_key", "apikey", "authorization"}

func secretKey(key string) bool {
	key = strings.ToLower(key)
	return slices.ContainsFunc(secretKeyWords, func(word string) bool { return strings.Contains(key, word) })
}

// maskSecrets replaces the secret values of a JSON request body with [REDACTED], which also holds for the
// "data" of an Actions secret.
func maskSecrets(path string, body []byte) []byte {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // so that the report shows the number that would be sent
	if decoder.Decode(&value) != nil {
		return body
	}
	var mask func(value any, secretData bool) any
	mask = func(value any, secretData bool) any {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if item != nil && item != "" && (secretKey(key) || (secretData && key == "data")) {
					v[key] = "[REDACTED]"
					continue
				}
				v[key] = mask(item, secretData)
			}
		case []any:
			for i, item := range v {
				v[i] = mask(item, secretData)
			}
		case string:
			// A webhook's URL can hold a token in its query.
			if u, err := url.Parse(v); err == nil && u.Host != "" && u.RawQuery != "" {
				return redactURL(u)
			}
		}
		return value
	}
	var masked bytes.Buffer
	encoder := json.NewEncoder(&masked)
	encoder.SetEscapeHTML(false) // so that the report shows the text that would be sent
	if encoder.Encode(mask(value, strings.Contains(path, "/actions/secrets/"))) != nil {
		return body
	}
	return bytes.TrimSpace(masked.Bytes())
}
