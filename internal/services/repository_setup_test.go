// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryTemplateUsesAuthenticatedDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			require.Equal(t, "/api/v1/user", r.URL.Path)
			_, _ = w.Write([]byte(`{"login":"account"}`))
			return
		}
		require.Equal(t, "/api/v1/repos/owner/template/generate", r.URL.Path)
		payload := requestJSON(t, r)
		require.Equal(t, "account", payload["owner"])
		require.Equal(t, true, payload["git_content"])
		require.Equal(t, false, payload["git_hooks"])
		_, _ = w.Write([]byte(`{"id":2,"name":"generated"}`))
	}))
	defer server.Close()
	item, err := (Repositories{Client: transport.New(server.URL, "", false)}).Generate(context.Background(), "owner/template", RepositoryTemplateOptions{Name: "generated", Content: true})
	require.NoError(t, err)
	require.Equal(t, "generated", item.Name)
}

func TestRepositoryMigrationDoesNotRetryOrLeakCredentials(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations++
		require.Equal(t, "/api/v1/repos/migrate", r.URL.Path)
		payload := requestJSON(t, r)
		require.Equal(t, "source-secret", payload["auth_password"])
		require.NotContains(t, payload, "Timeout")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"source-secret"}`))
	}))
	defer server.Close()
	client := transport.New(server.URL, "destination-token", false)
	originalTimeout := client.HTTP.Timeout
	_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git", AuthUser: "source-account", AuthPassword: "source-secret", Timeout: time.Minute})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "source-secret")
	require.Equal(t, 1, mutations)
	require.Equal(t, originalTimeout, client.HTTP.Timeout)
}

func TestRepositoryMigrationMapsOptionsAndClearsTokenEcho(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/migrate", r.URL.Path)
		payload := requestJSON(t, r)
		assert.Equal(t, "imported", payload["repo_name"])
		assert.Equal(t, "acme", payload["repo_owner"])
		assert.Equal(t, "https://source.test/repo", payload["clone_addr"])
		assert.Equal(t, "github", payload["service"])
		assert.Equal(t, "source-token", payload["auth_token"])
		assert.Equal(t, true, payload["issues"])
		assert.Equal(t, true, payload["lfs"])
		assert.Equal(t, "https://source.test/lfs", payload["lfs_endpoint"])
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"bad credential source-token"}`))
	}))
	_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{
		Name: "imported", Owner: "acme", CloneURL: "https://source.test/repo", Service: "github", AuthToken: "source-token",
		Issues: true, LFS: true, LFSEndpoint: "https://source.test/lfs",
	})
	var failure *transport.APIError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, http.StatusUnprocessableEntity, failure.Status)
	require.NotContains(t, err.Error(), "source-token")
}

func TestRepositoryMigrationKeepsMessageWithoutCredentials(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"repository already exists"}`))
	}))
	_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git"})
	require.ErrorContains(t, err, "repository already exists")
}
