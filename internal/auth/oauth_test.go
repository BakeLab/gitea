// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestOAuthLoginPKCEAndState(t *testing.T) {
	var redirect, challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/base/login/oauth/access_token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "authorization_code", r.Form.Get("grant_type"))
		require.Equal(t, "valid-code", r.Form.Get("code"))
		require.NotEmpty(t, r.Form.Get("code_verifier"))
		require.Equal(t, redirect, r.Form.Get("redirect_uri"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer server.Close()
	credential, err := OAuthLogin(context.Background(), OAuthOptions{Instance: server.URL + "/base", ClientID: "configured-client", HTTP: server.Client(), Timeout: time.Second, Authorize: func(ctx context.Context, raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		require.Equal(t, "configured-client", u.Query().Get("client_id"))
		require.Equal(t, "S256", u.Query().Get("code_challenge_method"))
		redirect = u.Query().Get("redirect_uri")
		callback, err := url.Parse(redirect)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", callback.Hostname())
		// Gitea's built-in gt application registers only the bare loopback URL.
		require.Empty(t, callback.Path)
		challenge = u.Query().Get("code_challenge")
		require.NotEmpty(t, challenge)
		// An unrelated callback cannot consume or satisfy the authorization flow.
		response, err := http.Get(redirect + "?code=invalid&state=wrong")
		if err != nil {
			return err
		}
		response.Body.Close()
		require.Equal(t, 400, response.StatusCode)
		response, err = http.Get(redirect + "?code=valid-code&state=" + url.QueryEscape(u.Query().Get("state")))
		if err != nil {
			return err
		}
		response.Body.Close()
		return nil
	}})
	require.NoError(t, err)
	require.Equal(t, "access", credential.AccessToken)
	require.Equal(t, "refresh", credential.RefreshToken)
}

func TestRefreshSerializesAndRetainsRefreshToken(t *testing.T) {
	config := settings.New(t.TempDir())
	keyring := &memoryKeyring{values: map[string]string{}}
	credentials := &Store{Dir: config.Dir, Keyring: keyring}
	manager := newTestManager(t, config, credentials)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, r.ParseForm())
		require.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		require.Equal(t, "client", r.Form.Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"access_token":"new-access","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer server.Close()
	now := time.Now()
	require.NoError(t, manager.Register(server.URL, "alice", "file", "oauth", HostOptions{OAuthClientID: "client"}, Credential{AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: now.Add(-time.Minute)}))
	var wg sync.WaitGroup
	results := make(chan Credential, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			c, err := manager.Refresh(context.Background(), server.URL, "alice", server.Client(), false, now)
			results <- c
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	for credential := range results {
		require.Equal(t, "new-access", credential.AccessToken)
		require.Equal(t, "old-refresh", credential.RefreshToken)
	}
	require.Equal(t, 1, requests)
}

func TestOAuthWithoutHTTPClientUsesDefaultClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer server.Close()
	credential, err := OAuthLogin(context.Background(), OAuthOptions{Instance: server.URL, Timeout: time.Second, Authorize: func(_ context.Context, raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		response, err := http.Get(u.Query().Get("redirect_uri") + "?code=code&state=" + url.QueryEscape(u.Query().Get("state")))
		if err != nil {
			return err
		}
		return response.Body.Close()
	}})
	require.NoError(t, err)
	require.Equal(t, "access", credential.AccessToken)
	config := settings.New(t.TempDir())
	manager := newTestManager(t, config, &Store{Dir: config.Dir})
	require.NoError(t, manager.Register(server.URL, "alice", "file", "oauth", HostOptions{}, Credential{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Minute)}))
	credential, err = manager.Refresh(context.Background(), server.URL, "alice", nil, false, time.Now())
	require.NoError(t, err)
	require.Equal(t, "access", credential.AccessToken)
}

func TestRefreshEndsBeforeSettingsLockWaitersGiveUp(t *testing.T) {
	// A refresh holds the settings lock, so concurrent gt processes must wait longer than it can take.
	require.Less(t, refreshTimeout, settings.LockTimeout)
}
