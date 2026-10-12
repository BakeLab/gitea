// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
)

// newTestClient serves handler for the duration of the test and returns a client for it.
// Requests probing the server version or API schema fail the test: gt targets one Gitea
// release and must never gate operations on either.
func newTestClient(t *testing.T, handler http.Handler) *transport.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/version") || strings.HasSuffix(r.URL.Path, "/swagger.v1.json") {
			t.Errorf("unexpected capability probe %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return transport.New(server.URL, "", false)
}

// requestJSON decodes the JSON object a handler received.
func requestJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var payload map[string]any
	assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	return payload
}
