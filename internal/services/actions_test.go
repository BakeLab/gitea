// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestDispatchSendsOneRequest(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnprocessableEntity, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			dispatches := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dispatches++
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/v1/repos/owner/repo/actions/workflows/ci.yml/dispatches", r.URL.Path)
				require.Equal(t, "true", r.URL.Query().Get("return_run_details"))
				var payload struct {
					Ref    string            `json:"ref"`
					Inputs map[string]string `json:"inputs"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, "main", payload.Ref)
				require.Equal(t, map[string]string{"mode": "fast"}, payload.Inputs)
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{"workflow_run_id":11,"html_url":"https://gitea.test/owner/repo/actions/runs/11"}`))
				}
			}))
			result, err := (Actions{Client: client, Repository: "owner/repo"}).Dispatch(context.Background(), "ci.yml", "main", map[string]string{"mode": "fast"})
			require.Equal(t, 1, dispatches)
			if status == http.StatusOK {
				require.NoError(t, err)
				require.Equal(t, DispatchResult{ID: 11, URL: "https://gitea.test/owner/repo/actions/runs/11"}, result)
				return
			}
			var apiErr *transport.APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, status, apiErr.Status)
			require.Zero(t, result)
		})
	}
}

func TestWorkflowsMapDefinitions(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/workflows":
			_, _ = w.Write([]byte(`{"total_count":1,"workflows":[{"id":"ci.yml","name":"CI","path":".gitea/workflows/ci.yml","state":"active","badge_url":"excluded"}]}`))
		case "/api/v1/repos/owner/repo/actions/workflows/ci.yml":
			_, _ = w.Write([]byte(`{"id":"ci.yml","name":"CI","path":".gitea/workflows/ci.yml","state":"disabled_manually"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	items, err := service.Workflows(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Workflow{{ID: "ci.yml", Name: "CI", Path: ".gitea/workflows/ci.yml", State: "active"}}, items)
	item, err := service.Workflow(context.Background(), "ci.yml")
	require.NoError(t, err)
	require.Equal(t, "disabled_manually", item.State)
}

func TestWatchReportsEveryPollWithJobsUntilTheRunCompletes(t *testing.T) {
	polls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[{"id":"ci.yml","name":"CI"}]}`))
		case "/api/v1/repos/owner/repo/actions/runs/1":
			polls++
			if polls < 3 {
				_, _ = w.Write([]byte(`{"id":1,"path":"ci.yml@refs/heads/main","status":"in_progress"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":1,"path":"ci.yml@refs/heads/main","status":"completed","conclusion":"failure"}`))
		case "/api/v1/repos/owner/repo/actions/runs/1/jobs":
			_, _ = w.Write([]byte(`{"jobs":[{"id":2,"name":"build","status":"in_progress"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	var updates []string
	service := Actions{Client: client, Repository: "owner/repo"}
	final, err := service.Watch(context.Background(), 1, time.Millisecond, func(run Run) error {
		updates = append(updates, run.Workflow+" "+run.Status+" "+run.Jobs[0].Name)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"CI in_progress build", "CI in_progress build", "CI completed build"}, updates)
	require.False(t, SuccessfulRun(final))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.Watch(ctx, 1, time.Millisecond, nil)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestActionMutationsUseTheirEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		method string
		call   func(Actions) error
	}{
		{"enable", "/api/v1/repos/owner/repo/actions/workflows/ci.yml/enable", http.MethodPut, func(s Actions) error {
			return s.SetWorkflowEnabled(context.Background(), "ci.yml", true)
		}},
		{"disable", "/api/v1/repos/owner/repo/actions/workflows/ci.yml/disable", http.MethodPut, func(s Actions) error {
			return s.SetWorkflowEnabled(context.Background(), "ci.yml", false)
		}},
		{"rerun", "/api/v1/repos/owner/repo/actions/runs/7/rerun", http.MethodPost, func(s Actions) error {
			return s.Rerun(context.Background(), 7, 0, false)
		}},
		{"rerun-failed", "/api/v1/repos/owner/repo/actions/runs/7/rerun-failed-jobs", http.MethodPost, func(s Actions) error {
			return s.Rerun(context.Background(), 7, 0, true)
		}},
		{"delete", "/api/v1/repos/owner/repo/actions/runs/7", http.MethodDelete, func(s Actions) error {
			return s.DeleteRun(context.Background(), 7)
		}},
		{"cancel", "/api/v1/repos/owner/repo/actions/runs/7/cancel", http.MethodPost, func(s Actions) error {
			run, err := s.CancelRun(context.Background(), 7, false)
			require.EqualValues(t, 7, run.ID)
			return err
		}},
		{"force-cancel", "/api/v1/repos/owner/repo/actions/runs/7/force-cancel", http.MethodPost, func(s Actions) error {
			_, err := s.CancelRun(context.Background(), 7, true)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutations++
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, tc.path, r.URL.Path)
				if tc.method == http.MethodPost {
					_, _ = w.Write([]byte(`{"id":7,"status":"waiting"}`))
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			service := Actions{Client: client, Repository: "owner/repo"}
			require.NoError(t, tc.call(service))
			require.Equal(t, 1, mutations)
		})
	}
}

func TestTerminalActionsRecognizeGiteaAndLegacyStatuses(t *testing.T) {
	for _, status := range []string{"completed", "success", "failure", "canceled", "cancelled", "skipped"} { //nolint:misspell // Gitea API spelling.
		require.True(t, completedActionStatus(status), status)
	}
	for _, status := range []string{"queued", "waiting", "pending", "running", "in_progress", "cancelling", "blocked", "unknown"} { //nolint:misspell // Gitea API spelling.
		require.False(t, completedActionStatus(status), status)
		require.False(t, SuccessfulRun(Run{Status: status, Conclusion: "success"}), status)
	}
	require.True(t, SuccessfulRun(Run{Status: "completed", Conclusion: "success"}))
	require.True(t, SuccessfulRun(Run{Status: "success"}))
	require.False(t, SuccessfulRun(Run{Status: "completed", Conclusion: "failure"}))
}

func TestRunFiltersEncodeServerQueriesAndRejectInvalidStatus(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/owner/repo/actions/workflows/ci.yml/runs", r.URL.Path)
		require.Equal(t, []string{"queued", "failure"}, r.URL.Query()["status"])
		require.Equal(t, "feature/a b", r.URL.Query().Get("branch"))
		require.Equal(t, "push", r.URL.Query().Get("event"))
		require.Equal(t, "alice", r.URL.Query().Get("actor"))
		require.Equal(t, "abc123", r.URL.Query().Get("head_sha"))
		_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	opts := RunListOptions{Limit: 20, Page: 2, Branch: "feature/a b", Event: "push", Actor: "alice", Commit: "abc123", Workflow: "ci.yml", Statuses: []string{"queued", "failure"}}
	items, err := service.Runs(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, items)
	require.Equal(t, 1, calls)
	opts.Statuses = []string{"unknown"}
	_, err = service.Runs(context.Background(), opts)
	require.ErrorContains(t, err, "unsupported run status")
	require.Equal(t, 1, calls)
}

func TestRunTimeFilteringPaginatesMatchingResults(t *testing.T) {
	until := time.Date(2028, 1, 2, 0, 0, 0, 0, time.UTC)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":4,"created_at":"2028-01-03T00:00:00Z"},{"id":3,"created_at":"2028-01-01T12:00:00Z"}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":2,"created_at":"2028-01-01T00:00:00Z"}]}`))
		default:
			_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
		}
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	items, err := service.Runs(context.Background(), RunListOptions{Limit: 1, Page: 2, Until: &until})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 2, items[0].ID)
}

