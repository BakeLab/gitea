// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueListSendsTypePageSizeAndFilters(t *testing.T) {
	var queries []url.Values
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		queries = append(queries, r.URL.Query())
		_, _ = w.Write([]byte(`[{"id":1,"number":2,"title":"Bug","user":{"login":"bob"}}]`))
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	items, more, err := service.List(context.Background(), ListOptions{State: "open", Limit: 1, Page: 3})
	require.NoError(t, err)
	require.False(t, more)
	require.Equal(t, []Issue{{Author: "bob", ID: 1, Number: 2, Kind: KindIssue, Title: "Bug", Labels: []Label{}, Assignees: []string{}}}, items)
	since := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	_, _, err = service.List(context.Background(), ListOptions{
		Kind: "all", Limit: 30, Page: 1, Search: "crash", Assignee: "carol", Labels: "bug,ui", Milestones: "v1",
		Author: "dave", Mentions: "erin", Since: &since,
	})
	require.NoError(t, err)
	require.Equal(t, []url.Values{
		{"state": {"open"}, "type": {"issues"}, "limit": {"1"}, "page": {"3"}},
		{
			"type": {"all"}, "limit": {"30"}, "page": {"1"}, "q": {"crash"}, "assigned_by": {"carol"}, "labels": {"bug,ui"},
			"milestones": {"v1"}, "created_by": {"dave"}, "mentioned_by": {"erin"}, "since": {"2026-10-01T08:00:00Z"},
		},
	}, queries)
}

