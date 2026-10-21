// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullMilestoneClearUsesIssueEndpoint(t *testing.T) {
	for _, failCompletion := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial failure"}[failCompletion], func(t *testing.T) {
			var calls []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if r.Method == http.MethodPatch {
					assert.Equal(t, "/api/v1/repos/owner/repo/issues/1", r.URL.Path)
					data, _ := io.ReadAll(r.Body)
					cleared, body, unset := int64(0), "", true
					assertIssueEdit(t, gitea.EditIssueOption{Milestone: &cleared, Body: &body, RemoveDeadline: &unset}, data)
					_, _ = w.Write([]byte("{\"number\":1}"))
					return
				}
				assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1", r.URL.Path)
				// The pull request is read ahead of an edit of several fields, and after it.
				if failCompletion && len(calls) > 1 {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_, _ = w.Write([]byte("{\"number\":1,\"title\":\"Title\",\"milestone\":{\"id\":2}}"))
			}))
			cleared, body := int64(0), ""
			clearDeadline := true
			item, err := (Pulls{Client: client, Repository: "owner/repo"}).Edit(context.Background(), 1, PullEdit{Milestone: &cleared, Body: &body, RemoveDeadline: &clearDeadline})
			if failCompletion {
				require.ErrorContains(t, err, "pull request #1 was edited, but reading it back failed: ")
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 1, item.Number)
			}
			require.Equal(t, []string{"GET /api/v1/repos/owner/repo/pulls/1", "PATCH /api/v1/repos/owner/repo/issues/1", "GET /api/v1/repos/owner/repo/pulls/1"}, calls)
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
		{"root first", "PULL_REQUEST_TEMPLATE.md", "", map[string]int{"PULL_REQUEST_TEMPLATE.md": http.StatusOK, ".gitea/pull_request_template.md": http.StatusOK}, 6},
		{"uppercase before lowercase", ".gitea/PULL_REQUEST_TEMPLATE.md", "", map[string]int{".gitea/PULL_REQUEST_TEMPLATE.md": http.StatusOK, ".gitea/pull_request_template.md": http.StatusOK}, 6},
		{"later match", ".github/pull_request_template.md", "", map[string]int{".github/pull_request_template.md": http.StatusOK}, 6},
		{"docs is not searched", "", "", map[string]int{"docs/pull_request_template.md": http.StatusOK}, 6},
		{"none", "", "", nil, 6},
		{"server error", "", "500", map[string]int{".gitea/pull_request_template.md": http.StatusInternalServerError}, 6},
		{"match before error", "pull_request_template.md", "", map[string]int{"pull_request_template.md": http.StatusOK, ".github/PULL_REQUEST_TEMPLATE.md": http.StatusInternalServerError}, 6},
		{"error before match", "", "500", map[string]int{"pull_request_template.md": http.StatusInternalServerError, ".github/PULL_REQUEST_TEMPLATE.md": http.StatusOK}, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, "release/1.0", r.URL.Query().Get("ref"))
				path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/raw/")
				status, ok := tc.status[path]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(path))
			}))
			defer server.Close()
			got, err := (Pulls{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}).Template(context.Background(), "release/1.0")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.requests, int(requests.Load()))
		})
	}
}

