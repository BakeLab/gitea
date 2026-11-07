// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

const activityFeed = `[
 {"id":9,"op_type":"commit_repo","act_user":{"login":"alice"},"repo":{"full_name":"acme/project"},"ref_name":"refs/heads/main","content":"{\"Commits\":[{\"Sha1\":\"b\",\"Message\":\"Add tests\\n\\nbody\"},{\"Sha1\":\"a\",\"Message\":\"Fix login\"}],\"Len\":2}","created":"2026-10-11T17:00:00Z"},
 {"id":8,"op_type":"comment_issue","act_user":{"login":"bob"},"repo":{"full_name":"acme/project"},"content":"12|Looks good to me","comment_id":77,"created":"2026-10-11T16:00:00Z"},
 {"id":7,"op_type":"create_issue","act_user":{"login":"carol"},"repo":{"full_name":"acme/project"},"content":"12|Crash on start","is_private":true,"created":"2026-10-11T15:00:00Z"},
 {"id":6,"op_type":"push_tag","act_user":{"login":"alice"},"repo":{"full_name":"acme/project"},"ref_name":"refs/tags/v1.2.0","content":"{\"Commits\":null}","created":"2026-10-11T14:00:00Z"},
 {"id":5,"op_type":"publish_release","act_user":{"login":"alice"},"repo":{"full_name":"acme/project"},"ref_name":"refs/tags/v1.2.0","content":"Version 1.2","created":"2026-10-11T13:00:00Z"},
 {"id":4,"op_type":"close_issue","act_user":{"login":"bob"},"repo":{"full_name":"acme/project"},"content":"12|","created":"2026-10-11T12:00:00Z"},
 {"id":3,"op_type":"something_new","act_user":{"login":"bob"},"repo":{"full_name":"acme/project"},"content":"","created":"2026-10-11T11:00:00Z"}]`

func TestActivityFeeds(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		mu.Lock()
		requests = append(requests, path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch path {
		case "/orgs/acme":
			_, _ = w.Write([]byte(`{"id":3,"username":"acme"}`))
		case "/orgs/alice", "/orgs/nobody", "/orgs/quiet":
			w.WriteHeader(http.StatusNotFound)
		case "/repos/acme/project":
			_, _ = w.Write([]byte(`{"full_name":"acme/project"}`))
		case "/users/quiet/activities/feeds", "/repos/quiet/project/activities/feeds":
			_, _ = w.Write([]byte(`[]`))
		case "/users/alice/activities/feeds", "/orgs/acme/activities/feeds", "/repos/acme/project/activities/feeds", "/repos/alice/other/activities/feeds":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(activityFeed))
		case "/users/nobody/activities/feeds":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Errorf("unexpected %s", path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		list := requests
		requests = nil
		return list
	}
	run := func(args ...string) (string, error) {
		deps, out, _ := serverDeps(t, server.URL)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "acme/project"})
		err := Execute(context.Background(), deps, append(args, "--no-input"))
		return out.String(), err
	}

	out, err := run("activity", "alice", "--date", "2026-10-11", "--only-performed-by")
	require.NoError(t, err)
	require.Equal(t, []string{"/orgs/alice?", "/users/alice/activities/feeds?date=2026-10-11&limit=30&only-performed-by=true&page=1"}, sortedQueries(take()), "a user is not an organization, so its own feed")
	require.Equal(t, strings.Join([]string{
		"2026-10-11T17:00:00Z\talice\tpushed to\tacme/project\t2 commits to main: Add tests",
		"2026-10-11T16:00:00Z\tbob\tcommented on issue\tacme/project\t#12: Looks good to me",
		"2026-10-11T15:00:00Z\tcarol\topened issue\tacme/project\t#12 Crash on start",
		"2026-10-11T14:00:00Z\talice\tpushed the tag\tacme/project\tv1.2.0",
		"2026-10-11T13:00:00Z\talice\tpublished the release\tacme/project\tVersion 1.2 (v1.2.0)",
		"2026-10-11T12:00:00Z\tbob\tclosed issue\tacme/project\t#12",
		"2026-10-11T11:00:00Z\tbob\tsomething new\tacme/project\t",
	}, "\n")+"\n", out)

	_, err = run("activity", "acme")
	require.NoError(t, err)
	require.Equal(t, []string{"/orgs/acme?", "/orgs/acme/activities/feeds?limit=30&page=1"}, sortedQueries(take()), "an organization has its own feed")

	out, err = run("repo", "activity", "--json", "id,type,actor,repository,ref,content,private,comment_id,created_at", "--jq", ".[1]")
	require.NoError(t, err)
	require.JSONEq(t, `{"id":8,"type":"comment_issue","actor":"bob","repository":"acme/project","ref":"","content":"12|Looks good to me","private":false,"comment_id":77,"created_at":"2026-10-11T16:00:00Z"}`, out)
	require.Equal(t, []string{"/repos/acme/project/activities/feeds?limit=30&page=1"}, sortedQueries(take()))

	_, err = run("repo", "activity", "alice/other")
	require.NoError(t, err)
	require.Equal(t, []string{"/repos/alice/other/activities/feeds?limit=30&page=1"}, sortedQueries(take()), "the argument wins over the repository of the context")

	_, err = run("activity", "acme", "--only-performed-by")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--only-performed-by cannot be combined with an organization")
	require.Equal(t, []string{"/orgs/acme?"}, sortedQueries(take()), "refused once the owner is known to be an organization, before reading its feed")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"repo", "activity", "acme/project", "-R", "acme/project"}, "a repository argument cannot be combined with --repo"},
		{[]string{"activity", "--date", "yesterday"}, `--date requires a day as YYYY-MM-DD, got "yesterday"`},
		{[]string{"repo", "activity", "--date", "2026-13-40"}, "--date requires a day as YYYY-MM-DD"},
		{[]string{"activity", "-R", "acme/project"}, "did you mean: gt repo activity -R acme/project"},
	} {
		take()
		_, err := run(tc.args...)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), tc.args)
		require.ErrorContains(t, err, tc.want)
		require.Empty(t, take(), "a bad option is refused before any request")
	}
	_, err = run("activity", "nobody")
	require.ErrorContains(t, err, `user "nobody"`)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"activity", "quiet"}, "no activity for quiet\n"},
		{[]string{"repo", "activity", "quiet/project"}, "no activity for quiet/project\n"},
	} {
		deps, _, diagnostics := serverDeps(t, server.URL)
		deps.Streams.OutTTY = true
		require.NoError(t, Execute(context.Background(), deps, append(tc.args, "--no-input")))
		require.Equal(t, tc.want, diagnostics.String())
	}
	deps, tty, _ := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "activity", "acme/project", "--no-input"}))
	require.Contains(t, tty.String(), "activity of acme/project\n")
}

