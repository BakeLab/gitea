// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gitea.dev/tea/modules/config"

	"github.com/stretchr/testify/assert"
)

func TestCheckLoginStatus(t *testing.T) {
	// Keep helper detection isolated from the developer's real git config.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), ".gitconfig"))

	t.Run("valid token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/user", r.URL.Path)
			assert.Equal(t, "token secret-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":1,"login":"alice"}`))
		}))
		defer server.Close()

		status := CheckLoginStatus(context.Background(), &config.Login{
			Name:         "test",
			URL:          server.URL,
			Token:        "secret-token",
			VersionCheck: false,
		})

		assert.True(t, status.Valid)
		assert.Empty(t, status.Error)
		assert.Equal(t, "test", status.Name)
		assert.Equal(t, server.URL, status.URL)
		assert.Equal(t, "alice", status.User)
		assert.Equal(t, "token", status.AuthMethod)
		assert.False(t, status.Helper)
	})

	t.Run("invalid token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"token is invalid"}`))
		}))
		defer server.Close()

		status := CheckLoginStatus(context.Background(), &config.Login{
			Name:         "test",
			URL:          server.URL,
			Token:        "expired-token",
			VersionCheck: false,
		})

		assert.False(t, status.Valid)
		assert.Contains(t, status.Error, "token is invalid")
	})

	t.Run("missing token", func(t *testing.T) {
		status := CheckLoginStatus(context.Background(), &config.Login{
			Name: "test",
			URL:  "https://gitea.example.com",
		})

		assert.False(t, status.Valid)
		assert.Contains(t, status.Error, "no access token configured")
	})
}
