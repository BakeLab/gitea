// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

func TestMilestoneNameSelectionResolvesAllPagesBeforeEditing(t *testing.T) {
	duplicate := false
	edits := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path == "/api/v1/repos/alice/project/milestones/9" {
				_, _ = w.Write([]byte(`{"id":9,"title":"Release one"}`))
				return
			}
			require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
			require.Equal(t, "all", r.URL.Query().Get("state"))
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write([]byte(`[{"id":9,"title":"Release one"}]`))
			case "2":
				if duplicate {
					_, _ = w.Write([]byte(`[{"id":10,"title":"Release one"}]`))
				} else {
					_, _ = w.Write([]byte(`[]`))
				}
			default:
				_, _ = w.Write([]byte(`[]`))
			}
			return
		}
		edits++
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/milestones/9", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":9,"title":"Release one","state":"closed"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"milestone", "close", "Release one", "--by-name", "-R", "alice/project", "--json=id,state"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.JSONEq(t, `{"id":9,"state":"closed"}`, out.String())
	duplicate = true
	out.Reset()
	require.ErrorContains(t, Execute(context.Background(), deps, args), "ambiguous")
	require.Empty(t, out.String())
	require.Equal(t, 1, edits)
}

func TestMilestoneDeleteReturnsJSONSnapshotOnlyAfterSuccess(t *testing.T) {
	calls := []string{}
	fail := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		if r.Method == http.MethodGet {
			require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
			writeFirstPage(w, r, `[{"id":9,"title":"Release one","state":"open"}]`)
			return
		}
		require.Equal(t, "/api/v1/repos/alice/project/milestones/9", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		if fail {
			w.WriteHeader(403)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"milestone", "delete", "9", "-R", "alice/project", "--yes", "--json=id,title"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.JSONEq(t, `{"id":9,"title":"Release one"}`, out.String())
	require.Equal(t, []string{"GET", "GET", "DELETE"}, calls)
	fail = true
	out.Reset()
	require.Error(t, Execute(context.Background(), deps, args))
	require.Empty(t, out.String())
}

func TestMilestoneIssueCommandsSelectByName(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/milestones":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":9,"title":"Release one"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues" && r.Method == http.MethodGet:
			// Gitea matches a filter value by title first, so the unique title is sent.
			require.Equal(t, "Release one", r.URL.Query().Get("milestones"))
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues/3" && r.Method == http.MethodPatch:
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.EqualValues(t, 9, payload["milestone"])
			_, _ = w.Write([]byte(`{"number":3,"milestone":{"id":9}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "issue", "list", "Release one", "--by-name", "-R", "alice/project", "--json"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "issue", "add", "Release one", "3", "--by-name", "-R", "alice/project", "--json=number"}))
	require.NotContains(t, calls, "GET /api/v1/repos/alice/project/milestones/9")
	err := Execute(context.Background(), deps, []string{"milestone", "issue", "remove", " ", "3", "--by-name", "-R", "alice/project"})
	require.ErrorContains(t, err, "milestone title or ID cannot be empty")
}

// payloadField returns the JSON request body's field name as text.
func payloadField(t *testing.T, r *http.Request, name string) string {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	return fmt.Sprint(payload[name])
}

func TestMilestonesAcceptTitlesOrIDs(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/milestones":
			writeFirstPage(w, r, `[{"id":3,"title":"v1.0","state":"open"},{"id":4,"title":"2026","state":"closed"},{"id":5,"title":"v2.0"}]`)
		case "GET /api/v1/repos/alice/project/milestones/3", "GET /api/v1/repos/alice/project/milestones/4":
			_, _ = w.Write([]byte(`{"id":3,"title":"v1.0","state":"open"}`))
		case "PATCH /api/v1/repos/alice/project/milestones/3", "PATCH /api/v1/repos/alice/project/milestones/4":
			calls = append(calls, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":3,"title":"v1.0","state":"closed"}`))
		case "POST /api/v1/repos/alice/project/issues":
			requirePayload(t, `{"title":"x","milestone":3}`, r.Body)
			calls = append(calls, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"number":8}`))
		case "PATCH /api/v1/repos/alice/project/issues/8":
			calls = append(calls, r.Method+" "+r.URL.Path+" milestone="+payloadField(t, r, "milestone"))
			_, _ = w.Write([]byte(`{"number":8}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append(args, "-R", "alice/project", "--json", "--yes"))
	}
	require.NoError(t, run("milestone", "view", "v1.0"))
	require.Contains(t, out.String(), `"id":3`)
	require.NoError(t, run("milestone", "close", "V1.0", "2026"))
	require.NoError(t, run("issue", "create", "--title", "x", "-m", "v1.0"))
	require.NoError(t, run("issue", "edit", "8", "--milestone", "3"))
	require.NoError(t, run("issue", "edit", "8", "--remove-milestone"))
	require.NoError(t, run("issue", "edit", "8", "--milestone", "0"))
	require.Equal(t, []string{
		"PATCH /api/v1/repos/alice/project/milestones/3",
		"POST /api/v1/repos/alice/project/issues",
		"PATCH /api/v1/repos/alice/project/issues/8 milestone=3",
		"PATCH /api/v1/repos/alice/project/issues/8 milestone=0",
		"PATCH /api/v1/repos/alice/project/issues/8 milestone=0",
	}, calls)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"milestone", "view", "v9"}, `milestone "v9" not found in alice/project`},
		{[]string{"milestone", "close", "v1.0", "3"}, "milestone 3 is selected more than once"},
		{[]string{"issue", "create", "--title", "x", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
	} {
		require.ErrorContains(t, run(test.args...), test.want)
	}
	require.Len(t, calls, 5)
}

