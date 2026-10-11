// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchRenameEscapesNames(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations++
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/v1/repos/owner/repo/branches/feature/a%23b", r.URL.EscapedPath())
		var payload map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, map[string]string{"name": "feature/renamed"}, payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	err := (Branches{Client: client}).Rename(context.Background(), "owner/repo", "feature/a#b", "feature/renamed")
	require.NoError(t, err)
	require.Equal(t, 1, mutations)
}

func TestBranchListMapsServerFields(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/owner/repo/branches", r.URL.Path)
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		assert.Equal(t, "3", r.URL.Query().Get("page"))
		_, _ = w.Write([]byte(`[{"name":"main","commit":{"id":"abc"},"protected":true,"required_approvals":2,"user_can_push":true,"user_can_merge":true},{"name":"orphan","commit":null}]`))
	}))
	items, err := (Branches{Client: client}).List(context.Background(), "owner/repo", 20, 3)
	require.NoError(t, err)
	require.Equal(t, []Branch{
		{Name: "main", SHA: "abc", Protected: true, RequiredApprovals: 2, CanPush: true, CanMerge: true},
		{Name: "orphan"},
	}, items)
}

func TestBranchCreateAndProtectPayloads(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := requestJSON(t, r)
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/branches":
			assert.Equal(t, "feature", payload["new_branch_name"])
			assert.Equal(t, "", payload["old_branch_name"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"feature","commit":{"id":"abc"}}`))
		case "/api/v1/repos/owner/repo/branch_protections":
			assert.Equal(t, "main", payload["branch_name"])
			assert.Equal(t, "main", payload["rule_name"])
			assert.Equal(t, false, payload["enable_push"])
			assert.EqualValues(t, 1, payload["required_approvals"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"rule_name":"main"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Branches{Client: client}
	item, err := service.Create(context.Background(), "owner/repo", "feature", "")
	require.NoError(t, err)
	require.Equal(t, Branch{Name: "feature", SHA: "abc"}, item)
	require.NoError(t, service.Protect(context.Background(), "owner/repo", "main"))
}
