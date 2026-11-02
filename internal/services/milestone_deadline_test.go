// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMilestoneEditPreservesDeadlineUnlessExplicitlyCleared(t *testing.T) {
	reads := 0
	want := "2028-01-15T23:59:59-08:00"
	current := `{"id":9,"due_on":"2028-01-15T23:59:59-08:00"}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reads++
			_, _ = w.Write([]byte(current))
			return
		}
		options := requestJSON(t, r)
		require.Equal(t, want, options["due_on"])
		_, _ = w.Write([]byte(`{"id":9}`))
	}))
	service := Milestones{Client: client, Repository: "alice/project"}
	state := "closed"
	_, err := service.Edit(context.Background(), 9, MilestoneOptions{State: &state})
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	want = "0001-01-01T00:00:00Z"
	_, err = service.Edit(context.Background(), 9, MilestoneOptions{Deadline: &want})
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	current = `{"id":9}`
	_, err = service.Edit(context.Background(), 9, MilestoneOptions{State: &state})
	require.NoError(t, err)
	require.Equal(t, 2, reads)
}