func TestRunTimeFilteringUsesCreationTimeAndStopsAtOlderRuns(t *testing.T) {
	since := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	pages := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		_, _ = w.Write([]byte(`{"workflow_runs":[
			{"id":3,"status":"queued","created_at":"2028-01-02T00:00:00Z","started_at":"1970-01-01T00:00:00Z","completed_at":"1970-01-01T00:00:00Z"},
			{"id":2,"status":"completed","created_at":"2028-01-01T00:00:00Z","started_at":"2028-01-01T00:00:05Z","completed_at":"2028-01-01T00:01:00Z"},
			{"id":1,"status":"completed","created_at":"2027-12-31T00:00:00Z","started_at":"2027-12-31T00:00:05Z"}]}`))
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	items, err := service.Runs(context.Background(), RunListOptions{Limit: 30, Page: 1, Since: &since})
	require.NoError(t, err)
	require.Equal(t, 1, pages)
	require.Len(t, items, 2)
	require.EqualValues(t, 3, items[0].ID)
	require.Nil(t, items[0].StartedAt)
	require.Nil(t, items[0].CompletedAt)
	require.Equal(t, "2028-01-01T00:00:05Z", *items[1].StartedAt)
	data, err := json.Marshal(items[0])
	require.NoError(t, err)
	require.Contains(t, string(data), `"started_at":null,"completed_at":null`)
}

