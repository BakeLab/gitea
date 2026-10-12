// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullMilestoneClearUsesIssueEndpoint(t *testing.T) {
	for _, failCompletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial failure"}[failCompletion], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					assert.Equal(t, http.MethodPatch, r.Method)
					assert.Equal(t, "/api/v1/repos/owner/repo/issues/1", r.URL.Path)
					data, _ := io.ReadAll(r.Body)
					cleared, body, unset := int64(0), "", true
					assertIssueEdit(t, gitea.EditIssueOption{Milestone: &cleared, Body: &body, RemoveDeadline: &unset}, data)
					_, _ = w.Write([]byte("{\"number\":1}"))
					return
				}
				assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1", r.URL.Path)
				assert.Equal(t, http.MethodGet, r.Method)
				if failCompletion {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_, _ = w.Write([]byte("{\"number\":1,\"title\":\"Title\"}"))
			}))
			defer server.Close()
			cleared, body := int64(0), ""
			clearDeadline := true
			item, err := (Pulls{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}).Edit(context.Background(), 1, PullEdit{Milestone: &cleared, Body: &body, RemoveDeadline: &clearDeadline})
			if failCompletion {
				require.ErrorContains(t, err, "issue fields were updated")
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 1, item.Number)
			}
			require.Equal(t, 2, calls)
		})
	}
}

func TestListOpenPagesPastSmallServerLimits(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"number":1,"head":{"ref":"Feature"}}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"number":2,"head":{"ref":"feature"}}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	service := Pulls{Client: client, Repository: "alice/project"}
	items, err := service.ListOpen(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "feature", items[1].Head)
}

func TestListOpenRejectsEndlessPagination(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		_, _ = w.Write([]byte(`[{"number":1,"head":{"ref":"main"}}]`))
	}))
	_, err := (Pulls{Client: client, Repository: "alice/project"}).ListOpen(context.Background())
	require.ErrorContains(t, err, "exceeded")
}

func TestPullFiltersAreEncodedBeforeListing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		query := r.URL.Query()
		assert.Equal(t, "alice", query.Get("poster"))
		assert.Equal(t, "feature/topic", query.Get("base_branch"))
		assert.Equal(t, "recentupdate", query.Get("sort"))
		assert.Equal(t, "9", query.Get("milestone"))
		assert.Equal(t, []string{"10", "11"}, query["labels"])
		assert.Equal(t, "2", query.Get("page"))
		_, _ = w.Write([]byte(`[{"number":7,"user":{"login":"alice"},"mergeable":true,"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T12:00:00Z"}]`))
	}))
	defer server.Close()
	service := Pulls{Client: transport.New(server.URL, "token", false), Repository: "alice/project"}
	options := PullListOptions{State: "open", Limit: 30, Page: 2, Author: "alice", Base: "feature/topic", Sort: "recentupdate", Milestone: 9, Labels: []int64{10, 11}}
	items, _, err := service.List(context.Background(), options)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "alice", items[0].Author)
	require.Equal(t, "2026-10-02T12:00:00Z", items[0].UpdatedAt)
	require.True(t, items[0].Mergeable)
}

func TestPullTemplateLookup(t *testing.T) {
	for _, tc := range []struct {
		name, want, wantErr string
		status              map[string]int
		requests            int
	}{
		{"root first", "template", "", map[string]int{"PULL_REQUEST_TEMPLATE.md": http.StatusOK, ".gitea/pull_request_template.md": http.StatusOK}, 1},
		{"uppercase before lowercase", "template", "", map[string]int{".gitea/PULL_REQUEST_TEMPLATE.md": http.StatusOK}, 3},
		{"later match", "template", "", map[string]int{".github/pull_request_template.md": http.StatusOK}, 6},
		{"docs is not searched", "", "", map[string]int{"docs/pull_request_template.md": http.StatusOK}, 6},
		{"none", "", "", nil, 6},
		{"server error", "", "500", map[string]int{".gitea/pull_request_template.md": http.StatusInternalServerError}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				assert.Equal(t, "release/1.0", r.URL.Query().Get("ref"))
				status, ok := tc.status[strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/raw/")]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("template"))
			}))
			defer server.Close()
			got, err := (Pulls{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}).Template(context.Background(), "release/1.0")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.requests, requests)
		})
	}
}

func TestPullMergeExplainsRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, pull, want string
	}{
		{"conflicting files", `{"state":"open","mergeable":false}`, "pull request #3 has conflicting files or is otherwise not mergeable"},
		{"already merged", `{"state":"closed","merged":true}`, "pull request #3 has already been merged"},
		{"closed", `{"state":"closed"}`, "pull request #3 is closed"},
		{"draft", `{"state":"open","draft":true}`, "pull request #3 is a draft"},
		{"refused while mergeable", `{"state":"open","mergeable":true}`, "Please try again later"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3/merge":
					w.WriteHeader(http.StatusMethodNotAllowed)
					_, _ = w.Write([]byte(`{"message":"Please try again later"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3":
					_, _ = w.Write([]byte(tc.pull))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			err := (Pulls{Client: client, Repository: "owner/repo"}).Merge(context.Background(), 3, MergeOptions{Method: "merge"})
			require.ErrorContains(t, err, tc.want)
			var apiErr *transport.APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusMethodNotAllowed, apiErr.Status)
		})
	}
}

func TestIssueAndPullListsFillPagesCappedByServer(t *testing.T) {
	// The server caps pages at two items (MAX_RESPONSE_ITEMS=2) and holds seven.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size := min(limit, 2)
		numbers := []string{}
		for n := (page-1)*size + 1; n <= min(page*size, 7); n++ {
			numbers = append(numbers, `{"number":`+strconv.Itoa(n)+`}`)
		}
		w.Header().Set("X-Total-Count", "7")
		_, _ = w.Write([]byte("[" + strings.Join(numbers, ",") + "]"))
	}))
	issues, more, err := (Issues{Client: client, Repository: "owner/repo"}).List(context.Background(), ListOptions{State: "open", Limit: 3, Page: 2})
	require.NoError(t, err)
	require.Len(t, issues, 3)
	require.EqualValues(t, 4, issues[0].Number)
	require.True(t, more)
	pulls, more, err := (Pulls{Client: client, Repository: "owner/repo"}).List(context.Background(), PullListOptions{State: "open", Limit: 3, Page: 3})
	require.NoError(t, err)
	require.Len(t, pulls, 1)
	require.EqualValues(t, 7, pulls[0].Number)
	require.False(t, more)
}

func TestPullMergeSubmitsTheCurrentHead(t *testing.T) {
	var merged map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"state":"open","mergeable":true,"head":{"sha":"abc123"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3/merge":
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&merged))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	err := (Pulls{Client: client, Repository: "owner/repo"}).Merge(context.Background(), 3, MergeOptions{Method: "squash", Title: "Squashed"})
	require.NoError(t, err)
	require.Equal(t, "squash", merged["Do"])
	require.Equal(t, "Squashed", merged["MergeTitleField"])
	require.Equal(t, "abc123", merged["head_commit_id"])
	require.Equal(t, false, merged["delete_branch_after_merge"])
}

func TestPullMergeStopsWhenThePullIsUnreadable(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusNotFound)
	}))
	err := (Pulls{Client: client, Repository: "owner/repo"}).Merge(context.Background(), 3, MergeOptions{Method: "merge"})
	var apiErr *transport.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusNotFound, apiErr.Status)
}

func TestPullEditSendsOnlySuppliedPullFields(t *testing.T) {
	var edited map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/v1/repos/owner/repo/pulls/4", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&edited))
		_, _ = w.Write([]byte(`{"number":4,"state":"closed","base":{"ref":"release"}}`))
	}))
	title, state, base, allow := "Renamed", "closed", "release", false
	assignees := []string{}
	item, err := (Pulls{Client: client, Repository: "owner/repo"}).Edit(context.Background(), 4, PullEdit{
		Title: &title, State: &state, Base: &base, AllowMaintainerEdit: &allow, Assignees: &assignees,
	})
	require.NoError(t, err)
	require.Equal(t, "closed", item.State)
	require.Equal(t, "release", item.Base)
	require.Equal(t, "Renamed", edited["title"])
	require.Equal(t, "closed", edited["state"])
	require.Equal(t, "release", edited["base"])
	require.Equal(t, false, edited["allow_maintainer_edit"])
	require.Equal(t, []any{}, edited["assignees"])
	require.Nil(t, edited["body"])
	require.Nil(t, edited["due_date"])
	require.Nil(t, edited["unset_due_date"])
	require.Nil(t, edited["labels"])
	require.EqualValues(t, 0, edited["milestone"])
}

func TestPullCreateSendsMaintainerEdits(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/repos/owner/repo/pulls", r.URL.Path)
		created := requestJSON(t, r)
		assert.Equal(t, false, created["allow_maintainer_edit"])
		assert.Equal(t, "feature", created["head"])
		_, _ = w.Write([]byte(`{"number":5,"user":{"login":"alice"},"head":{"ref":"feature","sha":"abc"},"base":{"ref":"main"}}`))
	}))
	allow := false
	item, err := (Pulls{Client: client, Repository: "owner/repo"}).Create(context.Background(), PullOptions{Title: "T", Base: "main", Head: "feature", AllowMaintainerEdit: &allow})
	require.NoError(t, err)
	require.EqualValues(t, 5, item.Number)
	require.Equal(t, "alice", item.Author)
	require.Equal(t, "feature", item.Head)
	require.Equal(t, "abc", item.HeadSHA)
}

func TestPullDiffReturnsThePatch(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/owner/repo/pulls/2.diff", r.URL.Path)
		_, _ = w.Write([]byte("diff --git a/x b/x\n"))
	}))
	diff, err := (Pulls{Client: client, Repository: "owner/repo"}).Diff(context.Background(), 2)
	require.NoError(t, err)
	defer diff.Close()
	data, err := io.ReadAll(diff)
	require.NoError(t, err)
	require.Equal(t, "diff --git a/x b/x\n", string(data))
}

func TestPullFromMapsServerFields(t *testing.T) {
	pull := decode[gitea.PullRequest](t, `{
		"id":10,"number":3,"title":"Feature","body":"Adds","state":"open","html_url":"https://gitea.test/alice/project/pulls/3",
		"user":{"login":"bob"},"assignees":[{"login":"carol"}],"requested_reviewers":[{"login":"dave"}],
		"requested_reviewers_teams":[{"name":"core"}],"labels":[{"id":1,"name":"bug"}],"milestone":{"id":2,"title":"v1"},
		"merged":false,"mergeable":true,"draft":true,"comments":4,
		"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T12:00:00+02:00","due_date":"2026-11-01T00:00:00Z",
		"base":{"ref":"main","repo":{"full_name":"alice/project"}},
		"head":{"label":"bob:feature","ref":"feature","sha":"abc","repo":{"full_name":"bob/project","clone_url":"https://gitea.test/bob/project.git","ssh_url":"git@gitea.test:bob/project.git","permissions":{"push":true}}}}`)
	deadline := "2026-11-01T00:00:00Z"
	require.Equal(t, Pull{
		Author: "bob", CreatedAt: "2026-10-01T12:00:00Z", UpdatedAt: "2026-10-02T12:00:00+02:00", Assignees: []string{"carol"},
		Milestone: &IssueMilestone{ID: 2, Title: "v1"}, Labels: []Label{{ID: 1, Name: "bug"}}, Deadline: &deadline,
		ID: 10, Number: 3, Title: "Feature", Body: "Adds", URL: "https://gitea.test/alice/project/pulls/3", State: "open",
		Mergeable: true, Draft: true, Base: "main", Head: "feature", HeadSHA: "abc", HeadRepo: "bob/project", HeadCanPush: true,
		HeadCloneURL: "https://gitea.test/bob/project.git", HeadSSHURL: "git@gitea.test:bob/project.git",
		Reviewers: []string{"dave"}, ReviewerTeams: []string{"core"}, Comments: 4,
	}, pullFrom(pull))
}

func TestPullFromFallsBackToBaseForDeletedHeads(t *testing.T) {
	pull := decode[gitea.PullRequest](t, `{"number":3,"head":{"label":"bob:feature","ref":"refs/pull/3/head"},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`)
	result := pullFrom(pull)
	require.True(t, result.HeadRemoteDeleted)
	require.Equal(t, "feature", result.Head)
	require.Equal(t, "alice/project", result.HeadRepo)
}

func TestPullFromToleratesMissingBranches(t *testing.T) {
	require.Equal(t, Pull{Number: 1, Assignees: []string{}, Labels: []Label{}, Reviewers: []string{}, ReviewerTeams: []string{}},
		pullFrom(decode[gitea.PullRequest](t, `{"number":1,"head":null,"base":null}`)))
}