func TestPullTemplateProbesConcurrently(t *testing.T) {
	// Every probe only completes once all of them are in flight.
	var arrived sync.WaitGroup
	arrived.Add(len(pullTemplatePaths))
	allArrived := make(chan struct{})
	go func() { arrived.Wait(); close(allArrived) }()
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Done()
		select {
		case <-allArrived:
		case <-time.After(5 * time.Second):
			t.Errorf("%s did not run concurrently with the other template probes", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	got, err := (Pulls{Client: client, Repository: "owner/repo"}).Template(context.Background(), "main")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestPullMergeExplainsRefusal(t *testing.T) {
	mergeRecheckDelay = 0
	for _, tc := range []struct {
		name, pull, message, want string
		merges                    int
	}{
		{"conflicting files or checking", `{"state":"open","mergeable":false}`, "Please try again later", "pull request #3 has conflicting files, or Gitea is still checking whether it can be merged; retry in a moment", 2},
		{"not mergeable", `{"state":"open","mergeable":false}`, "PR is not ready to be merged", "pull request #3 has conflicting files or is otherwise not mergeable", 1},
		{"already merged", `{"state":"closed","merged":true}`, "", "pull request #3 has already been merged", 1},
		{"closed", `{"state":"closed"}`, "Please try again later", "pull request #3 is closed", 1},
		{"draft", `{"state":"open","draft":true}`, "Please try again later", "pull request #3 is a draft", 1},
		{"refused while mergeable", `{"state":"open","mergeable":true}`, "Please try again later", "pull request #3 cannot be merged yet: Gitea is still checking whether it can be merged; retry in a moment", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merges := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3/merge":
					merges++
					w.WriteHeader(http.StatusMethodNotAllowed)
					_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
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
			require.Equal(t, tc.merges, merges)
		})
	}
}

func TestPullMergeRetriesWhileGiteaChecksTheMergeability(t *testing.T) {
	mergeRecheckDelay = 0
	merges := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3":
			_, _ = w.Write([]byte(`{"number":3,"state":"open","mergeable":false,"head":{"sha":"abc123"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/3/merge":
			if merges++; merges == 1 {
				w.WriteHeader(http.StatusMethodNotAllowed)
				_, _ = w.Write([]byte(`{"message":"Please try again later"}`))
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	require.NoError(t, (Pulls{Client: client, Repository: "owner/repo"}).Merge(context.Background(), 3, MergeOptions{Method: "merge"}))
	require.Equal(t, 2, merges)
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

	// A head that moved after the user confirmed is not merged: Gitea compares it with the confirmed one.
	err = (Pulls{Client: client, Repository: "owner/repo"}).Merge(context.Background(), 3, MergeOptions{Method: "merge", HeadSHA: "confirmed"})
	require.NoError(t, err)
	require.Equal(t, "confirmed", merged["head_commit_id"])
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
		if r.Method == http.MethodGet {
			// An edit of several fields reads the pull request first.
			_, _ = w.Write([]byte(`{"number":4,"title":"Old","state":"open","base":{"ref":"main"}}`))
			return
		}
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

func TestPullCreateNamesTheForkOfAMissingHead(t *testing.T) {
	for _, test := range []struct {
		head, message string
		status        int
		want          string
	}{
		{"bob:nosuch", `ref \"nosuch\" does not exist`, http.StatusNotFound, `branch "nosuch" not found in the fork of owner/repo owned by bob`},
		{"nobody:x", "not found", http.StatusNotFound, `user or organization "nobody" not found`},
		{"carol:x", "the user carol does not have a fork of the base repository", http.StatusBadRequest, "fork of owner/repo owned by carol not found"},
		{"owner:nosuch", `ref \"nosuch\" does not exist`, http.StatusNotFound, `branch "nosuch" not found in owner/repo`},
		{"nosuch", `ref \"nosuch\" does not exist`, http.StatusNotFound, `branch "nosuch" not found in owner/repo`},
		// Gitea answers a missing repository with a bare "not found" too.
		{"feature", "not found", http.StatusNotFound, "repository owner/repo not found"},
		{"bob:feature", "not found", http.StatusNotFound, "repository owner/repo not found"},
	} {
		t.Run(test.head, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo" && !strings.HasPrefix(test.want, "repository") {
					_, _ = w.Write([]byte(`{"full_name":"owner/repo"}`))
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"message":"` + test.message + `"}`))
			}))
			_, err := (Pulls{Client: client, Repository: "owner/repo"}).Create(context.Background(), PullOptions{Title: "T", Base: "main", Head: test.head})
			require.EqualError(t, err, test.want)
		})
	}
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

func TestPullFromLeavesTheHeadRepositoryOfADeletedForkEmpty(t *testing.T) {
	pull := decode[gitea.PullRequest](t, `{"number":3,"head":{"label":"feature","ref":"refs/pull/3/head","repo_id":-1},
		"base":{"ref":"main","repo":{"full_name":"alice/project","clone_url":"https://example.test/alice/project.git"}}}`)
	result := pullFrom(pull)
	require.True(t, result.HeadRemoteDeleted)
	require.Equal(t, "feature", result.Head)
	require.Empty(t, result.HeadRepo, "the head is not in the base repository")
	require.Equal(t, "https://example.test/alice/project.git", result.HeadCloneURL, "refs/pull is fetched from the base")
	require.False(t, PullListOptions{Head: "alice:feature"}.matches(result))
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(data), `"head_repo":""`, "lists and views keep the same keys")
}

func TestPullFromPutsTheHeadOfAnAGitPullRequestInTheBase(t *testing.T) {
	pull := decode[gitea.PullRequest](t, `{"number":3,"head":{"label":"","ref":"refs/pull/3/head"},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`)
	require.Equal(t, "alice/project", pullFrom(pull).HeadRepo)
}

func TestPullFromKeepsTheForkOfADeletedHead(t *testing.T) {
	pull := decode[gitea.PullRequest](t, `{"number":3,
		"head":{"label":"bob:feature","ref":"refs/pull/3/head","repo":{"full_name":"bob/project","clone_url":"https://example.test/bob/project.git"}},
		"base":{"ref":"main","repo":{"full_name":"alice/project","clone_url":"https://example.test/alice/project.git","ssh_url":"git@example.test:alice/project.git"}}}`)
	result := pullFrom(pull)
	require.True(t, result.HeadRemoteDeleted)
	require.Equal(t, "bob/project", result.HeadRepo)
	require.Equal(t, "https://example.test/alice/project.git", result.HeadCloneURL, "refs/pull is fetched from the base")
	require.Equal(t, "git@example.test:alice/project.git", result.HeadSSHURL)
}

func TestPullFromKeepsAGitHeadsUndeleted(t *testing.T) {
	pull := decode[gitea.PullRequest](t, `{"number":3,"state":"open","head":{"label":"","ref":"refs/pull/3/head","repo":{"full_name":"alice/project"}},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`)
	result := pullFrom(pull)
	require.False(t, result.HeadRemoteDeleted)
	require.Empty(t, result.Head)
	require.Equal(t, "alice/project", result.HeadRepo)
}

func TestPullFromToleratesMissingBranches(t *testing.T) {
	require.Equal(t, Pull{Number: 1, Assignees: []string{}, Labels: []Label{}, Reviewers: []string{}, ReviewerTeams: []string{}},
		pullFrom(decode[gitea.PullRequest](t, `{"number":1,"head":null,"base":null}`)))
}

func TestPullClientFiltersStopReadingOncePageIsFull(t *testing.T) {
	requests := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		items := make([]string, 0, 50)
		for i := range 50 {
			items = append(items, `{"number":`+strconv.Itoa((page-1)*50+i+1)+`,"draft":true}`)
		}
		w.Header().Set("X-Total-Count", "5000")
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	page, err := (Pulls{Client: client, Repository: "alice/project"}).ListPage(context.Background(), PullListOptions{State: "open", Draft: true, Limit: 30, Page: 2})
	require.NoError(t, err)
	require.Equal(t, 2, requests)
	require.Len(t, page.Items, 30)
	require.EqualValues(t, 31, page.Items[0].Number)
	require.True(t, page.More)
	require.False(t, page.Counted)
}

func TestPullClientFiltersCountWhenEveryPullWasRead(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"number":1,"draft":true},{"number":2},{"number":3,"draft":true}]`))
	}))
	page, err := (Pulls{Client: client, Repository: "alice/project"}).ListPage(context.Background(), PullListOptions{State: "open", Draft: true, Limit: 2, Page: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.False(t, page.More)
	require.True(t, page.Counted)
	require.Equal(t, 2, page.Total)
}

func TestPullNoAssigneeKeepsUnassignedPulls(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.URL.Query().Get("assignee"))
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"number":1,"assignees":[{"login":"bob"}]},{"number":2},{"number":3,"assignees":[]}]`))
	}))
	page, err := (Pulls{Client: client, Repository: "alice/project"}).ListPage(context.Background(), PullListOptions{State: "open", NoAssignee: true, Limit: 30, Page: 1})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.EqualValues(t, 2, page.Items[0].Number)
	require.EqualValues(t, 3, page.Items[1].Number)
	require.True(t, page.Counted)
}

func TestPullErrorsNameTheMissingRepositoryOrPullRequest(t *testing.T) {
	for _, repository := range []string{"owner/repo", "owner/missing"} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/repos/owner/repo" {
				_, _ = w.Write([]byte(`{"full_name":"owner/repo"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}))
		want := "pull request #9 not found in owner/repo"
		if repository == "owner/missing" {
			want = "repository owner/missing not found"
		}
		pulls := Pulls{Client: client, Repository: repository}
		_, err := pulls.View(context.Background(), 9)
		require.EqualError(t, err, want)
		_, err = pulls.Diff(context.Background(), 9)
		require.EqualError(t, err, want)
		_, err = pulls.Files(context.Background(), 9)
		require.EqualError(t, err, want)
		_, err = (Reviews{Client: client, Repository: repository}).All(context.Background(), 9)
		require.EqualError(t, err, want)
		_, _, err = pulls.List(context.Background(), PullListOptions{State: "open", Limit: 10, Page: 1})
		require.EqualError(t, err, "repository "+repository+" not found")
	}
}

