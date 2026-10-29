// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestTimelineListsWhatHappenedOldestFirst(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash"}`))
		case "/api/v1/settings/api":
			_, _ = w.Write([]byte(`{"max_response_items":50}`))
		case "/api/v1/repos/alice/project/issues/3/timeline":
			writeFirstPage(w, r, `[
				{"id":1,"type":"label","body":"1","label":{"name":"bug"},"user":{"login":"bob"},"created_at":"2026-01-01T00:00:00Z"},
				{"id":2,"type":"change_title","old_title":"crash","new_title":"Crash","user":{"login":"bob"},"created_at":"2026-01-02T00:00:00Z"},
				{"id":3,"type":"comment","body":"Fixed in main\nThanks","user":{"login":"alice"},"created_at":"2026-01-03T00:00:00Z"},
				{"id":4,"type":"close","user":{"login":"alice"},"created_at":"2026-01-03T00:00:00Z"},
				{"id":5,"type":"assignees","assignee":{"login":"carol"},"user":{"login":"bob"},"created_at":"2026-01-04T00:00:00Z"},
				{"id":6,"type":"assignees","assignee":{"login":"carol"},"removed_assignee":true,"user":{"login":"bob"},"created_at":"2026-01-04T00:00:00Z"},
				{"id":7,"type":"add_dependency","dependent_issue":{"number":4,"repository":{"full_name":"alice/project"}},"user":{"login":"bob"},"created_at":"2026-01-05T00:00:00Z"},
				{"id":8,"type":"remove_dependency","dependent_issue":{"number":9,"repository":{"full_name":"alice/other"}},"user":{"login":"bob"},"created_at":"2026-01-05T00:00:00Z"},
				{"id":9,"type":"review_request","assignee":{"login":"dave"},"user":{"login":"bob"},"created_at":"2026-01-06T00:00:00Z"},
				{"id":10,"type":"review_request","assignee":{"login":"dave"},"removed_assignee":true,"user":{"login":"bob"},"created_at":"2026-01-06T00:00:00Z"},
				{"id":11,"type":"added_deadline","body":"2026-12-01","user":{"login":"bob"},"created_at":"2026-01-07T00:00:00Z"},
				{"id":12,"type":"modified_deadline","body":"2027-01-01|2026-12-01","user":{"login":"bob"},"created_at":"2026-01-07T00:00:00Z"},
				{"id":13,"type":"removed_deadline","body":"2027-01-01","user":{"login":"bob"},"created_at":"2026-01-07T00:00:00Z"},
				{"id":14,"type":"review_request","assignee_team":{"name":"devs"},"removed_assignee":false,"user":{"login":"bob"},"created_at":"2026-01-08T00:00:00Z"},
				{"id":15,"type":"review_request","assignee_team":{"name":"devs"},"removed_assignee":true,"user":{"login":"bob"},"created_at":"2026-01-08T00:00:00Z"},
				{"id":16,"type":"milestone","milestone":null,"old_milestone":null,"user":{"login":"bob"},"created_at":"2026-01-09T00:00:00Z"},
				{"id":17,"type":"add_dependency","dependent_issue":{"number":2,"repository":{"full_name":"alice/secret"}},"user":{"login":"bob"},"created_at":"2026-01-10T00:00:00Z"},
				{"id":18,"type":"add_time_manual","body":"30 minutes","user":{"login":"bob"},"created_at":"2026-01-11T00:00:00Z"},
				{"id":19,"type":"start_tracking","body":"","user":{"login":"bob"},"created_at":"2026-01-11T00:00:00Z"},
				{"id":20,"type":"stop_tracking","body":"2 seconds","user":{"login":"bob"},"created_at":"2026-01-11T00:00:00Z"},
				{"id":21,"type":"cancel_tracking","body":"","user":{"login":"bob"},"created_at":"2026-01-11T00:00:00Z"},
				{"id":22,"type":"delete_time_manual","body":"30 minutes","user":{"login":"bob"},"created_at":"2026-01-11T00:00:00Z"}]`)
		case "/api/v1/repos/alice/other/issues/9":
			_, _ = w.Write([]byte(`{"number":9}`))
		case "/api/v1/repos/alice/secret/issues/2":
			// Gitea names issues of private repositories in the timeline of public ones.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project"}))
	require.Equal(t, "2026-01-01T00:00:00Z\tbob\tadded label \"bug\"\n2026-01-02T00:00:00Z\tbob\trenamed \"crash\" to \"Crash\"\n2026-01-03T00:00:00Z\talice\tcommented: Fixed in main\n2026-01-03T00:00:00Z\talice\tclosed\n"+
		"2026-01-04T00:00:00Z\tbob\tassigned carol\n2026-01-04T00:00:00Z\tbob\tunassigned carol\n"+
		"2026-01-05T00:00:00Z\tbob\tadded dependency #4\n2026-01-05T00:00:00Z\tbob\tremoved dependency alice/other#9\n"+
		"2026-01-06T00:00:00Z\tbob\trequested a review from dave\n2026-01-06T00:00:00Z\tbob\tremoved the review request for dave\n"+
		"2026-01-07T00:00:00Z\tbob\tset the due date to 2026-12-01\n2026-01-07T00:00:00Z\tbob\tchanged the due date from 2026-12-01 to 2027-01-01\n"+
		"2026-01-07T00:00:00Z\tbob\tremoved the due date 2027-01-01\n"+
		"2026-01-08T00:00:00Z\tbob\trequested a review from team devs\n2026-01-08T00:00:00Z\tbob\tremoved the review request for team devs\n"+
		"2026-01-09T00:00:00Z\tbob\tchanged the milestone\n2026-01-10T00:00:00Z\tbob\tadded a dependency\n"+
		"2026-01-11T00:00:00Z\tbob\tlogged 30 minutes\n2026-01-11T00:00:00Z\tbob\tstarted the stopwatch\n2026-01-11T00:00:00Z\tbob\tstopped the stopwatch, logging 2 seconds\n"+
		"2026-01-11T00:00:00Z\tbob\tcanceled the stopwatch\n2026-01-11T00:00:00Z\tbob\tdeleted 30 minutes of logged time\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "type,label", "--jq", ".[0]"}))
	require.JSONEq(t, `{"type":"label","label":"bug"}`, out.String())
	// Every key is present, and only comments and reviews carry a body.
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "--jq", ".[:3]"}))
	require.JSONEq(t, `[
		{"id":1,"type":"label","author":"bob","url":"","created_at":"2026-01-01T00:00:00Z","body":null,"label":"bug","label_added":true,
		 "milestone":null,"old_milestone":null,"old_title":null,"new_title":null,"assignee":null,"assignee_team":null,"removed_assignee":null,"dependent_issue":null,"deadline":null,"old_deadline":null,"duration":null},
		{"id":2,"type":"change_title","author":"bob","url":"","created_at":"2026-01-02T00:00:00Z","body":null,"label":null,"label_added":null,
		 "milestone":null,"old_milestone":null,"old_title":"crash","new_title":"Crash","assignee":null,"assignee_team":null,"removed_assignee":null,"dependent_issue":null,"deadline":null,"old_deadline":null,"duration":null},
		{"id":3,"type":"comment","author":"alice","url":"","created_at":"2026-01-03T00:00:00Z","body":"Fixed in main\nThanks","label":null,"label_added":null,
		 "milestone":null,"old_milestone":null,"old_title":null,"new_title":null,"assignee":null,"assignee_team":null,"removed_assignee":null,"dependent_issue":null,"deadline":null,"old_deadline":null,"duration":null}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "assignee,removed_assignee,dependent_issue", "--jq", ".[5:9]"}))
	require.JSONEq(t, `[{"assignee":"carol","removed_assignee":true,"dependent_issue":null},
		{"assignee":null,"removed_assignee":null,"dependent_issue":"alice/project#4"},
		{"assignee":null,"removed_assignee":null,"dependent_issue":"alice/other#9"},
		{"assignee":"dave","removed_assignee":false,"dependent_issue":null}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "body,deadline,old_deadline", "--jq", ".[10:13]"}))
	require.JSONEq(t, `[{"body":null,"deadline":"2026-12-01","old_deadline":null},
		{"body":null,"deadline":"2027-01-01","old_deadline":"2026-12-01"},
		{"body":null,"deadline":null,"old_deadline":"2027-01-01"}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "assignee,assignee_team,removed_assignee", "--jq", ".[13:15]"}))
	require.JSONEq(t, `[{"assignee":null,"assignee_team":"devs","removed_assignee":false},{"assignee":null,"assignee_team":"devs","removed_assignee":true}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "body,duration", "--jq", ".[17:22]"}))
	require.JSONEq(t, `[{"body":null,"duration":"30 minutes"},{"body":null,"duration":null},{"body":null,"duration":"2 seconds"},{"body":null,"duration":null},{"body":null,"duration":"30 minutes"}]`, out.String())
	out.Reset()
	plainTTY(t, &deps, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project"}))
	require.Contains(t, out.String(), "Showing 22 of 22 events on alice/project#3\n")
}

