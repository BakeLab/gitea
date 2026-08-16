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
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

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

// Regression test for the browser opener hang: xdg-open does not exit until
// the browser it launched does, and the callback is only consumed after
// openBrowser returns. Waiting on the opener hangs the CLI even though the
// user authenticated successfully.
func TestOpenBrowser_DoesNotWaitForOpener(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("xdg-open is not the opener on this platform")
	}

	const (
		fakeOpenerSleepTime = 10 * time.Second
		openBrowserTimeout  = 2 * time.Second
	)

	// A stand-in xdg-open that holds the foreground the way a browser it had
	// to launch would.
	dir := t.TempDir()
	opener := filepath.Join(dir, "xdg-open")
	script := fmt.Sprintf("#!/bin/sh\nexec sleep %d\n", int(fakeOpenerSleepTime.Seconds()))
	require.NoError(t, os.WriteFile(opener, []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	done := make(chan error, 1)
	go func() { done <- openBrowser("http://127.0.0.1:1/") }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(openBrowserTimeout):
		t.Fatal("openBrowser blocked on the opener; the callback would never be consumed")
	}
}