func TestCheckUsersLooksUpAssigneesByEmailButNotReviewers(t *testing.T) {
	var looked []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		looked = append(looked, strings.TrimPrefix(r.URL.Path, "/api/v1/users/"))
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"user redirect does not exist"}`))
	}))
	service := Pulls{Client: client, Repository: "owner/repo"}
	// Gitea looks a reviewer up by email, but takes assignees by name only.
	require.NoError(t, service.CheckUsers(context.Background(), nil, []string{"carol@example.test"}))
	require.Empty(t, looked)
	err := service.CheckUsers(context.Background(), []string{"carol@example.test"}, nil)
	require.EqualError(t, err, `user "carol@example.test" not found`)
	require.Equal(t, []string{"carol@example.test"}, looked)
}

// partialEditServer serves pull request 7 of alice/project and edits it in Gitea's order: each
// endpoint saves the title, the body, the assignees and the milestone, and the pull request
// endpoint then the base branch and whether maintainers can edit, and stops at the first
// refusal. Gitea leaves the assignees and the milestone out, and refuses the labels, for people
// who cannot write to the pull requests. It refuses assignee "private" with a 500, as for a user
// without access to a private repository, and assignee "blocked" with a 403.
type partialEditServer struct {
	t                         *testing.T
	writer, refuseMaintainers bool
	failIssue                 bool
	// readStatus answers the reads after the first with this status, when set.
	readStatus int
	// labelsStatus answers the label changes with this status, once Gitea stored them, when set.
	labelsStatus      int
	title, body, base string
	state             string
	milestone         int64
	maintainers       bool
	labels            []int64
	assignees         []string
	// reviewers are the users asked for a review.
	reviewers []string
	calls     []string
	reads     int
}

func (f *partialEditServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":` + strconv.Quote(message) + `}`))
	}
	var body struct {
		gitea.EditPullRequestOption
		Milestone *int64 `json:"milestone"`
	}
	if r.Method == http.MethodPatch {
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.title = cmp.Or(body.Title, f.title)
		if body.Body != nil {
			f.body = *body.Body
		}
		if f.failIssue && strings.HasSuffix(r.URL.Path, "/issues/7") {
			fail(http.StatusInternalServerError, "assignees could not be saved")
			return
		}
		if body.Assignees != nil && f.writer {
			for _, name := range body.Assignees {
				switch name {
				case "private":
					fail(http.StatusInternalServerError, "user private has no access to the repository")
					return
				case "blocked":
					fail(http.StatusForbidden, "user is blocked")
					return
				}
			}
			f.assignees = body.Assignees
		}
		// The pull request endpoint takes no zero milestone, which the SDK sends for none.
		if body.Milestone != nil && f.writer && (*body.Milestone != 0 || strings.HasSuffix(r.URL.Path, "/issues/7")) {
			f.milestone = *body.Milestone
		}
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/repos/alice/project/pulls/7":
		if f.reads++; f.readStatus != 0 && f.reads > 1 {
			fail(f.readStatus, "unavailable")
			return
		}
	case "GET /api/v1/repos/alice/project":
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
		return
	case "GET /api/v1/users/carol":
		_, _ = w.Write([]byte(`{"login":"carol"}`))
		return
	case "GET /api/v1/settings/api":
		_, _ = w.Write([]byte(`{}`))
		return
	case "GET /api/v1/repos/alice/project/pulls/7/reviews":
		reviews := []any{}
		for i, name := range f.reviewers {
			reviews = append(reviews, map[string]any{"id": i + 1, "state": "REQUEST_REVIEW", "user": map[string]any{"login": name}})
		}
		data, err := json.Marshal(reviews)
		require.NoError(f.t, err)
		writeFirstPage(w, r, string(data))
		return
	case "POST /api/v1/repos/alice/project/pulls/7/requested_reviewers":
		var body ReviewRequestInput
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		// Gitea answers only the requests it made: none for a reviewer it already asked.
		made := []any{}
		for _, name := range body.Users {
			if !slices.Contains(f.reviewers, name) {
				f.reviewers = append(f.reviewers, name)
				made = append(made, map[string]any{"state": "REQUEST_REVIEW", "user": map[string]any{"login": name}})
			}
		}
		_ = json.NewEncoder(w).Encode(made)
		return
	case "PATCH /api/v1/repos/alice/project/issues/7":
	case "PATCH /api/v1/repos/alice/project/pulls/7":
		if body.State != nil {
			f.state = string(*body.State)
		}
		if body.Base != "" {
			if body.Base != "main" && body.Base != "dev" {
				fail(http.StatusNotFound, "new base '"+body.Base+"' not exist")
				return
			}
			f.base = body.Base
		}
		if body.AllowMaintainerEdit != nil {
			if f.refuseMaintainers {
				fail(http.StatusForbidden, "SetAllowEdits: user is not the poster")
				return
			}
			f.maintainers = *body.AllowMaintainerEdit
		}
	case "PUT /api/v1/repos/alice/project/issues/7/labels":
		if !f.writer {
			fail(http.StatusForbidden, "write permission is required")
			return
		}
		var body struct {
			Labels []int64 `json:"labels"`
		}
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.labels = body.Labels
		if f.labelsStatus != 0 {
			fail(f.labelsStatus, "the labels were stored, but the answer failed")
			return
		}
		_, _ = w.Write([]byte(`[]`))
		return
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	pull := map[string]any{
		"number": 7, "title": f.title, "body": f.body, "state": cmp.Or(f.state, "open"), "base": map[string]any{"ref": f.base},
		"allow_maintainer_edit": f.maintainers, "labels": []any{}, "assignees": []any{},
	}
	if f.milestone != 0 {
		pull["milestone"] = map[string]any{"id": f.milestone}
	}
	for _, id := range f.labels {
		pull["labels"] = append(pull["labels"].([]any), map[string]any{"id": id})
	}
	for _, name := range f.assignees {
		pull["assignees"] = append(pull["assignees"].([]any), map[string]any{"login": name})
	}
	_ = json.NewEncoder(w).Encode(pull)
}

