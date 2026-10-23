// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

// plainTTY makes deps render for a terminal without color or markdown, so views compare as text.
func plainTTY(t *testing.T, deps *Dependencies, serverURL string) {
	t.Helper()
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(serverURL, map[string]string{"NO_COLOR": "1"})
	rawMarkdown(t, *deps)
}

func TestDiscussionViews(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash\u001b[2J","body":"Steps:\r\n\u001b]52;c;aGk=\u0007run‮\r","html_url":"https://example.org/i/3","state":"open","user":{"login":"bob"},"created_at":"2026-01-01T00:00:00Z","labels":[{"name":"bug","color":"ff0000"}],"assignees":[{"login":"alice"}],"comments":1}`))
		case "/api/v1/repos/alice/project/issues/3/dependencies", "/api/v1/repos/alice/project/issues/3/blocks":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/alice/project/pulls/4/reviews":
			if r.URL.Query().Get("page") > "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"state":"REQUEST_REVIEW","user":{"login":"carol"}}]`))
		case "/api/v1/repos/alice/project/issues/3/comments":
			_, _ = w.Write([]byte(`[{"id":79,"body":"Confirmed\u001b[31m\n\tok","user":{"login":"carol"},"created_at":"2026-01-02T00:00:00Z"}]`))
		case "/api/v1/repos/alice/project/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"title":"Fix","html_url":"https://example.org/p/4","state":"closed","merged":true,"merged_at":"2026-01-03T12:00:00Z","merged_by":{"login":"alice"},"user":{"login":"bob"},"created_at":"2026-01-03T00:00:00Z","base":{"ref":"main"},"head":{"ref":"fix"},"requested_reviewers":[{"login":"carol"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "view", "3", "-R", "alice/project", "--comments"}))
	require.Equal(t, "title:\tCrash[2J\nstate:\topen\nauthor:\tbob\nlabels:\tbug\nassignees:\talice\nmilestone:\t\ndue:\t\nref:\t\nblocked by:\t\nblocks:\t\ncomments:\t1\nnumber:\t3\nurl:\thttps://example.org/i/3\n--\nSteps:\n]52;c;aGk=run\n"+
		"--\nauthor:\tcarol\ncreated_at:\t2026-01-02T00:00:00Z\nid:\t79\n--\nConfirmed[31m\n\tok\n", out.String())
	out.Reset()
	plainTTY(t, &deps, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "view", "3", "-R", "alice/project"}))
	require.Equal(t, "Crash[2J #3\nOpen • bob opened about 3 days ago • 1 comment\nLabels:    bug\nAssignees: alice\n\nSteps:\n]52;c;aGk=run\n\n"+
		"1 comment — use --comments to view them\n\nView this issue on Gitea: https://example.org/i/3\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "view", "3", "-R", "alice/project", "--comments"}))
	require.Contains(t, out.String(), "\n\ncarol • about 2 days ago • #79\nConfirmed[31m\n\tok\n\nView this issue on Gitea:")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "4", "-R", "alice/project"}))
	require.Equal(t, "Fix #4\nMerged • alice merged into main from fix • about 12 hours ago • 0 comments\nReviewers: carol (Requested)\n\nNo description provided\n\nView this pull request on Gitea: https://example.org/p/4\n", out.String())
}