func TestIssueDependenciesMapReferencesAndTreatMissingSupportAsNone(t *testing.T) {
	missing := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if missing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.Equal(t, "50", r.URL.Query().Get("limit"))
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/7/dependencies":
			_, _ = w.Write([]byte(`[{"number":3,"repository":{"full_name":"alice/other"}},{"number":0,"repository":null}]`))
		case "/api/v1/repos/alice/project/issues/7/blocks":
			_, _ = w.Write([]byte(`[{"number":9,"repository":{"full_name":"alice/project"}}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	dependencies, err := service.Dependencies(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, IssueDependencies{
		BlockedBy: []IssueReference{{Repository: "alice/other", Number: 3}, {}},
		Blocks:    []IssueReference{{Repository: "alice/project", Number: 9}},
	}, dependencies)
	missing = true
	dependencies, err = service.Dependencies(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, IssueDependencies{BlockedBy: []IssueReference{}, Blocks: []IssueReference{}}, dependencies)
}

func TestDiscussionsTellPullRequestsAndMergesApart(t *testing.T) {
	list := `[{"number":1,"state":"open"},{"number":2,"state":"closed","pull_request":{"merged":false}},` +
		`{"number":3,"state":"closed","pull_request":{"merged":true}},{"number":4,"state":"open","pull_request":{"merged":false}}]`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(list))
	}))
	want := []struct{ kind, state string }{{KindIssue, "open"}, {KindPull, "closed"}, {KindPull, "merged"}, {KindPull, "open"}}
	issues, _, err := (Issues{Client: client, Repository: "alice/project"}).List(context.Background(), ListOptions{Kind: "all", State: "all", Limit: 30, Page: 1})
	require.NoError(t, err)
	found, err := (Issues{Client: client, Repository: "alice/project"}).Search(context.Background(), DiscussionSearchOptions{State: "all", Limit: 30, Page: 1})
	require.NoError(t, err)
	require.Len(t, issues, len(want))
	for i, item := range want {
		require.Equal(t, item.kind, issues[i].Kind, issues[i].Number)
		require.Equal(t, item.state, issues[i].State, issues[i].Number)
		require.Equal(t, item.state, found[i].State, found[i].Number)
	}
}

func TestIssueTemplatesAcceptCheckboxOptionObjects(t *testing.T) {
	body := `[{"name":"Form","body":[{"type":"checkboxes","attributes":{"options":[{"label":"I searched","required":true}]}}]}]`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	templates, err := (Issues{Client: client, Repository: "alice/project"}).IssueTemplates(context.Background())
	require.NoError(t, err)
	require.Equal(t, []IssueTemplate{{Name: "Form", Form: true}}, templates)
}

func TestIssueCreateInMissingRepositoryReportsTheRepository(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	_, err := (Issues{Client: client, Repository: "alice/projetc"}).Create(context.Background(), IssueOptions{Title: "x"})
	require.EqualError(t, err, "repository alice/projetc not found")
}

func TestIssueRequestsNameTheMissingRepositoryOrItem(t *testing.T) {
	for _, repository := range []string{"owner/repo", "owner/missing"} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/repos/owner/repo" {
				_, _ = w.Write([]byte(`{"full_name":"owner/repo","has_issues":true,"has_pull_requests":true}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}))
		missing := "repository owner/missing not found"
		want := func(item string) string {
			if repository == "owner/missing" {
				return missing
			}
			return item + " not found in owner/repo"
		}
		ctx := context.Background()
		issues := Issues{Client: client, Repository: repository}
		_, err := issues.View(ctx, 9)
		require.EqualError(t, err, want("issue #9"))
		_, err = issues.SetState(ctx, 9, "closed")
		require.EqualError(t, err, want("issue #9"))
		_, err = (Comments{Client: client, Repository: repository}).Create(ctx, 9, "hi")
		require.EqualError(t, err, want("issue #9"))
		_, err = (Comments{Client: client, Repository: repository}).View(ctx, 4)
		require.EqualError(t, err, want("comment 4"))
		_, err = (Webhooks{Client: client, Scope: WebhookScope{Repository: repository}}).View(ctx, 7)
		require.EqualError(t, err, want("webhook 7"))
		_, err = (Pulls{Client: client, Repository: repository}).View(ctx, 3)
		require.EqualError(t, err, want("pull request #3"))
		_, err = (Releases{Client: client, Repository: repository}).View(ctx, "v9")
		require.EqualError(t, err, want("release v9"))
		_, err = (Releases{Client: client, Repository: repository}).Latest(ctx)
		require.EqualError(t, err, want("latest release"))
		branches := Branches{Client: client}
		_, err = branches.Find(ctx, repository, "dev")
		require.EqualError(t, err, want(`branch "dev"`))
		require.EqualError(t, branches.Delete(ctx, repository, "dev"), want(`branch "dev"`))
		require.EqualError(t, branches.Rename(ctx, repository, "dev", "main"), want(`branch "dev"`))
		_, err = (DeployKeys{Client: client, Repository: repository}).View(ctx, 13)
		require.EqualError(t, err, want("deploy key 13"))
		require.EqualError(t, (DeployKeys{Client: client, Repository: repository}).Delete(ctx, 13), want("deploy key 13"))
		if repository == "owner/missing" {
			require.EqualError(t, branches.RequireAll(ctx, repository, []string{"a", "b", "c"}), missing, "the repository is named once, not every branch")
			_, err = (Milestones{Client: client, Repository: repository}).AllIn(ctx, "open")
			require.EqualError(t, err, missing)
		} else {
			require.EqualError(t, branches.RequireAll(ctx, repository, []string{"a", "b"}), "branch \"a\" not found in owner/repo\nbranch \"b\" not found in owner/repo")
		}
		if repository == "owner/missing" {
			_, _, err = issues.List(ctx, ListOptions{State: "open", Limit: 10, Page: 1})
			require.EqualError(t, err, missing)
			_, err = issues.Search(ctx, DiscussionSearchOptions{Query: "x", Limit: 10, Page: 1})
			require.EqualError(t, err, missing)
			_, err = (Milestones{Client: client, Repository: repository}).List(ctx, "open", 10, 1)
			require.EqualError(t, err, missing)
			_, err = (Webhooks{Client: client, Scope: WebhookScope{Repository: repository}}).List(ctx, 10, 1)
			require.EqualError(t, err, missing)
			_, err = (Webhooks{Client: client, Scope: WebhookScope{Organization: "nope"}}).List(ctx, 10, 1)
			require.EqualError(t, err, `organization "nope" not found`)
			_, err = (Notifications{Client: client, Repository: repository}).List(ctx, NotificationOptions{Limit: 10, Page: 1})
			require.EqualError(t, err, missing)
		}
	}
}

