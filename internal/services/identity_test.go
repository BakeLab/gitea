// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/cli/internal/transport"

	"github.com/stretchr/testify/require"
)

func TestCurrentAccountValidatesIdentityAndPropagatesErrors(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"login":"Alice"}`, 200, "Alice"},
		{`{"login":" "}`, 200, ""},
		{`invalid`, 200, ""},
		{`{"message":"do not expose this"}`, 401, ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/base/api/v1/user", r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "token secret", r.Header.Get("Authorization"))
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			account, err := CurrentAccount(context.Background(), transport.New(server.URL+"/base", "secret", false))
			if tc.want != "" {
				require.NoError(t, err)
				require.Equal(t, tc.want, account)
			} else {
				require.Error(t, err)
				require.Empty(t, account)
				require.NotContains(t, err.Error(), "secret")
				require.NotContains(t, err.Error(), "do not expose this")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CurrentAccount(ctx, transport.New("https://example.org", "secret", false))
	require.ErrorIs(t, err, context.Canceled)
}

func TestCurrentAccountAsksOncePerClientAndSudoUser(t *testing.T) {
	requests := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		login := "alice"
		if sudo := r.Header.Get("Sudo"); sudo != "" {
			login = sudo
		}
		_, _ = w.Write([]byte(`{"login":"` + login + `"}`))
	}))
	for range 2 {
		account, err := CurrentAccount(context.Background(), client)
		require.NoError(t, err)
		require.Equal(t, "alice", account)
	}
	client.Sudo = "bob"
	account, err := CurrentAccount(context.Background(), client)
	require.NoError(t, err)
	require.Equal(t, "bob", account)
	require.Equal(t, 2, requests)
}

func TestMatchAccount(t *testing.T) {
	require.True(t, MatchAccount("", "Alice"))
	require.True(t, MatchAccount("alice", "Alice"))
	require.False(t, MatchAccount("bob", "Alice"))
}

func TestTokenScopesAndServerVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/token":
			require.Equal(t, "token secret", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"id":1,"name":"gt","scopes":["write:repository","read:user"]}`))
		case "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"28.1.0"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := transport.New(server.URL, "secret", false)
	scopes, err := TokenScopes(context.Background(), client)
	require.NoError(t, err)
	require.Equal(t, []string{"write:repository", "read:user"}, scopes)
	version, err := ServerVersion(context.Background(), client)
	require.NoError(t, err)
	require.Equal(t, "28.1.0", version)
}