func TestRunTimeFilteringStopsAtScanCap(t *testing.T) {
	until := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	pages := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
		run := `{"id":1,"created_at":"2028-12-31T00:00:00Z"}`
		_, _ = w.Write([]byte(`{"workflow_runs":[` + strings.Repeat(run+",", 99) + run + `]}`))
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	_, err := service.Runs(context.Background(), RunListOptions{Limit: 1, Page: 1, Until: &until})
	require.ErrorIs(t, err, ErrRunScanExhausted)
	require.Equal(t, maxRunScanPages, pages)
}

func TestFindWorkflowByFileStemPathOrName(t *testing.T) {
	workflows := []Workflow{
		{ID: "ci.yml", Name: "CI", Path: ".gitea/workflows/ci.yml"},
		{ID: "nightly.yaml", Name: "Nightly build", Path: ".gitea/workflows/nightly.yaml"},
		{ID: "deploy.yml", Name: "Deploy", Path: ".gitea/workflows/deploy.yml"},
		{ID: "deploy.yaml", Name: "Deploy staging", Path: ".gitea/workflows/deploy.yaml"},
	}
	for raw, want := range map[string]string{
		"ci.yml": "ci.yml", "ci": "ci.yml", "CI": "ci.yml", ".gitea/workflows/nightly.yaml": "nightly.yaml",
		"nightly build": "nightly.yaml", "Deploy": "deploy.yml", "deploy.yaml": "deploy.yaml",
	} {
		workflow, err := FindWorkflow(workflows, raw, "alice/x")
		require.NoError(t, err, raw)
		require.Equal(t, want, workflow.ID, raw)
	}
	_, err := FindWorkflow(workflows, "deploy", "alice/x")
	require.EqualError(t, err, `workflow "deploy" is ambiguous in alice/x (deploy.yml, deploy.yaml); pass the file name`)
	_, err = FindWorkflow(workflows, "release", "alice/x")
	require.EqualError(t, err, `workflow "release" not found in alice/x`)
}

func TestDispatchInputsReadTheWorkflowDispatchTrigger(t *testing.T) {
	for _, tc := range []struct {
		name, definition string
		want             []WorkflowInput
		err              error
	}{
		{"event name", "on: workflow_dispatch\n", []WorkflowInput{}, nil},
		{"event list", "on: [push, workflow_dispatch]\n", []WorkflowInput{}, nil},
		{"trigger without inputs", "on:\n  workflow_dispatch:\n", []WorkflowInput{}, nil},
		{"inputs in order", `on:
  workflow_dispatch:
    inputs:
      target:
        description: Deploy target
        required: true
        default: staging
        type: choice
        options: [staging, production]
      dry_run:
        type: boolean
        default: true
`, []WorkflowInput{
			{Name: "target", Description: "Deploy target", Type: "choice", Default: "staging", Required: true, Options: []string{"staging", "production"}},
			{Name: "dry_run", Type: "boolean", Default: "true"},
		}, nil},
		{"no trigger", "on:\n  push:\n", nil, ErrNoDispatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs, err := DispatchInputs([]byte(tc.definition))
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.want, inputs)
		})
	}
}