func TestIssueViewDependencies(t *testing.T) {
	supported := true
	var paths []string
	var mu sync.Mutex
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash","html_url":"https://example.org/i/3","state":"open","user":{"login":"bob"},"created_at":"2026-01-03T00:00:00Z","due_date":"2026-12-24T23:59:59Z","ref":"main"}`))
		case !supported:
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Query().Get("page") != "1":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues/3/dependencies":
			_, _ = w.Write([]byte(`[{"number":1,"repository":{"full_name":"alice/project"}},{"number":5,"repository":{"full_name":"bob/lib"}},{"number":0,"title":"HIDDEN"}]`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues/3/blocks":
			_, _ = w.Write([]byte(`[{"number":9,"repository":{"full_name":"alice/project"}}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC) }
	plainTTY(t, &deps, server.URL)
	args := []string{"issue", "view", "3", "-R", "alice/project"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "Crash #3\nOpen • bob opened about 1 day ago • 0 comments\nDue:        Dec 24, 2026 (in about 11 months)\nRef:        main\nBlocked by: #1, bob/lib#5, hidden\nBlocks:     #9\n\nNo description provided\n\nView this issue on Gitea: https://example.org/i/3\n", out.String())
	out.Reset()
	deps.Streams.OutTTY = false
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Contains(t, out.String(), "\ndue:\t2026-12-24T23:59:59Z\nref:\tmain\nblocked by:\t#1, bob/lib#5, hidden\nblocks:\t#9\n")
	out.Reset()
	supported, paths = false, nil
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Contains(t, out.String(), "\nblocked by:\t\nblocks:\t\n")
	require.ElementsMatch(t, []string{"/api/v1/repos/alice/project/issues/3", "/api/v1/repos/alice/project/issues/3/dependencies", "/api/v1/repos/alice/project/issues/3/blocks"}, paths)
	out.Reset()
	paths = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "--json=number")))
	require.Equal(t, []string{"/api/v1/repos/alice/project/issues/3"}, paths)
	out.Reset()
	supported = true
	require.NoError(t, Execute(context.Background(), deps, append(args, "--json=number,blocked_by,blocks")))
	require.JSONEq(t, `{"number":3,"blocked_by":[{"repository":"alice/project","number":1},{"repository":"bob/lib","number":5},{"repository":"","number":0}],"blocks":[{"repository":"alice/project","number":9}]}`, out.String())
	// All fields, as --json alone and --jq output them, include the dependencies.
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--jq", "[.blocked_by[].number, .blocks[].number]")))
	require.JSONEq(t, `[1,5,0,9]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--json")))
	require.Contains(t, out.String(), `"blocks"`)
}

func TestPullViewCommentsIncludeReviews(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"title":"Fix","html_url":"https://example.org/p/4","state":"open","mergeable":true,"user":{"login":"bob"},"created_at":"2026-01-01T00:00:00Z","base":{"ref":"main"},"head":{"ref":"fix"},"comments":1}`))
		case "/api/v1/repos/alice/project/issues/4/comments":
			_, _ = w.Write([]byte(`[{"body":"Looks close","user":{"login":"carol"},"created_at":"2026-01-02T00:00:00Z"}]`))
		case "/api/v1/repos/alice/project/pulls/4/reviews":
			writeFirstPage(w, r, `[
				{"id":1,"body":"Please rename","state":"REQUEST_CHANGES","user":{"login":"alice"},"submitted_at":"2026-01-01T12:00:00Z","comments_count":1},
				{"id":2,"body":"","state":"APPROVED","user":{"login":"dave"},"submitted_at":"2026-01-03T00:00:00Z"},
				{"id":3,"body":"Draft","state":"PENDING","user":{"login":"erin"},"submitted_at":"2026-01-03T00:00:00Z"},
				{"id":4,"body":"Great","state":"APPROVED","user":{"login":"alice"},"submitted_at":"2026-01-03T06:00:00Z"}
			]`)
		case "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case "/api/v1/repos/alice/project/pulls/4/reviews/1/comments":
			_, _ = w.Write([]byte(`[{"id":9,"body":"Typo here","path":"main.go","position":12,"user":{"login":"alice"},"created_at":"2026-01-01T12:00:00Z"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "4", "-R", "alice/project", "--comments"}))
	require.Equal(t, "title:\tFix\nstate:\topen\nauthor:\tbob\nbase:\tmain\nhead:\tfix\nlabels:\t\nassignees:\t\nreviewers:\talice (Approved), dave (Approved)\nmilestone:\t\nchecks:\t\nmergeable:\tyes\ncomments:\t1\nnumber:\t4\nurl:\thttps://example.org/p/4\n--\n\n"+
		"--\nauthor:\talice\ncreated_at:\t2026-01-01T12:00:00Z\nreview:\tREQUEST_CHANGES\n--\nPlease rename\n"+
		"--\nauthor:\talice\ncreated_at:\t2026-01-01T12:00:00Z\nid:\t9\npath:\tmain.go:12\n--\nTypo here\n"+
		"--\nauthor:\tcarol\ncreated_at:\t2026-01-02T00:00:00Z\n--\nLooks close\n"+
		"--\nauthor:\tdave\ncreated_at:\t2026-01-03T00:00:00Z\nreview:\tAPPROVED\n--\n\n"+
		"--\nauthor:\talice\ncreated_at:\t2026-01-03T06:00:00Z\nreview:\tAPPROVED\n--\nGreat\n", out.String())
	out.Reset()
	plainTTY(t, &deps, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "4", "-R", "alice/project", "--comments"}))
	require.Equal(t, "Fix #4\nOpen • bob wants to merge into main from fix • about 3 days ago • 1 comment\nReviewers: alice (Approved), dave (Approved)\nMergeable: yes\n\nNo description provided\n\n"+
		"alice requested changes • about 2 days ago\nPlease rename\n\n"+
		"alice commented on main.go:12 • about 2 days ago\nTypo here\n\n"+
		"carol • about 2 days ago\nLooks close\n\n"+
		"dave approved these changes • about 1 day ago\n\n"+
		"alice approved these changes • about 18 hours ago\nGreat\n\n"+
		"View this pull request on Gitea: https://example.org/p/4\n", out.String())
}

