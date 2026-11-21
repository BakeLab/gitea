// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtensionRefsAndFilesTellAMissingRepository(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() + "?" + r.URL.RawQuery {
		case "/api/v1/repos/o/r?":
			_, _ = w.Write([]byte(`{"full_name":"o/r"}`))
		case "/api/v1/repos/o/r/git/commits/feature%2Fx?":
			_, _ = w.Write([]byte(`{"sha":"abc123"}`))
		case "/api/v1/repos/o/r/git/commits/empty?":
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/repos/o/r/raw/gt.yml?ref=release%2F1.0":
			_, _ = w.Write([]byte("name: tool\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := Repositories{Client: client}
	sha, err := service.Commit(context.Background(), "o/r", "feature/x")
	require.NoError(t, err)
	require.Equal(t, "abc123", sha)
	_, err = service.Commit(context.Background(), "o/r", "empty")
	require.ErrorIs(t, err, errInvalidJSON)
	_, err = service.Commit(context.Background(), "o/r", "nope")
	require.EqualError(t, err, `ref "nope" not found in o/r`)
	_, err = service.Commit(context.Background(), "o/gone", "main")
	require.EqualError(t, err, "repository o/gone not found")

	data, found, err := service.File(context.Background(), "o/r", "release/1.0", "gt.yml")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "name: tool\n", string(data))
	_, found, err = service.File(context.Background(), "o/r", "main", "missing.yml")
	require.NoError(t, err)
	require.False(t, found)
	_, _, err = service.File(context.Background(), "o/gone", "main", "gt.yml")
	require.EqualError(t, err, "repository o/gone not found")
}