func TestPullTimelineSelectsThePullRequestLikeOtherPullCommands(t *testing.T) {
	var timelines int
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/issues/9":
			_, _ = w.Write([]byte(`{"number":9,"title":"Feature","pull_request":{}}`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues/9/timeline":
			timelines++
			_, _ = w.Write([]byte(`[{"id":1,"type":"close","user":{"login":"bob"},"created_at":"2026-01-03T00:00:00Z"}]`))
		case r.URL.Path == "/api/v1/settings/api":
			_, _ = w.Write([]byte(`{"max_response_items":50}`))
		case r.URL.Path == "/api/v1/repos/alice/project/pulls" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(`[{"number":9,"title":"Feature","state":"open","head":{"ref":"feature","sha":"abc","repo":{"full_name":"alice/project"}}}]`))
		case r.URL.Path == "/api/v1/repos/alice/project/pulls":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feature"}}
	for _, args := range [][]string{{"pr", "timeline"}, {"pr", "timeline", "feature"}, {"pr", "timeline", "9"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args), "%v", args)
		require.Equal(t, "2026-01-03T00:00:00Z\tbob\tclosed\n", out.String(), "%v", args)
	}
	require.Equal(t, 3, timelines)
	// issue timeline still takes exactly one issue.
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"issue", "timeline"})))
}
