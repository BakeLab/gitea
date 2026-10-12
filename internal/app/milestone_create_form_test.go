// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

type milestoneFormFunc func(context.Context) (terminal.MilestoneFormValues, error)

func (f milestoneFormFunc) EditMilestone(ctx context.Context, _ string, _ terminal.MilestoneFormValues) (terminal.MilestoneFormValues, error) {
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

func TestMilestoneTablesViewsAndDoneLines(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/milestones":
			writeFirstPage(w, r, `[{"id":5,"title":"later","state":"open"},{"id":4,"title":"v0.9","state":"closed","closed_issues":3},`+
				`{"id":3,"title":"v1.0","state":"open","description":"First release","due_on":"2026-12-31T00:00:00Z","open_issues":3,"closed_issues":1}]`)
		case "GET /api/v1/repos/alice/project/issues/1", "GET /api/v1/repos/alice/project/issues/2":
			_, _ = w.Write([]byte(`{"number":1,"milestone":{"id":4,"title":"v0.9"}}`))
		case "PATCH /api/v1/repos/alice/project/issues/1", "PATCH /api/v1/repos/alice/project/issues/2":
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = fmt.Fprintf(w, `{"number":%s}`, r.URL.Path[len(r.URL.Path)-1:])
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":3,"title":"v1.0","state":"closed"}`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return testNow }
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	run := func(tty bool, args ...string) {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		deps.Streams.OutTTY = tty
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input")))
	}
	run(true, "milestone", "list", "--state", "all")
	require.Equal(t, "Showing 3 of 3 milestones in alice/project\n\n"+
		"ID  TITLE  STATE   DUE                PROGRESS\n"+
		"3   v1.0   open    in about 2 months  1/4 (25%)\n"+
		"5   later  open                       0/0\n"+
		"4   v0.9   closed                     3/3 (100%)\n", out.String())
	run(false, "milestone", "list", "--state", "all")
	require.Equal(t, "3\tv1.0\topen\t2026-12-31T00:00:00Z\t1/4 (25%)\n5\tlater\topen\t\t0/0\n4\tv0.9\tclosed\t\t3/3 (100%)\n", out.String())
	run(true, "milestone", "view", "v1.0")
	require.Equal(t, "v1.0 #3\nOpen • due Dec 31, 2026 (in about 2 months) • 1 of 4 closed (25%)\n\nFirst release\n\n"+
		"View this milestone on Gitea: "+server.URL+"/alice/project/milestone/3\n", out.String())
	run(true, "milestone", "close", "v1.0", "v0.9")
	require.Equal(t, "✓ Closed milestone \"v1.0\" in alice/project\n! Milestone \"v0.9\" in alice/project is already closed\n", diagnostics.String())
	run(true, "milestone", "issue", "add", "v1.0", "1", "#2")
	require.Equal(t, "✓ Added #1, #2 to milestone \"v1.0\" in alice/project\n", diagnostics.String())
	run(true, "milestone", "issue", "remove", "v0.9", "1")
	require.Equal(t, "✓ Removed #1 from milestone \"v0.9\" in alice/project\n", diagnostics.String())
	run(false, "milestone", "create", "v2.0")
	require.Equal(t, server.URL+"/alice/project/milestone/3\n", out.String())
	require.Equal(t, []string{
		"GET /api/v1/repos/alice/project/milestones/3",
		"PATCH /api/v1/repos/alice/project/milestones/3",
		"PATCH /api/v1/repos/alice/project/issues/1",
		"PATCH /api/v1/repos/alice/project/issues/2",
		"PATCH /api/v1/repos/alice/project/issues/1",
		"POST /api/v1/repos/alice/project/milestones",
	}, changed)
}

func TestMilestoneEditFormStartsFromTheMilestone(t *testing.T) {
	var edited map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/milestones":
			writeFirstPage(w, r, `[{"id":3,"title":"v1.0","description":"First","due_on":"2026-12-31T00:00:00Z"}]`)
		case "PATCH /api/v1/repos/alice/project/milestones/3":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&edited))
			_, _ = w.Write([]byte(`{"id":3,"title":"v1.0.0"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	var heading string
	var current terminal.MilestoneFormValues
	deps.MilestoneForm = milestoneFormEditor(func(h string, c terminal.MilestoneFormValues) terminal.MilestoneFormValues {
		heading, current = h, c
		return terminal.MilestoneFormValues{Title: "v1.0.0", Description: c.Description}
	})
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "edit", "v1.0", "-R", "alice/project"}))
	require.Equal(t, "Editing milestone \"v1.0\" in alice/project", heading)
	require.Equal(t, "First", current.Description)
	require.Equal(t, "2026-12-31", current.Deadline.Format(time.DateOnly))
	require.Equal(t, "v1.0.0", edited["title"])
	require.Equal(t, "First", edited["description"])
}

type milestoneFormEditor func(string, terminal.MilestoneFormValues) terminal.MilestoneFormValues

func (f milestoneFormEditor) EditMilestone(_ context.Context, heading string, current terminal.MilestoneFormValues) (terminal.MilestoneFormValues, error) {
	return f(heading, current), nil
}
