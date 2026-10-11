// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminPromptAndFlagsReachEquivalentPayloads(t *testing.T) {
	var payloads []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"id":1,"login":"account"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	prompt := &scriptedPrompter{input: "account@example.test", secret: " preserved password ", accept: true}
	deps.Prompter = prompt
	deps.Streams.InTTY = true
	deps.Streams.ErrTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"admin", "user", "create", "account", "--full-name", "Test Account"}))
	require.Equal(t, []string{"email"}, prompt.asked)
	require.Len(t, prompt.secrets, 2)
	require.Len(t, prompt.confirmations, 1)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"admin", "user", "create", "account", "--full-name", "Test Account", "--email", "account@example.test", "--password", " preserved password ", "--no-input"}))
	require.Equal(t, payloads[0], payloads[1])
	require.Len(t, prompt.asked, 1)
	require.Len(t, prompt.secrets, 2)
	require.NotContains(t, out.String(), "password")
}