func TestPullEditNamesWhatGiteaSavedBeforeARefusal(t *testing.T) {
	title, dev, nosuch, no, milestone := "E-title", "dev", "nosuch", false, int64(4)
	private, blocked := []string{"private"}, []string{"blocked"}
	for _, tc := range []struct {
		name          string
		opts          PullEdit
		writer        bool
		saved, failed []string
		want          string
		base, title   string
		milestone     int64
		// skipped is set when the failure kept later requests from being sent.
		skipped bool
	}{
		{
			name: "maintainers refused after the base", writer: true,
			opts:  PullEdit{Base: &dev, AllowMaintainerEdit: &no},
			saved: []string{"the base branch"}, failed: []string{"whether maintainers can edit"},
			want: "the base branch was saved, but whether maintainers can edit was not changed; the edit failed: permission denied by the Gitea instance: SetAllowEdits: user is not the poster",
			base: "dev", title: "Old",
		},
		{
			name: "maintainers refused after the issue fields", writer: true,
			opts:   PullEdit{Title: &title, Labels: &[]int64{3}, Milestone: &milestone, Base: &dev, AllowMaintainerEdit: &no},
			saved:  []string{"the title", "the milestone", "the base branch", "the labels"},
			failed: []string{"whether maintainers can edit"},
			want:   "the title, the milestone, the base branch and the labels were saved, but whether maintainers can edit was not changed; the edit failed: ",
			base:   "dev", title: "E-title", milestone: 4,
		},
		{
			name: "missing base after the title", writer: true,
			opts:  PullEdit{Title: &title, Base: &nosuch},
			saved: []string{"the title"}, failed: []string{"the base branch"},
			want: `the title was saved, but the base branch was not changed; the edit failed: branch "nosuch" not found in alice/project`,
			base: "main", title: "E-title",
		},
		{
			name: "missing base alone after a milestone left out", writer: false,
			opts:  PullEdit{Title: &title, Milestone: &milestone, Base: &nosuch},
			saved: []string{"the title"}, failed: []string{"the milestone"},
			want: `the title was saved, but the milestone was not changed; changing the base branch failed: branch "nosuch" not found in alice/project`,
			base: "main", title: "E-title",
		},
		{
			name: "assignee without access after the title", writer: true,
			opts:  PullEdit{Title: &title, Assignees: &private},
			saved: []string{"the title"}, failed: []string{"the assignees"},
			want: "the title was saved, but the assignees were not changed; the edit failed: ",
			base: "main", title: "E-title",
		},
		{
			name: "assignee without access through the issue endpoint", writer: true,
			opts:  PullEdit{Title: &title, Assignees: &private, Milestone: &milestone},
			saved: []string{"the title"}, failed: []string{"the assignees", "the milestone"},
			want: "the title was saved, but the assignees and the milestone were not changed; the edit failed: ",
			base: "main", title: "E-title", skipped: true,
		},
		{
			name: "blocked assignee with a base", writer: true,
			opts:  PullEdit{Title: &title, Assignees: &blocked, Base: &dev},
			saved: []string{"the title"}, failed: []string{"the assignees", "the base branch"},
			want: "the title was saved, but the assignees and the base branch were not changed; the edit failed: permission denied by the Gitea instance: user is blocked",
			base: "main", title: "E-title",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &partialEditServer{t: t, writer: tc.writer, refuseMaintainers: true, title: "Old", base: "main", maintainers: true}
			item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, tc.opts)
			partial, ok := errors.AsType[*PartialEditError](err)
			require.True(t, ok, "%v", err)
			require.ErrorContains(t, err, tc.want)
			require.Equal(t, tc.saved, partial.Saved)
			require.Equal(t, tc.failed, partial.Failed)
			require.NotContains(t, err.Error(), "cannot write")
			require.Equal(t, tc.base, server.base)
			require.Equal(t, tc.title, server.title)
			require.Equal(t, tc.milestone, server.milestone)
			if tc.skipped {
				require.Zero(t, item.Number, "the pull request goes with the error only after every request")
				return
			}
			require.EqualValues(t, 7, item.Number, "the pull request as saved goes with the error")
			require.Equal(t, tc.base, item.Base)
			require.Equal(t, tc.title, item.Title)
		})
	}
}