func TestMilestoneListSortsBeforePaging(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Gitea orders by due date alone, so undated milestones come first.
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`[{"id":1,"title":"undated","state":"open"},{"id":2,"title":"dated","state":"open","due_on":"2028-01-15T00:00:00Z"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"milestone", "list", "-R", "alice/project", "--limit", "1", "--json=title"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.JSONEq(t, `[{"title":"dated"}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--page", "2")))
	require.JSONEq(t, `[{"title":"undated"}]`, out.String())
}

func TestBulkMilestoneStateContinuesAfterFailureAndKeepsJSONClean(t *testing.T) {
	mutations := []string{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/milestones" {
			writeFirstPage(w, r, `[{"id":9,"title":"v9"},{"id":10,"title":"v10"},{"id":11,"title":"v11"}]`)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/milestones/")
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprintf(w, `{"id":%s,"title":"v%s"}`, id, id)
			return
		}
		mutations = append(mutations, id)
		if id == "10" {
			w.WriteHeader(403)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%s,"title":"Milestone %s","state":"closed"}`, id, id)
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"milestone", "close", "9", "10", "11", "-R", "alice/project", "--json=id,state"})
	require.ErrorContains(t, err, "milestone v10")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.Equal(t, []string{"9", "10", "11"}, mutations)
	require.JSONEq(t, `[{"id":9,"state":"closed"},{"id":11,"state":"closed"}]`, out.String())
	require.Empty(t, diagnostics.String())
	mutations = nil
	out.Reset()
	err = Execute(context.Background(), deps, []string{"milestone", "close", "9", "bad", "-R", "alice/project"})
	require.ErrorContains(t, err, `milestone "bad" not found in alice/project`)
	require.Empty(t, mutations)
	require.Empty(t, out.String())
}

func TestMilestoneCreateDescriptionFileAndEditor(t *testing.T) {
	var createBody map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createBody))
		_, _ = w.Write([]byte(`{"id":4,"title":"Ship","description":"from file","state":"open"}`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	file := filepath.Join(t.TempDir(), "description.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "alice/project", "--title", "Ship", "--description-file", file, "--json=id,description", "--no-input"}))
	require.Equal(t, "from file", createBody["description"])
	require.JSONEq(t, `{"id":4,"description":"from file"}`, out.String())

	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"milestone", "create", "-R", "alice/project", "--title", "Ship", "--description", "x", "--description-file", file, "--no-input",
	})))
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"milestone", "edit", "4", "-R", "alice/project", "--editor", "--description", "x", "--no-input",
	})))
}

func TestMilestoneEditEditorSeedsDescription(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/milestones":
			writeFirstPage(w, r, `[{"id":4,"title":"Ship","description":"current notes","state":"open"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/milestones/4":
			_, _ = w.Write([]byte(`{"id":4,"title":"Ship","description":"current notes","state":"open"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/milestones/4":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "edited notes", body["description"])
			_, _ = w.Write([]byte(`{"id":4,"title":"Ship","description":"edited notes","state":"open"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	editor, path := newDraftEditor(t, "edited notes")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "edit", "4", "-R", "alice/project", "--editor", "--json=description", "--no-input"}))
	require.Equal(t, "current notes", editor.initial)
	require.JSONEq(t, `{"description":"edited notes"}`, out.String())
	requireDraft(t, path, false)
}

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
	// --by-name only says how to read the argument, so the form still opens.
	heading = ""
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "edit", "v1.0", "--by-name", "-R", "alice/project"}))
	require.NotEmpty(t, heading)
}

type milestoneFormEditor func(string, terminal.MilestoneFormValues) terminal.MilestoneFormValues

func (f milestoneFormEditor) EditMilestone(_ context.Context, heading string, current terminal.MilestoneFormValues) (terminal.MilestoneFormValues, error) {
	return f(heading, current), nil
}