func TestAFailedRepositoryLookupKeepsTheMissingItem(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/owner/repo" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	ctx := context.Background()
	_, err := (Releases{Client: client, Repository: "owner/repo"}).View(ctx, "v9")
	require.EqualError(t, err, "release v9 not found in owner/repo")
	branches := Branches{Client: client}
	require.EqualError(t, branches.Delete(ctx, "owner/repo", "dev"), `branch "dev" not found in owner/repo`)
	require.EqualError(t, branches.Rename(ctx, "owner/repo", "dev", "main"), `branch "dev" not found in owner/repo`)
	require.EqualError(t, branches.RequireAll(ctx, "owner/repo", []string{"dev"}), `branch "dev" not found in owner/repo`)
}

func TestIssueDependenciesReportAFailureBesideAMissingList(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/dependencies") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, err := (Issues{Client: client, Repository: "owner/repo"}).Dependencies(context.Background(), 7)
	require.ErrorContains(t, err, "500")
}

func TestIssueAndPullRequestsNameATurnedOffUnit(t *testing.T) {
	const (
		noIssues = "issues are disabled in owner/repo; turn them on with: gt repo edit --enable-issues -R owner/repo"
		noPulls  = "pull requests are disabled in owner/repo; turn them on with: gt repo edit --enable-pull-requests -R owner/repo"
		tracker  = "owner/repo uses an external issue tracker: https://tracker.example"
	)
	const refused = "user should have specific read permission or be a repo admin or a site admin"
	for _, test := range []struct {
		repository, view, list, create, pull, pulls string
	}{
		{`{"has_issues":false,"has_pull_requests":true}`, noIssues, noIssues, noIssues, "pull request #9 not found in owner/repo", "repository owner/repo not found"},
		{`{"has_issues":true,"has_pull_requests":false}`, "issue #9 not found in owner/repo", "repository owner/repo not found", "permission denied by the Gitea instance: " + refused, noPulls, noPulls},
		{`{"has_issues":true,"has_pull_requests":true,"external_tracker":{"external_tracker_url":"https://tracker.example"}}`, tracker, tracker, tracker, "pull request #9 not found in owner/repo", "repository owner/repo not found"},
	} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/repos/owner/repo":
				_, _ = w.Write([]byte(test.repository))
			case r.Method == http.MethodPost:
				// Gitea refuses issues without the unit with a 403 for anyone but admins.
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"` + refused + `"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
			}
		}))
		ctx := context.Background()
		issues := Issues{Client: client, Repository: "owner/repo"}
		_, err := issues.View(ctx, 9)
		require.EqualError(t, err, test.view)
		_, _, err = issues.List(ctx, ListOptions{State: "open", Limit: 10, Page: 1})
		require.EqualError(t, err, test.list)
		_, err = issues.Search(ctx, DiscussionSearchOptions{State: "open", Limit: 10, Page: 1})
		require.EqualError(t, err, test.list)
		_, err = issues.Search(ctx, DiscussionSearchOptions{State: "open", Pulls: true, Limit: 10, Page: 1})
		require.EqualError(t, err, test.pulls)
		_, err = (Times{Client: client, Repository: "owner/repo"}).ListPage(ctx, TimeOptions{Limit: 10, Page: 1})
		require.EqualError(t, err, test.list)
		_, err = issues.Create(ctx, IssueOptions{Title: "x"})
		require.EqualError(t, err, test.create)
		pulls := Pulls{Client: client, Repository: "owner/repo"}
		_, err = pulls.View(ctx, 9)
		require.EqualError(t, err, test.pull)
		_, err = pulls.Edit(ctx, 9, PullEdit{Changes: &MetadataChanges{AddLabels: []int64{1}}})
		require.EqualError(t, err, test.pull)
		_, err = pulls.Diff(ctx, 9)
		require.EqualError(t, err, test.pull)
		_, err = pulls.Files(ctx, 9)
		require.EqualError(t, err, test.pull)
		_, err = (Reviews{Client: client, Repository: "owner/repo"}).ListPage(ctx, 9, 10, 1)
		require.EqualError(t, err, test.pull)
		_, err = (Issues{Client: client, Repository: "owner/repo", Pulls: true}).View(ctx, 9)
		require.EqualError(t, err, test.pull)
		_, _, err = pulls.List(ctx, PullListOptions{State: "open", Limit: 10, Page: 1})
		require.EqualError(t, err, test.pulls)
	}
}