func TestWorkflowFileTellsAMissingRefFromAMissingFile(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/owner/repo/contents/.gitea/workflows/ci.yml", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		ref := r.URL.Query().Get("ref")
		if ref == "gone" {
			_, _ = fmt.Fprintf(w, `{"message":"object does not exist [id: %s, rel_path: ]"}`, ref)
			return
		}
		_, _ = w.Write([]byte(`{"message":"object does not exist [id: 0123abc, rel_path: .gitea/workflows/ci.yml]"}`))
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	workflow := Workflow{ID: "ci.yml", Path: ".gitea/workflows/ci.yml"}
	_, err := service.WorkflowFile(context.Background(), workflow, "gone")
	require.EqualError(t, err, `ref "gone" not found in owner/repo`)
	_, err = service.WorkflowFile(context.Background(), workflow, "main")
	require.EqualError(t, err, "workflow file .gitea/workflows/ci.yml not found in owner/repo on main")
	_, err = service.WorkflowFile(context.Background(), workflow, "")
	require.EqualError(t, err, "workflow file .gitea/workflows/ci.yml not found in owner/repo")
}

func TestDispatchFallsBackToATagRef(t *testing.T) {
	var refs []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Ref string `json:"ref"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		refs = append(refs, payload.Ref)
		if payload.Ref == "refs/tags/v2" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"workflow does not have a workflow_dispatch trigger"}`))
			return
		}
		if payload.Ref == "v1.0" || payload.Ref == "v2" || strings.HasSuffix(payload.Ref, "nope") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"ref \"` + payload.Ref + `\" not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"workflow_run_id":12}`))
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	result, err := service.Dispatch(context.Background(), "ci.yml", "v1.0", nil)
	require.NoError(t, err)
	require.Equal(t, int64(12), result.ID)
	_, err = service.Dispatch(context.Background(), "ci.yml", "nope", nil)
	var missing *NotFoundError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "ref", missing.Kind)
	_, err = service.Dispatch(context.Background(), "ci.yml", "v2", nil)
	require.ErrorContains(t, err, "workflow does not have a workflow_dispatch trigger")
	require.NotErrorAs(t, err, &missing)
	require.Equal(t, []string{"v1.0", "refs/tags/v1.0", "nope", "refs/tags/nope", "v2", "refs/tags/v2"}, refs)
}

func TestRunAndWorkflowListsNameAMissingRepository(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	service := Actions{Client: client, Repository: "alice/missing"}
	_, err := service.Runs(context.Background(), RunListOptions{Limit: 30, Page: 1})
	require.EqualError(t, err, "repository alice/missing not found")
	_, err = service.Workflows(context.Background())
	require.EqualError(t, err, "repository alice/missing not found")
}

