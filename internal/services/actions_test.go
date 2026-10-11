// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitea.dev/tea/internal/transport"
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

func TestWatchChangesAndFailureConclusion(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			_, _ = w.Write([]byte(`{"id":1,"status":"in_progress"}`))
		} else {
			_, _ = w.Write([]byte(`{"id":1,"status":"completed","conclusion":"failure"}`))
		}
	}))
	defer server.Close()
	changes := []Run{}
	service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
	final, err := service.Watch(context.Background(), 1, time.Millisecond, func(run Run) error { changes = append(changes, run); return nil })
	require.NoError(t, err)
	require.Len(t, changes, 2)
	require.False(t, SuccessfulRun(final))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.Watch(ctx, 1, time.Millisecond, nil)
	require.True(t, errors.Is(err, context.Canceled))
}

func TestRunCancellationPreservesHistory(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			mutations := 0
			action := "cancel"
			if force {
				action = "force-cancel"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutations++
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/v1/repos/owner/repo/actions/runs/7/"+action, r.URL.Path)
				_, _ = w.Write([]byte(`{"id":7,"status":"completed","conclusion":"cancelled"}`)) //nolint:misspell // Gitea API spelling.
			}))
			defer server.Close()
			service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
			result, err := service.CancelRun(context.Background(), 7, force)
			require.NoError(t, err)
			require.EqualValues(t, 7, result.ID)
			require.Equal(t, "cancelled", result.Conclusion) //nolint:misspell // Gitea API spelling.
			require.Equal(t, 1, mutations)
		})
	}
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
			return s.Rerun(context.Background(), 7, false)
		}},
		{"rerun-failed", "/api/v1/repos/owner/repo/actions/runs/7/rerun-failed-jobs", http.MethodPost, func(s Actions) error {
			return s.Rerun(context.Background(), 7, true)
		}},
		{"delete", "/api/v1/repos/owner/repo/actions/runs/7", http.MethodDelete, func(s Actions) error {
			return s.DeleteRun(context.Background(), 7)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mutations++
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, tc.path, r.URL.Path)
				if tc.name == "rerun" {
					w.WriteHeader(http.StatusCreated)
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/owner/repo/actions/workflows/ci.yml/runs", r.URL.Path)
		require.Equal(t, []string{"queued", "failure"}, r.URL.Query()["status"])
		require.Equal(t, "feature/a b", r.URL.Query().Get("branch"))
		require.Equal(t, "push", r.URL.Query().Get("event"))
		require.Equal(t, "alice", r.URL.Query().Get("actor"))
		require.Equal(t, "abc123", r.URL.Query().Get("head_sha"))
		_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
	}))
	defer server.Close()
	service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
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
	since := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"started_at":"2027-12-31T00:00:00Z"},{"id":2,"started_at":"2028-01-01T00:00:00Z"}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":3,"started_at":"2028-01-02T00:00:00Z"}]}`))
		default:
			_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
		}
	}))
	defer server.Close()
	service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
	items, err := service.Runs(context.Background(), RunListOptions{Limit: 1, Page: 2, Since: &since})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 3, items[0].ID)
}

func TestRunTimeFilteringStopsAtScanCap(t *testing.T) {
	since := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"started_at":"2027-12-31T00:00:00Z"}]}`))
	}))
	defer server.Close()
	service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
	_, err := service.Runs(context.Background(), RunListOptions{Limit: 1, Page: 1, Since: &since})
	require.ErrorContains(t, err, "narrow the search")
	require.Equal(t, maxRunScanPages, pages)
}