func TestIssueAttachmentUploadNamesWhatIsMissing(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if r.URL.Path == "/api/v1/repos/o/r/issues/99/assets" {
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 97, index: 99]"}`))
		}
	}))
	_, err := Issues{Client: client, Repository: "o/r"}.UploadAttachment(context.Background(), 99, "a.txt", strings.NewReader("a"))
	require.EqualError(t, err, "issue #99 not found in o/r")
	_, err = Issues{Client: client, Repository: "o/missing"}.UploadAttachment(context.Background(), 1, "a.txt", strings.NewReader("a"))
	require.EqualError(t, err, "repository o/missing not found")
}

func TestSetBlockedByNamesWhatIsMissingOrRefused(t *testing.T) {
	for _, tc := range []struct {
		name, blocker string
		block         bool
		status        int
		message       string
		viewMissing   bool
		dependencies  bool
		err           string
	}{
		{name: "block", blocker: "o/r", block: true, status: http.StatusCreated},
		{name: "unblock", blocker: "o/r", status: http.StatusCreated},
		{name: "exists", blocker: "o/r", block: true, status: http.StatusInternalServerError, message: "Issue dependency does already exist"},
		{name: "missing repository", blocker: "x/y", block: true, status: http.StatusNotFound, message: "repository does not exist", err: "repository x/y not found"},
		{name: "missing issue", blocker: "o/r", block: true, status: http.StatusNotFound, message: "issue does not exist [id: 0, repo_id: 1, index: 7]", err: "issue #7 not found in o/r"},
		{name: "missing blocker", blocker: "x/y", block: true, status: http.StatusNotFound, message: "issue does not exist [id: 0, repo_id: 2, index: 3]", err: "issue x/y#3 not found"},
		{name: "dependencies off", blocker: "o/r", block: true, status: http.StatusNotFound, err: "issue dependencies are disabled in o/r"},
		{name: "no write access", blocker: "o/r", block: true, status: http.StatusNotFound, dependencies: true, err: "changing the dependencies of issue #7 requires write access to the issues of o/r"},
		{name: "invisible issue", blocker: "o/r", block: true, status: http.StatusNotFound, viewMissing: true, err: "issue #7 not found in o/r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/repos/o/r/issues/7":
					if tc.viewMissing {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					_, _ = w.Write([]byte(`{"id":70,"number":7}`))
				case "GET /api/v1/repos/o/r":
					_, _ = fmt.Fprintf(w, `{"full_name":"o/r","internal_tracker":{"enable_issue_dependencies":%t}}`, tc.dependencies)
				default:
					method := http.MethodDelete
					if tc.block {
						method = http.MethodPost
					}
					owner, repo, _ := strings.Cut(tc.blocker, "/")
					assert.Equal(t, method+" /api/v1/repos/o/r/issues/7/dependencies", r.Method+" "+r.URL.Path)
					assert.Equal(t, map[string]any{"index": 3.0, "owner": owner, "repo": repo}, requestJSON(t, r))
					w.WriteHeader(tc.status)
					if tc.message != "" {
						_, _ = fmt.Fprintf(w, `{"message":%q}`, tc.message)
					} else if tc.status == http.StatusCreated {
						_, _ = w.Write([]byte(`{"id":30}`))
					}
				}
			}))
			err := (Issues{Client: client, Repository: "o/r"}).SetBlockedBy(context.Background(), 7, IssueReference{Repository: tc.blocker, Number: 3}, tc.block)
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.err)
		})
	}
}
