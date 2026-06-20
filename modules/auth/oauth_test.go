// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression test for the redirect_uri propagation bug: with --redirect-url
// omitted, the callback listener picks a free port and opts.RedirectURL is
// rewritten in place. The OAuth2 config must see that rewritten URL so the
// token exchange sends the same redirect_uri the authorize step advertised
// (RFC 6749 §4.1.3).
func TestPerformBrowserOAuthFlow_RedirectURIMatchesAcrossAuthorizeAndExchange(t *testing.T) {
	var (
		mu                   sync.Mutex
		authorizeRedirectURI string
		exchangeRedirectURI  string
	)

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/authorize":
			mu.Lock()
			authorizeRedirectURI = r.URL.Query().Get("redirect_uri")
			mu.Unlock()
			cb, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			require.NoError(t, err)
			q := cb.Query()
			q.Set("code", "test-code")
			q.Set("state", r.URL.Query().Get("state"))
			cb.RawQuery = q.Encode()
			http.Redirect(w, r, cb.String(), http.StatusFound)

		case "/login/oauth/access_token":
			require.NoError(t, r.ParseForm())
			mu.Lock()
			exchangeRedirectURI = r.PostForm.Get("redirect_uri")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "test-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(idp.Close)

	// Replace the real browser opener with a goroutine that fetches the
	// authorize URL, just like a real browser would.
	origOpenBrowser := openBrowser
	t.Cleanup(func() { openBrowser = origOpenBrowser })
	openBrowser = func(authURL string) error {
		go func() {
			resp, err := http.Get(authURL)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	// Leave RedirectURL empty so tea picks a random port.
	_, token, err := performBrowserOAuthFlow(context.Background(), OAuthOptions{
		URL:      idp.URL,
		ClientID: "test-client-id",
	})
	require.NoError(t, err)
	require.Equal(t, "test-token", token.AccessToken)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, authorizeRedirectURI)
	require.NotEmpty(t, exchangeRedirectURI)
	assert.Equal(t, authorizeRedirectURI, exchangeRedirectURI,
		"redirect_uri must match between authorize and token exchange (RFC 6749 §4.1.3)")
}
