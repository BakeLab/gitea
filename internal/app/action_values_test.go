// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecretValueFileDropsTrailingLineEnding(t *testing.T) {
	var stored string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/actions/secrets/BUILD_TOKEN":
			var body struct{ Data string }
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			stored = body.Data
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.In = strings.NewReader("sensitive\r\n")
	require.NoError(t, Execute(context.Background(), deps, []string{"secret", "set", "BUILD_TOKEN", "-R", "alice/project", "--value-file", "-", "--yes"}))
	require.Equal(t, "sensitive", stored)
}
