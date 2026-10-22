// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

// pullListServer lists the pull requests of alice/project: #17 from bob's fork, #15 a draft, #14
// merged and #16 closed, with the total count Gitea reports.
func pullListServer(t *testing.T, states *[]string) *http.Server {
	t.Helper()
	pulls := map[string]string{
		"open": `[{"number":17,"title":"Improve greeting","state":"open","user":{"login":"bob"},"created_at":"2026-10-11T10:00:00Z","head":{"ref":"bob/greeting","repo":{"full_name":"bob/project"}},"html_url":"http://gitea/alice/project/pulls/17"},
			{"number":15,"title":"WIP: experiment","state":"open","draft":true,"user":{"login":"alice"},"created_at":"2026-10-11T09:00:00Z","head":{"ref":"experiment","repo":{"full_name":"alice/project"}}}]`,
		"closed": `[{"number":16,"title":"Add usage guide","state":"closed","user":{"login":"alice"},"head":{"ref":"docs/guide"}},
			{"number":14,"title":"Fix README typo","state":"closed","merged":true,"user":{"login":"alice"},"head":{"ref":"fix/typo"}}]`,
	}
	return &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		*states = append(*states, state)
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.Header().Set("X-Total-Count", "2")
		_, _ = w.Write([]byte(pulls[state]))
	})}
}

func TestPullListTable(t *testing.T) {
	var states []string
	server := newAuthenticatedServer(pullListServer(t, &states).Handler)
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project"}))
	require.Equal(t, "17\tImprove greeting\tbob:bob/greeting\topen\tbob\t2026-10-11T10:00:00Z\n15\tWIP: experiment\texperiment\tdraft\talice\t2026-10-11T09:00:00Z\n", out.String())

	out.Reset()
	plainTTY(t, &deps, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project"}))
	require.Equal(t, `Showing 2 of 2 open pull requests in alice/project

ID   TITLE             BRANCH            AUTHOR  CREATED
#17  Improve greeting  bob:bob/greeting  bob     about 2 hours ago
#15  WIP: experiment   experiment        alice   about 3 hours ago
`, stripLinks(out.String()))

	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--state", "merged"}))
	require.Equal(t, "Showing 1 of 1 merged pull requests in alice/project\n\nID   TITLE            BRANCH    STATE   AUTHOR  CREATED\n#14  Fix README typo  fix/typo  merged  alice\n", stripLinks(out.String()))

	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "-d"}))
	require.True(t, strings.HasPrefix(stripLinks(out.String()), "Showing 1 of 1 open pull requests in alice/project\n\nID   TITLE            BRANCH      AUTHOR  CREATED\n#15  WIP: experiment"), out.String())

	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "-H", "alice:greeting"}))
	require.Empty(t, out.String())
	require.Equal(t, "no open pull requests match your filters in alice/project\n", diagnostics.String())
	require.Equal(t, []string{"open", "closed", "open"}, slices.Compact(states))
}

func TestPullListFillsReviewersWhenSelected(t *testing.T) {
	views, reviews := 0, 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/13":
			views++
			_, _ = w.Write([]byte(`{"number":13}`))
		case "/api/v1/repos/alice/project/pulls/13/reviews":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			reviews++
			_, _ = w.Write([]byte(`[{"id":1,"state":"COMMENT","user":{"login":"carol"}},{"id":2,"state":"REQUEST_REVIEW","user":{"login":"bob"}},
				{"id":3,"state":"REQUEST_REVIEW","team":{"name":"core"}}]`))
		default:
			_, _ = w.Write([]byte(`[{"number":13,"requested_reviewers":[{"login":"carol"},{"login":"bob"}]}]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--json", "number,draft"}))
	require.JSONEq(t, `[{"number":13,"draft":false}]`, out.String())
	require.Zero(t, views+reviews)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--json", "number,reviewers,reviewer_teams,review_decision"}))
	require.JSONEq(t, `[{"number":13,"reviewers":["bob"],"reviewer_teams":["core"],"review_decision":"review_required"}]`, out.String())
	require.Equal(t, 1, reviews)
	require.Zero(t, views, "the reviews alone give the reviewers")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--jq", ".[].number"}))
	require.Equal(t, "13\n", out.String())
	require.Equal(t, 1, reviews, "a --jq filter that names no review field reads no reviews")
	require.Zero(t, views)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--jq", ".[].reviewers"}))
	require.Equal(t, 2, reviews)
	require.Zero(t, views)
}

func TestPullViewIgnoresReviewersWithoutAnOutstandingRequest(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/8":
			// Gitea lists everyone who reviewed as a requested reviewer.
			_, _ = w.Write([]byte(`{"number":8,"state":"open","requested_reviewers":[{"login":"carol"},{"login":"dave"}]}`))
		case "/api/v1/repos/alice/project/pulls/8/reviews":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"state":"COMMENT","user":{"login":"carol"}},{"id":2,"state":"APPROVED","user":{"login":"dave"},"dismissed":true}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "8", "-R", "alice/project", "--json", "reviewers,reviewer_teams,review_decision"}))
	require.JSONEq(t, `{"reviewers":[],"reviewer_teams":[],"review_decision":""}`, out.String())
}

func TestPullViewReviewDecisionNeedsTheRequiredOfficialApprovals(t *testing.T) {
	carolOfficial := "false"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/8":
			_, _ = w.Write([]byte(`{"number":8,"state":"closed","merged":true,"merge_commit_sha":"abc123","base":{"ref":"main"}}`))
		case "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main","protected":true,"required_approvals":2}`))
		case "/api/v1/repos/alice/project/pulls/8/reviews":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"state":"APPROVED","official":true,"body":"LGTM","user":{"login":"bob"},"submitted_at":"2026-01-02T00:00:00Z"},
				{"id":2,"state":"APPROVED","official":` + carolOfficial + `,"user":{"login":"carol"}},
				{"id":3,"state":"PENDING","user":{"login":"dave"}},{"id":4,"state":"REQUEST_REVIEW","user":{"login":"erin"}}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"pr", "view", "8", "-R", "alice/project", "--json", "review_decision,merge_commit_sha", "--jq", ".review_decision + \" \" + .merge_commit_sha"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "review_required abc123\n", out.String())
	carolOfficial = "true"
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "approved abc123\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "8", "-R", "alice/project", "--jq", "[.reviews[] | {author, state, body, submitted_at}]"}))
	require.JSONEq(t, `[{"author":"bob","state":"APPROVED","body":"LGTM","submitted_at":"2026-01-02T00:00:00Z"},{"author":"carol","state":"APPROVED","body":"","submitted_at":""}]`, out.String())
}

