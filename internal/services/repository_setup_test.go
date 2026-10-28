// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryTemplateUsesAuthenticatedDestination(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	item, err := (Repositories{Client: client}).Generate(context.Background(), "owner/template", RepositoryTemplateOptions{Name: "generated", Content: true})
	require.NoError(t, err)
	require.Equal(t, "generated", item.Name)
}

func TestRepositoryTemplateNamesWhatIsMissing(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/owner/template" {
			_, _ = w.Write([]byte(`{"full_name":"owner/template"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	ctx := context.Background()
	_, err := (Repositories{Client: client}).Generate(ctx, "owner/nonexist", RepositoryTemplateOptions{Name: "x", Owner: "acme"})
	require.EqualError(t, err, "template repository owner/nonexist not found")
	_, err = (Repositories{Client: client}).Generate(ctx, "owner/template", RepositoryTemplateOptions{Name: "x", Owner: "acme"})
	require.EqualError(t, err, `organization "acme" not found`)
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
	require.ErrorContains(t, err, "bad credential [REDACTED]", "the rest of the message explains the failure")
}

func TestRepositoryMigrationRedactsEscapedCredentials(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"cannot clone https://me:p%40ss%2Fw@source.test/repo: p@ss/w rejected"}`))
	}))
	_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git", AuthUser: "me", AuthPassword: "p@ss/w"})
	require.ErrorContains(t, err, "cannot clone https://me:[REDACTED]@source.test/repo: [REDACTED] rejected")
}

func TestRepositoryMigrationRedactsCredentialsBeforeShorteningTheMessage(t *testing.T) {
	// The cut after 300 characters falls within the password.
	message := strings.Repeat("x", 280) + " p@ss;word-long-secret rejected"
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprintf(w, `{"message":%q}`, message)
	}))
	_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git", AuthUser: "me", AuthPassword: "p@ss;word-long-secret"})
	require.ErrorContains(t, err, strings.Repeat("x", 280)+" [REDACTED] rejected")
	require.NotContains(t, err.Error(), "p@ss")
}

func TestRepositoryMigrationRedactsURLPasswords(t *testing.T) {
	for name, message := range map[string]string{
		// url.UserPassword escapes "@" but keeps ";".
		"userinfo escaping": "cannot clone https://me:p%40ss;word@source.test/repo",
		"unmarked password": "cannot clone https://me:other-secret@source.test/repo",
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = fmt.Fprintf(w, `{"message":%q}`, message)
			}))
			_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git", AuthUser: "me", AuthPassword: "p@ss;word"})
			require.EqualError(t, err, "Gitea API returned HTTP 422 for POST /api/v1/repos/migrate: cannot clone https://me:[REDACTED]@source.test/repo")
		})
	}
}

func TestRepositoryMigrationKeepsMessageWithoutCredentials(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"repository already exists"}`))
	}))
	_, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git"})
	require.ErrorContains(t, err, "repository already exists")
}

func TestRepositoryMigrateSendsADescriptionOfManyBytes(t *testing.T) {
	description := strings.Repeat("日", 800) // 2400 bytes, 800 characters
	var methods []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		require.Equal(t, description, requestJSON(t, r)["description"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":2,"name":"imported","full_name":"alice/imported","description":"` + description + `"}`))
	}))
	migrated, err := (Repositories{Client: client}).Migrate(context.Background(), RepositoryMigrationOptions{Name: "imported", CloneURL: "https://source.test/repo", Service: "git", Description: description})
	require.NoError(t, err)
	require.Equal(t, []string{http.MethodPost}, methods)
	require.Equal(t, description, migrated.Description)
}

func TestRepositoryMigrateFromAForgeWithoutAToken(t *testing.T) {
	for _, test := range []struct {
		name string
		opts RepositoryMigrationOptions
	}{
		{"anonymous", RepositoryMigrationOptions{Service: "github"}},
		{"password", RepositoryMigrationOptions{Service: "gitlab", AuthUser: "source-account", AuthPassword: "source-secret"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var sent map[string]any
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = requestJSON(t, r)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":2,"name":"imported","full_name":"alice/imported"}`))
			}))
			test.opts.Name, test.opts.CloneURL = "imported", "https://source.test/repo"
			migrated, err := (Repositories{Client: client}).Migrate(context.Background(), test.opts)
			require.NoError(t, err)
			require.Equal(t, "alice/imported", migrated.FullName)
			require.Equal(t, test.opts.Service, sent["service"])
			require.Empty(t, sent["auth_token"])
			if test.opts.AuthUser != "" {
				require.Equal(t, test.opts.AuthUser, sent["auth_username"])
			}
		})
	}
}
