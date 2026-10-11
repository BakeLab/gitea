// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMilestoneNamesIncludeClosedAndRejectAmbiguity(t *testing.T) {
	duplicate := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "all", r.URL.Query().Get("state"))
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"id":1,"title":"Other"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":2,"title":"Release","state":"closed"}]`))
		case "3":
			if duplicate {
				_, _ = w.Write([]byte(`[{"id":3,"title":"Release"}]`))
				return
			}
			fallthrough
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	service := Milestones{Client: client, Repository: "alice/project"}
	id, err := service.ResolveName(context.Background(), "Release")
	require.NoError(t, err)
	require.EqualValues(t, 2, id)
	_, err = service.ResolveName(context.Background(), "missing")
	require.ErrorContains(t, err, "does not exist")
	duplicate = true
	_, err = service.ResolveName(context.Background(), "Release")
	require.ErrorContains(t, err, "ambiguous")
}