func TestPullListFillsReviewDecisionWhenSelected(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/13/reviews":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"state":"APPROVED","user":{"login":"bob"}}]`))
		case "/api/v1/repos/alice/project/pulls/13":
			_, _ = w.Write([]byte(`{"number":13}`))
		default:
			_, _ = w.Write([]byte(`[{"number":13}]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--json", "number,review_decision"}))
	require.JSONEq(t, `[{"number":13,"review_decision":"approved"}]`, out.String())
}

func TestPullListReviewDecisionCountsApprovalsLikeMerging(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	reviews := map[string]string{
		"1": `[{"id":1,"state":"APPROVED","official":true,"user":{"login":"bob"}}]`,
		"2": `[{"id":2,"state":"APPROVED","official":true,"stale":true,"user":{"login":"bob"}}]`,
		"3": `[]`,
		"4": `[]`,
		"5": `[{"id":5,"state":"APPROVED","official":false,"user":{"login":"carol"}}]`,
	}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		number := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/pulls/"), "/reviews")
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/pulls":
			_, _ = w.Write([]byte(`[{"number":1,"base":{"ref":"main"}},{"number":2,"base":{"ref":"main"}},{"number":3,"base":{"ref":"main"}},{"number":4,"base":{"ref":"dev"}},{"number":5,"base":{"ref":"main"}}]`))
		case reviews[number] != "":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(reviews[number]))
		case r.URL.Path == "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main","protected":true,"required_approvals":1,"effective_branch_protection_name":"main"}`))
		case r.URL.Path == "/api/v1/repos/alice/project/branches/dev":
			_, _ = w.Write([]byte(`{"name":"dev"}`))
		case r.URL.Path == "/api/v1/repos/alice/project/branch_protections/main":
			_, _ = w.Write([]byte(`{"rule_name":"main","required_approvals":1,"ignore_stale_approvals":true}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--json", "number,review_decision"}))
	require.JSONEq(t, `[{"number":1,"review_decision":"approved"},{"number":2,"review_decision":"review_required"},{"number":3,"review_decision":"review_required"},
		{"number":4,"review_decision":""},{"number":5,"review_decision":"review_required"}]`, out.String())
	require.Equal(t, 1, requests["/api/v1/repos/alice/project/branches/main"])
	require.Equal(t, 1, requests["/api/v1/repos/alice/project/branches/dev"])
	require.Equal(t, 1, requests["/api/v1/repos/alice/project/branch_protections/main"])
}

func TestPullListFillsReviewFieldsWhenSelectingEveryField(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/13/reviews":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"state":"APPROVED","user":{"login":"bob"}}]`))
		case "/api/v1/repos/alice/project/pulls/13":
			_, _ = w.Write([]byte(`{"number":13,"merged":true,"merged_by":{"login":"carol"}}`))
		default:
			_, _ = w.Write([]byte(`[{"number":13}]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--state", "all", "--json"}))
	var items []map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &items))
	require.Len(t, items, 1)
	require.Equal(t, "carol", items[0]["merged_by"])
	require.Equal(t, "approved", items[0]["review_decision"])
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--state", "all", "--jq", ".[0] | {merged_by, review_decision}"}))
	require.JSONEq(t, `{"merged_by":"carol","review_decision":"approved"}`, out.String())
}

func TestPullViewResolvesCurrentBranch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/pulls/9":
			_, _ = fmt.Fprint(w, `{"number":9,"title":"Feature","state":"open","head":{"ref":"feature","sha":"abc"}}`)
		case r.URL.Query().Get("page") != "1":
			_, _ = fmt.Fprint(w, `[]`)
		case r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.Equal(t, "open", r.URL.Query().Get("state"))
			_, _ = fmt.Fprint(w, `[{"number":9,"title":"Feature","html_url":"https://example.org/pr/9","state":"open","head":{"ref":"feature","sha":"abc","repo":{"full_name":"alice/project"}}}]`)
		default:
			_, _ = fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feature"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "--json=number,title"}))
	require.JSONEq(t, `{"number":9,"title":"Feature"}`, out.String())
	deps.Git = &fakeGit{results: map[string]string{}}
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"pr", "view"})))
	err := Execute(context.Background(), deps, []string{"pr", "view", "-R", "alice/project"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "pass a pull request number, URL or branch with --repo")
}
