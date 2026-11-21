// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryJSONReportsAMissingParentAsNull(t *testing.T) {
	data, err := json.Marshal(Repository{FullName: "alice/project"})
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	require.Contains(t, fields, "parent")
	require.Nil(t, fields["parent"])
}

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
}

func TestRepositoryRenameNamesTheTakenName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"repository already exists [uname: alice, name: taken]"}`))
	}))
	name := "taken"
	_, err := (Repositories{Client: client}).Edit(context.Background(), "alice/old", RepositoryEdit{Name: &name})
	require.EqualError(t, err, "repository alice/taken already exists")
}

func TestRepositoryCreateReportsMissingOrganization(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"GetOrgByName"}`))
	}))
	_, err := (Repositories{Client: client}).Create(context.Background(), "acme-typo", RepositoryOptions{Name: "tool"})
	require.EqualError(t, err, `organization "acme-typo" not found`)
}

func TestRepositoryCreateForAnotherUserNeedsASiteAdministrator(t *testing.T) {
	admin := true
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/orgs/bob/repos":
			w.WriteHeader(http.StatusNotFound)
		case "POST /api/v1/admin/users/bob/repos":
			if !admin {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			assert.Equal(t, "tool", requestJSON(t, r)["name"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":3,"name":"tool","full_name":"bob/tool"}`))
		case "GET /api/v1/users/bob":
			_, _ = w.Write([]byte(`{"id":2,"login":"bob"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	item, err := (Repositories{Client: client}).Create(context.Background(), "bob", RepositoryOptions{Name: "tool"})
	require.NoError(t, err)
	require.Equal(t, "bob/tool", item.FullName)
	admin = false
	_, err = (Repositories{Client: client}).Create(context.Background(), "bob", RepositoryOptions{Name: "tool"})
	require.EqualError(t, err, "bob is a user, not an organization; only site administrators can create repositories for other users")
}

func TestRepositoryReadme(t *testing.T) {
	dirEntry := func(name string) string { return `{"name":"` + name + `","path":"` + name + `","type":"dir"}` }
	fileEntry := func(dir, name string) string {
		return `{"name":"` + name + `","path":"` + dir + name + `","type":"file"}`
	}
	listings := map[string]string{
		"/api/v1/repos/alice/project/contents/":        "[" + fileEntry("", "README.rst") + "," + fileEntry("", "readme.txt") + "," + dirEntry(".github") + "]",
		"/api/v1/repos/alice/project/contents/.github": "[" + fileEntry(".github/", "README.md") + "]",
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") == "develp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if body, ok := listings[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		assert.Equal(t, "/api/v1/repos/alice/project/raw/.github/README.md", r.URL.Path)
		_, _ = w.Write([]byte("hello"))
	}))
	path, content, err := (Repositories{Client: client}).Readme(context.Background(), "alice/project", "")
	require.NoError(t, err)
	assert.Equal(t, ".github/README.md", path)
	assert.Equal(t, "hello", content)

	_, _, err = (Repositories{Client: client}).Readme(context.Background(), "alice/project", "develp")
	require.EqualError(t, err, `branch "develp" not found in alice/project`)
}

func TestRepositoryCreateSendsADescriptionOfManyBytes(t *testing.T) {
	description := strings.Repeat("日", 800) // 2400 bytes in 800 characters, within Gitea's limit of 2048 characters
	var methods []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		require.Equal(t, description, requestJSON(t, r)["description"])
		_, _ = w.Write([]byte(`{"id":1,"name":"project","full_name":"alice/project","description":"` + description + `"}`))
	}))
	created, err := (Repositories{Client: client}).Create(context.Background(), "", RepositoryOptions{Name: "project", Description: description})
	require.NoError(t, err)
	require.Equal(t, []string{http.MethodPost}, methods)
	require.Equal(t, description, created.Description)
}
