// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDiscussionViews(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash\u001b[2J","body":"Steps:\r\n\u001b]52;c;aGk=\u0007run\u202e\r","html_url":"https://example.org/i/3","state":"open","user":{"login":"bob"},"created_at":"2026-01-01T00:00:00Z","labels":[{"name":"bug","color":"ff0000"}],"assignees":[{"login":"alice"}],"comments":1}`))
		case "/api/v1/repos/alice/project/issues/3/dependencies", "/api/v1/repos/alice/project/issues/3/blocks":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/alice/project/issues/3/comments":
			_, _ = w.Write([]byte(`[{"body":"Confirmed\u001b[31m\n\tok","user":{"login":"carol"},"created_at":"2026-01-02T00:00:00Z"}]`))
		case "/api/v1/repos/alice/project/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"title":"Fix","html_url":"https://example.org/p/4","state":"closed","merged":true,"user":{"login":"bob"},"created_at":"2026-01-03T00:00:00Z","base":{"ref":"main"},"head":{"ref":"fix"},"requested_reviewers":[{"login":"alice"}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "view", "3", "-R", "alice/project", "--comments"}))
	require.Equal(t, "title:\tCrash[2J\nstate:\topen\nauthor:\tbob\nlabels:\tbug\nassignees:\talice\nmilestone:\t\nblocked by:\t\nblocks:\t\ncomments:\t1\nnumber:\t3\nurl:\thttps://example.org/i/3\n--\nSteps:\n]52;c;aGk=run\n--\nauthor:\tcarol\ncreated_at:\t2026-01-02T00:00:00Z\n--\nConfirmed[31m\n\tok\n", out.String())
	out.Reset()
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "4", "-R", "alice/project"}))
	require.Equal(t, "Fix #4\nMerged • bob wants to merge into main from fix • about 1 day ago • 0 comments\nReviewers: alice\n\nNo description provided\n\nView this pull request on Gitea: https://example.org/p/4\n", out.String())
}

func TestIssueViewDependencies(t *testing.T) {
	supported := true
	var paths []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash","html_url":"https://example.org/i/3","state":"open","user":{"login":"bob"},"created_at":"2026-01-03T00:00:00Z"}`))
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
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	args := []string{"issue", "view", "3", "-R", "alice/project"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "Crash #3\nOpen • bob opened about 1 day ago • 0 comments\nBlocked by: #1, bob/lib#5, hidden\nBlocks: #9\n\nNo description provided\n\nView this issue on Gitea: https://example.org/i/3\n", out.String())
	out.Reset()
	deps.Streams.OutTTY = false
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Contains(t, out.String(), "\nblocked by:\t#1, bob/lib#5, hidden\nblocks:\t#9\n")
	out.Reset()
	supported, paths = false, nil
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Contains(t, out.String(), "\nblocked by:\t\nblocks:\t\n")
	require.Equal(t, []string{"/api/v1/repos/alice/project/issues/3", "/api/v1/repos/alice/project/issues/3/dependencies"}, paths)
	out.Reset()
	paths = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "--json=number")))
	require.Equal(t, []string{"/api/v1/repos/alice/project/issues/3"}, paths)
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for raw, want := range map[string]string{
		"2025-12-31T23:59:30Z": "less than a minute ago",
		"2025-12-31T23:59:00Z": "about 1 minute ago",
		"2025-12-31T21:00:00Z": "about 3 hours ago",
		"2025-11-01T00:00:00Z": "about 2 months ago",
		"2023-12-01T00:00:00Z": "about 2 years ago",
		"not a time":           "not a time",
	} {
		require.Equal(t, want, relativeTime(now, raw), raw)
	}
}

func TestPullViewCommentsIncludeReviews(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"title":"Fix","html_url":"https://example.org/p/4","state":"open","mergeable":true,"user":{"login":"bob"},"created_at":"2026-01-01T00:00:00Z","base":{"ref":"main"},"head":{"ref":"fix"},"comments":1}`))
		case "/api/v1/repos/alice/project/issues/4/comments":
			_, _ = w.Write([]byte(`[{"body":"Looks close","user":{"login":"carol"},"created_at":"2026-01-02T00:00:00Z"}]`))
		case "/api/v1/repos/alice/project/pulls/4/reviews":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"id":1,"body":"Please rename","state":"REQUEST_CHANGES","user":{"login":"alice"},"submitted_at":"2026-01-01T12:00:00Z","comments_count":1},
				{"id":2,"body":"","state":"APPROVED","user":{"login":"dave"},"submitted_at":"2026-01-03T00:00:00Z"},
				{"id":3,"body":"Draft","state":"PENDING","user":{"login":"erin"},"submitted_at":"2026-01-03T00:00:00Z"},
				{"id":4,"body":"Great","state":"APPROVED","user":{"login":"alice"},"submitted_at":"2026-01-03T06:00:00Z"}
			]`))
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
	require.Equal(t, "title:\tFix\nstate:\topen\nauthor:\tbob\nlabels:\t\nreviewers:\t\nassignees:\t\nmilestone:\t\nmergeable:\tyes\ncomments:\t1\nnumber:\t4\nurl:\thttps://example.org/p/4\n--\n\n"+
		"--\nauthor:\talice\ncreated_at:\t2026-01-01T12:00:00Z\nreview:\tREQUEST_CHANGES\n--\nPlease rename\n"+
		"--\nauthor:\talice\ncreated_at:\t2026-01-01T12:00:00Z\npath:\tmain.go:12\n--\nTypo here\n"+
		"--\nauthor:\tcarol\ncreated_at:\t2026-01-02T00:00:00Z\n--\nLooks close\n"+
		"--\nauthor:\talice\ncreated_at:\t2026-01-03T06:00:00Z\nreview:\tAPPROVED\n--\nGreat\n", out.String())
	out.Reset()
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "4", "-R", "alice/project", "--comments"}))
	require.Equal(t, "Fix #4\nOpen • bob wants to merge into main from fix • about 3 days ago • 1 comment\nMergeable: yes\n\nNo description provided\n\n"+
		"alice • reviewed (REQUEST_CHANGES) • about 2 days ago\nPlease rename\n\n"+
		"alice • commented on main.go:12 • about 2 days ago\nTypo here\n\n"+
		"carol • about 2 days ago\nLooks close\n\n"+
		"alice • reviewed (APPROVED) • about 18 hours ago\nGreat\n\n"+
		"View this pull request on Gitea: https://example.org/p/4\n", out.String())
}