func TestActivityDetail(t *testing.T) {
	for _, tc := range []struct {
		item services.Activity
		want string
	}{
		{services.Activity{Type: "commit_repo", Ref: "refs/heads/main", Content: ""}, "main"},
		{services.Activity{Type: "commit_repo", Ref: "refs/heads/dev", Content: "not json"}, "dev"},
		{services.Activity{Type: "commit_repo", Ref: "refs/heads/dev", Content: `{"Commits":[{"Message":"One"}],"Len":1}`}, "1 commit to dev: One"},
		{services.Activity{Type: "commit_repo", Ref: "refs/heads/dev", Content: `{"Commits":[{"Message":"Newest\n\nbody"},{"Message":"Older"}],"Len":60}`}, "60 commits to dev: Newest"},
		{services.Activity{Type: "commit_repo", Ref: "refs/heads/dev", Content: `{"Commits":[],"Len":3}`}, "3 commits to dev"},
		{services.Activity{Type: "commit_repo", Ref: "refs/heads/dev", Content: `{"Commits":null,"Len":3}`}, "3 commits to dev"},
		{services.Activity{Type: "comment_pull", Content: "5|text | with a bar"}, "#5: text | with a bar"},
		{services.Activity{Type: "comment_issue", Content: "5|"}, "#5"},
		{services.Activity{Type: "pull_review_dismissed", Content: "5|alice|out of date | stale"}, "#5 by alice: out of date | stale"},
		{services.Activity{Type: "pull_review_dismissed", Content: "5|alice|"}, "#5 by alice"},
		{services.Activity{Type: "merge_pull_request", Content: "5|Title"}, "#5 Title"},
		{services.Activity{Type: "create_issue", Content: "not a number|x"}, "not a number|x"},
		{services.Activity{Type: "delete_branch", Ref: "refs/heads/old"}, "old"},
	} {
		require.Equal(t, tc.want, activityDetail(tc.item), tc.item.Type+" "+tc.item.Content)
	}
}

// sortedQueries sorts the parameters of each request, so that a test does not depend on their order.
func sortedQueries(requests []string) []string {
	for i, request := range requests {
		path, query, _ := strings.Cut(request, "?")
		parameters := strings.Split(query, "&")
		slices.Sort(parameters)
		requests[i] = path + "?" + strings.Join(parameters, "&")
	}
	return requests
}
