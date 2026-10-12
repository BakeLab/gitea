// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryCreateSelectsOwnerEndpoint(t *testing.T) {
	for org, path := range map[string]string{"": "/api/v1/user/repos", "acme": "/api/v1/orgs/acme/repos"} {
		t.Run(path, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, path, r.URL.Path)
				payload := requestJSON(t, r)
				assert.Equal(t, "project", payload["name"])
				assert.Equal(t, true, payload["private"])
				assert.Equal(t, "Default", payload["issue_labels"])
				assert.Equal(t, "sha256", payload["object_format_name"])
				assert.Equal(t, "collaborator", payload["trust_model"])
				_, _ = w.Write([]byte(`{"id":1,"name":"project","full_name":"owner/project"}`))
			}))
			item, err := (Repositories{Client: client}).Create(context.Background(), org, RepositoryOptions{
				Name: "project", Private: true, Labels: "Default", ObjectFormat: "sha256", TrustModel: "collaborator",
			})
			require.NoError(t, err)
			require.Equal(t, Repository{ID: 1, Name: "project", FullName: "owner/project"}, item)
		})
	}
}

func TestRepositoryForkSendsOrganizationOnlyWhenSelected(t *testing.T) {
	for org, want := range map[string]any{"": nil, "acme": "acme"} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/repos/alice/project/forks", r.URL.Path)
			payload := requestJSON(t, r)
			assert.Equal(t, want, payload["organization"])
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":2,"full_name":"bob/project","fork":true,"parent":{"id":1,"full_name":"alice/project"}}`))
		}))
		item, existed, err := (Repositories{Client: client}).Fork(context.Background(), "alice/project", ForkOptions{Organization: org})
		require.NoError(t, err)
		require.False(t, existed)
		require.Equal(t, "alice/project", item.Parent.FullName)
	}
}

func TestRepositoryAssigneesTagsAndMissingPermissions(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/assignees":
			_, _ = w.Write([]byte(`[{"login":"alice","full_name":"Alice A."},{"login":"bob"}]`))
			return
		case "/api/v1/repos/alice/project/tags":
			assert.Equal(t, "2", r.URL.Query().Get("page"))
			assert.Equal(t, "5", r.URL.Query().Get("limit"))
			_, _ = w.Write([]byte(`[{"name":"v1.0"},{"name":"v0.9"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	service := Repositories{Client: client}
	names, err := service.assignees(context.Background(), "alice/project")
	require.NoError(t, err)
	require.Equal(t, []string{"alice", "bob"}, names)
	users, err := service.Assignees(context.Background(), "alice/project")
	require.NoError(t, err)
	require.Equal(t, []Assignee{{Username: "alice", FullName: "Alice A."}, {Username: "bob"}}, users)
	tags, err := service.Tags(context.Background(), "alice/project", 5, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"v1.0", "v0.9"}, tags)
	permissions, err := service.viewPermissions(context.Background(), "alice/project")
	require.NoError(t, err)
	require.Equal(t, RepositoryPermissions{}, permissions)
}
