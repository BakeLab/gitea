// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

type milestoneFormFunc func(context.Context) (terminal.MilestoneFormValues, error)

func (f milestoneFormFunc) CreateMilestone(ctx context.Context) (terminal.MilestoneFormValues, error) {
	return f(ctx)
}

func TestMilestoneCreateInteractiveForm(t *testing.T) {
	var body map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, _ = w.Write([]byte(`{"id":9,"title":"Release","description":"Ship it","state":"open","due_on":"2026-12-31T00:00:00Z"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	deps.MilestoneForm = milestoneFormFunc(func(context.Context) (terminal.MilestoneFormValues, error) {
		return terminal.MilestoneFormValues{Title: "Release", Description: "Ship it", Deadline: &due}, nil
	})
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "alice/project", "--json=id,title,description", "--interactive"}))
	require.JSONEq(t, `{"id":9,"title":"Release","description":"Ship it"}`, out.String())
	require.Empty(t, diagnostics.String())
	require.Equal(t, "Release", body["title"])
	require.Equal(t, "Ship it", body["description"])
	require.Equal(t, "2026-12-31T00:00:00Z", body["due_on"])

	out.Reset()
	deps.MilestoneForm = milestoneFormFunc(func(context.Context) (terminal.MilestoneFormValues, error) {
		t.Fatal("form should not run when create flags are set")
		return terminal.MilestoneFormValues{}, nil
	})
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"milestone", "create", "-R", "alice/project", "--no-input"})))
	require.Empty(t, out.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "alice/project", "--title", "Flagged", "--json=id", "--interactive"}))
}

func TestMilestoneCreateState(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		_, _ = w.Write([]byte(`{"id":9,"title":"Done","state":"closed"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "alice/project", "--title", "Done", "--state", "closed", "--json=id,state", "--no-input"}))
	require.Equal(t, "closed", payload["state"])
	require.JSONEq(t, `{"id":9,"state":"closed"}`, out.String())
}