func TestRunsAreNamedAfterTheirWorkflows(t *testing.T) {
	workflows := true
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/workflows":
			if !workflows {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"workflows":[{"id":"ci.yml","name":"CI"},{"id":"lint.yml"}]}`))
		case "/api/v1/repos/owner/repo/actions/runs":
			require.Equal(t, "main", r.URL.Query().Get("branch"))
			_, _ = w.Write([]byte(`{"total_count":3,"workflow_runs":[{"id":1,"path":"ci.yml@refs/heads/main"},{"id":2,"path":"lint.yml@refs/heads/main"},{"id":3,"path":"gone.yaml@refs/heads/main"}]}`))
		case "/api/v1/repos/owner/repo/actions/runs/1":
			_, _ = w.Write([]byte(`{"id":1,"path":"ci.yml@refs/heads/main"}`))
		case "/api/v1/repos/owner/repo/actions/runs/1/jobs":
			_, _ = w.Write([]byte(`{"jobs":[{"id":2,"name":"build"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	page, err := service.RunsPage(context.Background(), RunListOptions{Branch: "main", Limit: 3, Page: 1})
	require.NoError(t, err)
	names := make([]string, len(page.Items))
	for i, run := range page.Items {
		names[i] = run.Workflow
	}
	require.Equal(t, []string{"CI", "lint", "gone"}, names, "a workflow without a name or listing keeps its file stem")
	run, err := service.RunDetails(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "CI", run.Workflow)
	require.Equal(t, "build", run.Jobs[0].Name)

	returned := Run{ID: 1, Workflow: "ci", WorkflowPath: "ci.yml"}
	require.Equal(t, "CI", service.NameRun(context.Background(), returned).Workflow)
	workflows = false
	require.Equal(t, returned, service.NameRun(context.Background(), returned), "a run stays as it is when the workflows cannot be listed")
	_, err = service.RunDetails(context.Background(), 1)
	require.Error(t, err)
}

func TestWorkflowAtFindsFilesOfTheRef(t *testing.T) {
	ci := base64.StdEncoding.EncodeToString([]byte("name: Build\non: push\n"))
	reads := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := r.URL.Query().Get("ref")
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[{"id":"ci.yml","name":"CI","path":".github/workflows/ci.yml","state":"active"}]}`))
		case "/api/v1/repos/owner/repo/contents/.gitea/workflows":
			w.WriteHeader(http.StatusNotFound)
			if ref == "gone" {
				_, _ = w.Write([]byte(`{"message":"object does not exist [id: gone, rel_path: ]"}`))
			}
		case "/api/v1/repos/owner/repo/contents/.github/workflows":
			if ref == "empty" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`[{"name":"ci.yml","type":"file","path":".github/workflows/ci.yml","html_url":"https://gitea.test/ci.yml"},{"name":"new.yaml","type":"file","path":".github/workflows/new.yaml"},{"name":"README.md","type":"file","path":".github/workflows/README.md"},{"name":"sub.yml","type":"dir","path":".github/workflows/sub.yml"}]`))
		case "/api/v1/repos/owner/repo/contents/.github/workflows/ci.yml":
			reads++
			_, _ = w.Write([]byte(`{"content":"` + ci + `"}`))
		case "/api/v1/repos/owner/repo/contents/.github/workflows/new.yaml":
			reads++
			_, _ = w.Write([]byte(`{"content":""}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	workflow, err := service.WorkflowAt(context.Background(), "new", "feature")
	require.NoError(t, err)
	require.Equal(t, Workflow{ID: "new.yaml", Path: ".github/workflows/new.yaml"}, workflow)
	require.Zero(t, reads, "a file name matches without reading the files")
	workflow, err = service.WorkflowAt(context.Background(), "build", "feature")
	require.NoError(t, err)
	require.Equal(t, "ci.yml", workflow.ID)
	require.Equal(t, 2, reads)
	_, err = service.WorkflowAt(context.Background(), "sub", "feature")
	require.EqualError(t, err, `workflow "sub" not found in owner/repo on feature`)
	_, err = service.WorkflowAt(context.Background(), "ci", "gone")
	require.EqualError(t, err, `ref "gone" not found in owner/repo`)
	_, err = service.WorkflowAt(context.Background(), " ci ", "empty")
	require.EqualError(t, err, `workflow "ci" not found in owner/repo on empty`)

	workflow, err = service.FindWorkflowAt(context.Background(), "CI", "feature")
	require.NoError(t, err)
	require.Equal(t, Workflow{ID: "ci.yml", Name: "CI", Path: ".github/workflows/ci.yml", State: "active", URL: "https://gitea.test/ci.yml"}, workflow, "a listed workflow keeps its name and state")
	workflow, err = service.FindWorkflowAt(context.Background(), "new.yaml", "feature")
	require.NoError(t, err)
	require.Equal(t, "new.yaml", workflow.ID, "a workflow only on the ref is found there")
	workflow, err = service.FindWorkflowAt(context.Background(), "ci", "")
	require.NoError(t, err)
	require.Empty(t, workflow.URL)
}