func TestPullViewStates(t *testing.T) {
	for _, tc := range []struct {
		name, pull, statuses, reviews, want, decision string
	}{
		{
			name:     "draft",
			pull:     `{"number":15,"title":"WIP: experiment","state":"open","draft":true,"mergeable":false,"user":{"login":"alice"},"base":{"ref":"main"},"head":{"ref":"experiment"}}`,
			want:     "WIP: experiment #15\nDraft • alice wants to merge into main from experiment • about 1 day ago • 0 comments\nMergeable: no — draft; mark it ready with: gt pr ready 15\n",
			decision: "",
		},
		{
			name:     "fork with checks and conflicts",
			pull:     `{"number":17,"title":"Improve greeting","state":"open","mergeable":false,"user":{"login":"bob"},"base":{"ref":"main"},"head":{"ref":"greet","sha":"abc","repo":{"full_name":"bob/project"}},"requested_reviewers":[{"login":"carol"}]}`,
			statuses: `[{"id":1,"context":"ci / test","status":"pending"},{"id":2,"context":"lint","status":"failure"}]`,
			reviews:  `[{"id":4,"state":"REQUEST_CHANGES","user":{"login":"alice"}},{"id":5,"state":"REQUEST_REVIEW","user":{"login":"carol"}}]`,
			want:     "Improve greeting #17\nOpen • bob wants to merge into main from bob:greet • about 1 day ago • 0 comments\nReviewers: alice (Changes requested), carol (Requested)\nChecks:    1 failing, 1 pending (gt pr checks 17)\nMergeable: no — conflicts with main\n",
			decision: "changes_requested",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/repos/alice/project/pulls/15" || r.URL.Path == "/api/v1/repos/alice/project/pulls/17":
					_, _ = w.Write([]byte(tc.pull[:len(tc.pull)-1] + `,"created_at":"2026-01-03T00:00:00Z","html_url":"https://example.org/p"}`))
				case r.URL.Query().Get("page") != "1":
					_, _ = w.Write([]byte(`[]`))
				case r.URL.Path == "/api/v1/repos/alice/project/statuses/abc":
					_, _ = w.Write([]byte(tc.statuses))
				case tc.reviews == "":
					_, _ = w.Write([]byte(`[]`))
				default:
					_, _ = w.Write([]byte(tc.reviews))
				}
			}))
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			deps.Now = func() time.Time { return time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC) }
			plainTTY(t, &deps, server.URL)
			number := map[string]string{"draft": "15"}[tc.name]
			if number == "" {
				number = "17"
			}
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", number, "-R", "alice/project"}))
			require.Equal(t, tc.want+"\nNo description provided\n\nView this pull request on Gitea: https://example.org/p\n", out.String())
			out.Reset()
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", number, "-R", "alice/project", "--json=draft,review_decision"}))
			require.Contains(t, out.String(), `"review_decision": "`+tc.decision+`"`)
		})
	}
}

func TestReviewerSummaryKeepsChangesRequestedPastComments(t *testing.T) {
	reviews := []services.Review{
		{ID: 1, Author: "bob", State: "REQUEST_CHANGES"},
		{ID: 2, Author: "bob", State: "COMMENT"},
		{ID: 3, Author: "carol", State: "COMMENT"},
	}
	require.Equal(t, "bob (Changes requested), carol (Commented)", reviewerSummary(services.Pull{}, reviews))
}

func TestDueTextKeepsTheServerCalendarDate(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	require.Equal(t, "Dec 24, 2026 (in about 2 months)", dueText(now, "2026-12-24T23:59:59-05:00"))
	require.Equal(t, "Dec 24, 2026 (in about 2 months)", dueText(now, "2026-12-24T23:59:59Z"))
	require.Equal(t, "garbage", dueText(now, "garbage"))
}