func TestPullEditNamesNoChangeThatThePullRequestHeldAsSaved(t *testing.T) {
	// Gitea refuses the edit outright: the title that the pull request already had is not saved.
	server := &partialEditServer{t: t, writer: true, refuseMaintainers: true, title: "Same", base: "main", maintainers: true}
	title, no := "Same", false
	item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{Title: &title, AllowMaintainerEdit: &no})
	require.EqualError(t, err, "permission denied by the Gitea instance: SetAllowEdits: user is not the poster")
	require.NotErrorAs(t, err, new(*PartialEditError))
	require.Zero(t, item.Number)
}

func TestPullEditNamesNoneOfALeftOutMilestoneAsSaved(t *testing.T) {
	server := &partialEditServer{t: t, title: "Old", base: "main"}
	milestone, nosuch := int64(4), "nosuch"
	item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{Milestone: &milestone, Base: &nosuch})
	require.EqualError(t, err, `branch "nosuch" not found in alice/project`)
	require.NotErrorAs(t, err, new(*PartialEditError), "nothing was saved")
	require.Zero(t, item.Number)
}

func TestPullEditNamesWhatWasSavedAfterReviewRequests(t *testing.T) {
	server := &partialEditServer{t: t, writer: true, title: "Old", base: "main"}
	title, nosuch := "T", "nosuch"
	item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		Title: &title, Base: &nosuch, ReviewerChanges: &ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"carol"}}},
	})
	require.EqualError(t, err, `the review requests and the title were saved, but the base branch was not changed; the edit failed: branch "nosuch" not found in alice/project`)
	require.Equal(t, "T", item.Title)
}

