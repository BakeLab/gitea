// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/tea/internal/transport"
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
	require.True(t, errors.Is(err, context.Canceled))
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
