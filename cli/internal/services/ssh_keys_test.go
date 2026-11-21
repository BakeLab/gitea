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

const sshKeyJSON = `{"id":3,"title":"laptop","key":"ssh-ed25519 AAAA","fingerprint":"SHA256:abc","created_at":"2026-10-01T12:00:00Z","read_only":true,"user":{"login":"alice"}}`

func TestSSHKeyListPaginatesAndMapsKeys(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/user/keys", r.URL.Path)
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[` + sshKeyJSON + `]`))
	}))
	service := SSHKeys{Client: client}
	keys, err := service.List(context.Background(), 20, 1)
	require.NoError(t, err)
	require.Equal(t, []SSHKey{{ID: 3, Title: "laptop", Key: "ssh-ed25519 AAAA", Fingerprint: "SHA256:abc", CreatedAt: "2026-10-01T12:00:00Z", ReadOnly: true}}, keys.Items)
	keys, err = service.List(context.Background(), 20, 2)
	require.NoError(t, err)
	require.NotNil(t, keys.Items)
	require.Empty(t, keys.Items)
}

func TestSSHKeyAllReadsEveryPage(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "3" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[` + sshKeyJSON + `]`))
	}))
	keys, err := SSHKeys{Client: client}.All(context.Background())
	require.NoError(t, err)
	require.Len(t, keys, 2)
}

func TestSSHKeyViewCreateAndDelete(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPost:
			payload := requestJSON(t, r)
			assert.Equal(t, map[string]any{"title": "laptop", "key": "ssh-ed25519 AAAA", "read_only": false}, payload)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(sshKeyJSON))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			if r.URL.Path == "/api/v1/user" {
				_, _ = w.Write([]byte(`{"login":"alice"}`))
				return
			}
			_, _ = w.Write([]byte(sshKeyJSON))
		}
	}))
	service := SSHKeys{Client: client}
	key, err := service.View(context.Background(), 3)
	require.NoError(t, err)
	require.Equal(t, "laptop", key.Title)
	created, err := service.Create(context.Background(), "laptop", "ssh-ed25519 AAAA")
	require.NoError(t, err)
	require.EqualValues(t, 3, created.ID)
	require.NoError(t, service.Delete(context.Background(), 3))
	require.Equal(t, []string{"GET /api/v1/user/keys/3", "GET /api/v1/user", "POST /api/v1/user/keys", "DELETE /api/v1/user/keys/3"}, requests)
}

func TestSSHKeyViewRefusesKeysOfOtherAccounts(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"admin"}`))
			return
		}
		if r.URL.Path == "/api/v1/user/keys/4" {
			_, _ = w.Write([]byte(`{"id":4,"title":"other","key":"ssh-ed25519 BBBB"}`))
			return
		}
		_, _ = w.Write([]byte(sshKeyJSON))
	}))
	_, err := (SSHKeys{Client: client}).View(context.Background(), 3)
	require.EqualError(t, err, "SSH key 3 belongs to alice, not to admin")
	_, err = (SSHKeys{Client: client}).View(context.Background(), 4)
	require.EqualError(t, err, "SSH key 4 belongs to another account, not to admin")
}