func TestPullEditNamesNoReviewRequestThatWasAlreadyMadeAsSaved(t *testing.T) {
	server := &partialEditServer{t: t, writer: true, title: "Old", base: "main", reviewers: []string{"carol"}}
	title, nosuch := "T", "nosuch"
	_, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		Title: &title, Base: &nosuch, ReviewerChanges: &ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"carol"}}},
	})
	require.EqualError(t, err, `the title was saved, but the base branch was not changed; the edit failed: branch "nosuch" not found in alice/project`)
	require.Contains(t, server.calls, "POST /api/v1/repos/alice/project/pulls/7/requested_reviewers")
}

func TestChangesRequestsTellsAChangeFromANoOp(t *testing.T) {
	reviews := []Review{{Author: "carol", State: "REQUEST_REVIEW"}, {Author: "dave", State: "APPROVED"}, {Team: "core", State: "REQUEST_REVIEW"}}
	for _, tc := range []struct {
		changes ReviewRequestChanges
		want    bool
	}{
		{ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"Carol"}, Teams: []string{"core"}}}, false},
		{ReviewRequestChanges{Remove: ReviewRequestInput{Users: []string{"erin"}, Teams: []string{"docs"}}}, false},
		{ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"dave"}}}, true},
		{ReviewRequestChanges{Remove: ReviewRequestInput{Users: []string{"carol"}}}, true},
		{ReviewRequestChanges{Add: ReviewRequestInput{Teams: []string{"docs"}}}, true},
		{ReviewRequestChanges{Remove: ReviewRequestInput{Teams: []string{"Core"}}}, true},
		{ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"carol@example.test"}}}, true},
		{ReviewRequestChanges{Remove: ReviewRequestInput{Users: []string{"carol@example.test"}}}, true},
	} {
		require.Equal(t, tc.want, changesRequests(reviews, tc.changes), "%+v", tc.changes)
	}
}

