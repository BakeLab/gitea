// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"

	"github.com/stretchr/testify/require"
)

// syncGit answers the Git commands of a local repo sync in a clone whose origin is url, with
// experiment checked out and main one commit behind origin.
type syncGit struct {
	url, inside string
	calls       []string
}

func (g *syncGit) Output(_ context.Context, _ string, _ bool, args ...string) (string, error) {
	for len(args) > 2 && args[0] == "-c" {
		args = args[2:]
	}
	call := strings.Join(args, " ")
	g.calls = append(g.calls, call)
	switch call {
	case "rev-parse --is-inside-work-tree":
		if g.inside == "" {
			return "", errors.New("not a git repository")
		}
		return g.inside, nil
	case "remote get-url origin":
		return g.url, nil
	case "symbolic-ref --quiet HEAD":
		return "refs/heads/experiment", nil
	case "rev-parse --verify --quiet refs/heads/main^{commit}":
		return "1111111aaaa", nil
	case "rev-parse --verify FETCH_HEAD^{commit}":
		return "2222222bbbb", nil
	}
	return "", nil
}

func TestRepoSyncFastForwardsWithoutSwitchingBranches(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	runner := &syncGit{url: server.URL + "/alice/project.git", inside: "true"}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "sync", "-R", "alice/project", "--remote", "origin", "-b", "main"}))
	require.Empty(t, out.String())
	require.Equal(t, "✓ Synced main from origin/main (fast-forward 1111111..2222222)\n", diagnostics.String())
	require.Contains(t, runner.calls, "update-ref -m gt repo sync refs/heads/main 2222222bbbb 1111111aaaa")
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "switch") || strings.HasPrefix(call, "checkout") || strings.HasPrefix(call, "merge "), call)
	}
}

func TestRepoSyncUpdatesAForkOrMirrorOnTheServer(t *testing.T) {
	for _, test := range []struct {
		name, repo, feedback string
		requests             []string
		json                 string
	}{
		{
			name: "fork", repo: `{"full_name":"bob/project","fork":true,"default_branch":"main","parent":{"full_name":"alice/project"}}`,
			requests: []string{"GET /api/v1/repos/bob/project", "GET /api/v1/repos/bob/project/branches/main", "POST /api/v1/repos/bob/project/merge-upstream", "GET /api/v1/repos/bob/project/branches/main"},
			feedback: "✓ Synced bob/project main from alice/project (fast-forward 1111111..2222222)\n",
			json:     `{"branch":"main","commit":"2222222bbbb","previous":"1111111aaaa","remote":"alice/project"}`,
		},
		{
			name: "mirror", repo: `{"full_name":"bob/project","mirror":true,"original_url":"https://example.test/source.git"}`,
			requests: []string{"GET /api/v1/repos/bob/project", "POST /api/v1/repos/bob/project/mirror-sync"},
			feedback: "✓ Started updating the mirror bob/project from its source\n",
			json:     `{"branch":"","commit":"","previous":"","remote":"https://example.test/source.git"}`,
		},
		{
			name: "mirror of a path", repo: `{"full_name":"bob/project","mirror":true,"original_url":"/srv/source.git"}`,
			requests: []string{"GET /api/v1/repos/bob/project", "POST /api/v1/repos/bob/project/mirror-sync"},
			feedback: "✓ Started updating the mirror bob/project from its source\n",
			json:     `{"branch":"","commit":"","previous":"","remote":"/srv/source.git"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []string
			synced := false
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.URL.Path {
				case "/api/v1/repos/bob/project":
					_, _ = w.Write([]byte(test.repo))
				case "/api/v1/repos/bob/project/branches/main":
					commit := "1111111aaaa"
					if synced {
						commit = "2222222bbbb"
					}
					_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"` + commit + `"}}`))
				case "/api/v1/repos/bob/project/merge-upstream":
					requirePayload(t, `{"branch":"main","ff_only":true}`, r.Body)
					synced = true
					_, _ = w.Write([]byte(`{"merge_type":"fast-forward"}`))
				default:
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()
			deps, out, diagnostics := repoTTYDeps(t, server.URL)
			deps.Git = &syncGit{}
			require.NoError(t, Execute(context.Background(), deps, []string{"repo", "sync", "bob/project"}))
			require.Equal(t, test.requests, requests)
			require.Equal(t, test.feedback, diagnostics.String())
			require.Empty(t, out.String())
			deps, out, _ = serverDeps(t, server.URL)
			deps.Git, synced, requests = &syncGit{}, false, nil
			require.NoError(t, Execute(context.Background(), deps, []string{"repo", "sync", "bob/project", "--json"}))
			require.JSONEq(t, test.json, out.String())
		})
	}
}

func TestRepoSyncOutsideACloneExplainsWhatItNeeds(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &syncGit{}
	err := Execute(context.Background(), deps, []string{"repo", "sync", "-R", "alice/project", "-b", "main"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "repo sync requires a Git clone of alice/project; run it inside one, or pass a fork to update it on the server")
}

func TestRepoSyncOnTheServerIgnoresARemotePreferenceForAnotherRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"full_name":"bob/project","mirror":true,"original_url":"https://example.test/source.git"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.Remote = "origin"; return nil }))
	// origin is a clone of alice/project, not of the repository named on the command line.
	deps.Git = &syncGit{url: server.URL + "/alice/project.git", inside: "true"}
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "sync", "bob/project"}))
	require.Equal(t, []string{"GET /api/v1/repos/bob/project", "POST /api/v1/repos/bob/project/mirror-sync"}, requests)
	// Gitea updates a mirror as a whole, so a branch of it cannot be synced.
	requests = nil
	err := Execute(context.Background(), deps, []string{"repo", "sync", "bob/project", "--branch", "develop"})
	require.EqualError(t, err, "--branch cannot be used for the mirror bob/project: Gitea updates a mirror as a whole")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Equal(t, []string{"GET /api/v1/repos/bob/project"}, requests)
}
