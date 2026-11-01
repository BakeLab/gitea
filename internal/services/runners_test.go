// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunnerListCountsFromTheBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/orgs/acme/actions/runners", r.URL.Path)
		_, _ = w.Write([]byte(`{"runners":[{"id":3,"name":"builder-1"}],"total_count":3}`))
	}))
	page, err := Runners{Client: client, Scope: OwnerScope{Organization: "acme"}}.List(context.Background(), 1, 1, nil)
	require.NoError(t, err)
	require.Equal(t, Page[Runner]{Items: []Runner{{ID: 3, Name: "builder-1", Labels: []string{}}}, More: true, Total: 3, Counted: true}, page)
}