func TestPullEditNamesLabelsThatGiteaStoredBeforeFailingAsSaved(t *testing.T) {
	server := &partialEditServer{t: t, writer: true, labelsStatus: http.StatusInternalServerError, title: "Old", base: "main"}
	title, nosuch := "C4", "nosuch"
	item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		Title: &title, Base: &nosuch, Labels: &[]int64{3},
	})
	partial, ok := errors.AsType[*PartialEditError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, []string{"the title", "the labels"}, partial.Saved)
	require.Empty(t, partial.Failed)
	require.ErrorContains(t, err, `the title and the labels were saved, but changing the base branch failed: branch "nosuch" not found in alice/project; the edit failed: `)
	require.NotContains(t, err.Error(), "changing the labels failed")
	require.Equal(t, []int64{3}, server.labels)
	require.Len(t, item.Labels, 1, "the pull request as read back goes with the error")
}

func TestPullEditCallsLabelsUnknownWhenTheReadBackPredatesThem(t *testing.T) {
	// The state is saved before the base is refused, which the read before the labels shows;
	// nothing read after the labels tells whether Gitea stored them before it failed.
	server := &partialEditServer{t: t, writer: true, labelsStatus: http.StatusBadGateway, title: "Old", base: "main"}
	closed, nosuch := "closed", "nosuch"
	_, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		State: &closed, Base: &nosuch, Labels: &[]int64{3},
	})
	partial, ok := errors.AsType[*PartialEditError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, []string{"the state"}, partial.Saved)
	require.ErrorContains(t, err, "; the labels may or may not have been saved: ")
	require.ErrorContains(t, err, "; gt pr view 7 shows the current state")
	require.NotContains(t, err.Error(), "changing the labels failed")
	require.NotContains(t, partial.Failed, "the labels")
}

