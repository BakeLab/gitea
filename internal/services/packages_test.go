// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageReadsMapServerFields(t *testing.T) {
	const pkg = `{"id":3,"owner":{"login":"alice"},"creator":{"login":"bob"},"repository":{"full_name":"alice/project"},"type":"generic","name":"tool","version":"1.0","html_url":"https://gitea.example/alice/-/packages/generic/tool/1.0","created_at":"2026-10-01T12:00:00Z"}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/packages/alice":
			assert.Equal(t, "generic", r.URL.Query().Get("type"))
			assert.Equal(t, "to", r.URL.Query().Get("q"))
			assert.Equal(t, "2", r.URL.Query().Get("page"))
			_, _ = w.Write([]byte(`[` + pkg + `]`))
		case "/api/v1/packages/alice/generic/tool":
			assert.Equal(t, "5", r.URL.Query().Get("limit"))
			_, _ = w.Write([]byte(`[` + pkg + `]`))
		case "/api/v1/packages/alice/generic/tool/-/latest", "/api/v1/packages/alice/generic/tool/1.0":
			_, _ = w.Write([]byte(pkg))
		case "/api/v1/packages/alice/generic/tool/1.0/files":
			_, _ = w.Write([]byte(`[{"id":4,"name":"tool-linux","size":12,"sha256":"abc"}]`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Packages{Client: client, Owner: "alice"}
	want := Package{ID: 3, Owner: "alice", Type: "generic", Name: "tool", Version: "1.0", Repository: "alice/project", Creator: "bob", URL: "https://gitea.example/alice/-/packages/generic/tool/1.0", CreatedAt: "2026-10-01T12:00:00Z"}
	listed, err := service.List(context.Background(), PackageListOptions{Type: "generic", Query: "to", Limit: 10, Page: 2})
	require.NoError(t, err)
	require.Equal(t, []Package{want}, listed.Items)
	versions, err := service.Versions(context.Background(), "generic", "tool", 5, 1)
	require.NoError(t, err)
	require.Equal(t, []Package{want}, versions.Items)
	for _, version := range []string{"", "1.0"} {
		viewed, err := service.View(context.Background(), "generic", "tool", version)
		require.NoError(t, err)
		require.Equal(t, want, viewed)
	}
	files, err := service.Files(context.Background(), "generic", "tool", "1.0")
	require.NoError(t, err)
	require.Equal(t, []PackageFile{{ID: 4, Name: "tool-linux", Size: 12, SHA256: "abc"}}, files)
}

func TestPackageDeleteSelectsOneOrEveryVersion(t *testing.T) {
	var deleted []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		deleted = append(deleted, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/2.0") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	service := Packages{Client: client, Owner: "alice"}
	require.NoError(t, service.Delete(context.Background(), "generic", "tool", "1.0"))
	require.NoError(t, service.Delete(context.Background(), "generic", "tool", ""))
	require.EqualError(t, service.Delete(context.Background(), "generic", "tool", "2.0"), "package alice/generic/tool@2.0 not found")
	require.Equal(t, []string{"/api/v1/packages/alice/generic/tool/1.0", "/api/v1/packages/alice/generic/tool", "/api/v1/packages/alice/generic/tool/2.0"}, deleted)
}

func TestPackageLinkReportsWhetherItChanged(t *testing.T) {
	for _, tc := range []struct {
		name, repository, linked string
		status                   int
		message                  string
		changed                  bool
		err                      string
	}{
		{name: "link", repository: "project", status: http.StatusCreated, changed: true},
		{name: "unlink", status: http.StatusNoContent, changed: true},
		{name: "unlink without repository", status: http.StatusBadRequest, message: "invalid argument"},
		{name: "missing repository", repository: "nope", status: http.StatusNotFound, message: "repository does not exist", err: "repository alice/nope not found"},
		{name: "linked already", repository: "project", linked: "Alice/Project", status: http.StatusBadRequest, message: "invalid argument"},
		{name: "linked elsewhere", repository: "project", linked: "alice/other", status: http.StatusBadRequest, message: "invalid argument", err: "package alice/generic/tool is already linked to alice/other; unlink it first"},
		{name: "missing package", repository: "project", status: http.StatusNotFound, message: "package does not exist", err: "package alice/generic/tool not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					assert.Equal(t, "/api/v1/packages/alice/generic/tool/-/latest", r.URL.Path)
					_, _ = w.Write([]byte(`{"repository":{"full_name":"` + tc.linked + `"}}`))
					return
				}
				want := "/api/v1/packages/alice/generic/tool/-/unlink"
				if tc.repository != "" {
					want = "/api/v1/packages/alice/generic/tool/-/link/" + tc.repository
				}
				assert.Equal(t, http.MethodPost+" "+want, r.Method+" "+r.URL.Path)
				w.WriteHeader(tc.status)
				if tc.message != "" {
					_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
				}
			}))
			changed, err := (Packages{Client: client, Owner: "alice"}).Link(context.Background(), "generic", "tool", tc.repository)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.changed, changed)
		})
	}
}

func TestCheckGenericUploadRefusesAFileTheVersionHolds(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/packages/alice/generic/tool/1.0/files":
			_, _ = w.Write([]byte(`[{"name":"notes.txt"},{"name":"Tool-Linux"}]`))
		case "/api/v1/packages/alice/generic/tool/2.0/files":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Packages{Client: client, Owner: "alice"}
	err := service.CheckGenericUpload(context.Background(), "tool", "1.0", []string{"tool-darwin", "tool-linux"})
	require.EqualError(t, err, "file Tool-Linux already exists in tool@1.0")
	require.NoError(t, service.CheckGenericUpload(context.Background(), "tool", "1.0", []string{"tool-darwin"}))
	require.NoError(t, service.CheckGenericUpload(context.Background(), "tool", "2.0", []string{"tool-linux"}), "a new version holds no files")

	client.Signer = &digestSigner{}
	require.ErrorIs(t, service.CheckGenericUpload(context.Background(), "tool", "1.0", nil), errRegistrySignature)
	require.ErrorIs(t, service.UploadGeneric(context.Background(), "tool", "1.0", "tool-linux", strings.NewReader("")), errRegistrySignature)
}

func TestUploadGenericStreamsTheFileAndWordsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		err    string
	}{
		{name: "created", status: http.StatusCreated},
		{name: "exists", status: http.StatusConflict, err: "file tool-linux already exists in tool@1.0"},
		{name: "no access", status: http.StatusUnauthorized, err: "uploading packages to alice requires write access to its packages and a token with the write:package scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/user" {
					_, _ = w.Write([]byte(`{"login":"bob"}`))
					return
				}
				assert.Equal(t, "PUT /api/packages/alice/generic/tool/1.0/tool-linux", r.Method+" "+r.URL.Path)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, "binary", string(body))
				w.WriteHeader(tc.status)
			}))
			err := (Packages{Client: client, Owner: "alice"}).UploadGeneric(context.Background(), "tool", "1.0", "tool-linux", strings.NewReader("binary"))
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.err)
		})
	}
}