func TestPullEditNamesUnsentLabelsAsNotChangedWhenTheReadBackFails(t *testing.T) {
	server := &partialEditServer{t: t, writer: true, failIssue: true, readStatus: http.StatusBadGateway, title: "Old", base: "main"}
	title, dev, milestone := "E-title", "dev", int64(4)
	_, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		Title: &title, Milestone: &milestone, Base: &dev, Labels: &[]int64{3},
	})
	partial, ok := errors.AsType[*PartialEditError](err)
	require.True(t, ok, "%v", err)
	require.Empty(t, partial.Saved)
	require.Equal(t, []string{"the base branch", "the labels"}, partial.Failed, "neither was sent")
	require.ErrorContains(t, err, "the base branch and the labels were not changed; the edit failed: ")
	require.ErrorContains(t, err, "; the other changes may or may not have been saved, as reading the pull request back failed: ")
	require.NotContains(t, server.calls, "PUT /api/v1/repos/alice/project/issues/7/labels")
}

func TestPullEditTellsAStoredDueDateInAnyServerTimeZone(t *testing.T) {
	due := time.Date(2026, 11, 5, 23, 59, 59, 0, time.UTC)
	fields := editFields(PullEdit{Deadline: &due})
	field := fields[slices.IndexFunc(fields, func(f editField) bool { return f.name == "the due date" })]
	for stored, want := range map[string]bool{
		"2026-11-05T23:59:59Z":      true,
		"2026-11-06T01:59:59+02:00": true, // the same instant, in the server's time zone
		"2026-11-05T23:59:59+02:00": true, // the end of the day in the server's time zone
		"2026-11-05T23:59:59-08:00": true,
		"2026-11-04T23:59:59Z":      false,
		"2026-11-06T23:59:59+02:00": false,
	} {
		require.Equal(t, want, field.stored(Pull{Deadline: &stored}), stored)
	}
}

func TestPullEditNamesWhatTheIssueEndpointSavedBeforeFailing(t *testing.T) {
	server := &partialEditServer{t: t, writer: true, failIssue: true, title: "Old", base: "main"}
	title, dev, milestone := "E-title", "dev", int64(4)
	item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		Title: &title, Milestone: &milestone, Base: &dev, Labels: &[]int64{3},
	})
	partial, ok := errors.AsType[*PartialEditError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, []string{"the title"}, partial.Saved)
	require.Equal(t, []string{"the milestone", "the base branch", "the labels"}, partial.Failed, "the base branch and the labels are not sent")
	require.Zero(t, item.Number)
	require.NotContains(t, server.calls, "PATCH /api/v1/repos/alice/project/pulls/7")
	require.NotContains(t, server.calls, "PUT /api/v1/repos/alice/project/issues/7/labels")
}

func TestPullEditNamesWhatTheAnswersSavedWhenTheReadBackFails(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusNotFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := &partialEditServer{t: t, writer: true, readStatus: status, title: "Old", base: "main"}
			body, milestone, typo := "New body", int64(4), "typo"
			item, err := (Pulls{Client: newTestClient(t, server), Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
				Body: &body, Milestone: &milestone, Base: &typo, Labels: &[]int64{3},
			})
			partial, ok := errors.AsType[*PartialEditError](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, []string{"the body", "the milestone", "the labels"}, partial.Saved)
			require.Empty(t, partial.Failed, "nothing is known not to be changed")
			require.ErrorContains(t, err, `the body, the milestone and the labels were saved, but the edit failed: branch "typo" not found in alice/project; the other changes may or may not have been saved, as reading the pull request back failed: `)
			require.ErrorContains(t, err, "; gt pr view 7 shows the current state")
			require.NotContains(t, err.Error(), "changing the base branch")
			require.Error(t, partial.ReadErr)
			require.True(t, errors.Is(err, partial.ReadErr), "the error of the read goes with the edit's")
			require.Equal(t, []int64{3}, server.labels, "the labels go after the issue fields were taken")
			require.Zero(t, item.Number)
		})
	}
}
