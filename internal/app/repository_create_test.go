// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestRepositoryCreateGrantsTeamAccess(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/teams":
			// The server caps pages below the requested limit (MAX_RESPONSE_ITEMS=1).
			pages := map[string]string{"1": `[{"id":4,"name":"Owners"}]`, "2": `[{"id":3,"name":"Developers"}]`}
			_, _ = w.Write([]byte(cmp.Or(pages[r.URL.Query().Get("page")], `[]`)))
		case "POST /api/v1/orgs/acme/repos":
			_, _ = w.Write([]byte(`{"name":"project","full_name":"acme/project"}`))
		case "PUT /api/v1/teams/3/repos/acme/project":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "developers", "--no-input", "--json=full_name"}))
	teamPages := []string{"GET /api/v1/orgs/acme/teams?limit=50&page=1", "GET /api/v1/orgs/acme/teams?limit=50&page=2", "GET /api/v1/orgs/acme/teams?limit=50&page=3"}
	require.Equal(t, append(teamPages, "POST /api/v1/orgs/acme/repos", "PUT /api/v1/teams/3/repos/acme/project"), requests)
	require.JSONEq(t, `{"full_name":"acme/project"}`, out.String())
	requests = nil
	out.Reset()
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "missing", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `team "missing" not found in organization acme`)
	require.Equal(t, teamPages, requests)
	require.Empty(t, out.String())
}

func TestRepositoryCreateClonesNewRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = fmt.Fprintf(w, `{"id":7,"name":"project","full_name":"alice/project","clone_url":%q}`, "http://"+r.Host+"/alice/project.git")
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	call := credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/alice/project.git")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--clone", "--no-input", "--json=id,full_name"}))
	require.Equal(t, []string{"POST /api/v1/user/repos"}, requests)
	require.Equal(t, []string{call}, runner.calls)
	require.JSONEq(t, `{"id":7,"full_name":"alice/project"}`, out.String())
}

func TestRepositoryCreateReportsFailedCloneAfterCreating(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":7,"name":"project","full_name":"alice/project","clone_url":%q}`, "http://"+r.Host+"/alice/project.git")
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{}
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--clone", "--no-input", "--json=full_name"})
	require.ErrorContains(t, err, "created alice/project, but could not clone it")
	require.JSONEq(t, `{"full_name":"alice/project"}`, out.String())
}

func TestRepositoryCreateValidatesTemplatesBeforeCreating(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/licenses":
			_, _ = w.Write([]byte(`[{"key":"MIT","name":"MIT"},{"key":"Apache-2.0","name":"Apache-2.0"}]`))
		case "/api/v1/gitignore/templates":
			_, _ = w.Write([]byte(`["Go","Node"]`))
		case "/api/v1/user/repos":
			requirePayload(t, `{"name":"project","auto_init":true,"license":"MIT","gitignores":"Go,Node"}`, r.Body)
			_, _ = w.Write([]byte(`{"full_name":"alice/project","html_url":"https://gitea.test/alice/project"}`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--auto-init", "--license", "NOPE"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, `unknown license "NOPE"`)
	require.NotContains(t, requests, "POST /api/v1/user/repos")
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "new", "project", "--auto-init", "-l", "mit", "-g", "go,Node"}))
	require.Equal(t, "https://gitea.test/alice/project\n", out.String())
	require.Equal(t, "✓ Created repository alice/project\n", diagnostics.String())
}

func TestRepositoryCreatePublishesALocalRepository(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePayload(t, `{"name":"project"}`, r.Body)
		_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","html_url":"https://gitea.test/alice/project","clone_url":"http://%s/alice/project.git"}`, r.Host)
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	dir := t.TempDir()
	remote := server.URL + "/alice/project.git"
	push := credentialCall(t, deps, server.URL, "alice", "push", "--quiet", "--set-upstream", "origin", "HEAD")
	runner := &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", "remote add -- origin " + remote: "", push: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--public", "--source", dir, "--push"}))
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "remote get-url origin", "remote add -- origin " + remote, push}, runner.calls)
	require.Equal(t, []string{dir, dir, dir, dir}, runner.dirs)
	require.Equal(t, "✓ Created repository alice/project\n✓ Added Git remote origin for alice/project\n✓ Pushed the current branch to alice/project\n", diagnostics.String())
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--auto-init"})
	require.ErrorContains(t, err, "--source publishes a local repository")
}

func TestRepositoryMigrateAcceptsZeroMirrorInterval(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePayload(t, `{"repo_name":"gitea","clone_addr":"https://github.com/go-gitea/gitea.git","service":"git","mirror":true,"mirror_interval":"0"}`, r.Body)
		_, _ = w.Write([]byte(`{"full_name":"alice/gitea","mirror":true,"html_url":"https://gitea.test/alice/gitea"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "migrate", "https://github.com/go-gitea/gitea.git", "--mirror", "--mirror-interval", "0"}))
	require.Equal(t, "✓ Imported https://github.com/go-gitea/gitea.git into alice/gitea (pull mirror, synced manually)\n", diagnostics.String())
}

func TestRepositoryMigrateNamesTheRepositoryAfterItsSource(t *testing.T) {
	for _, test := range []struct{ source, want string }{
		{"https://github.com/go-gitea/gitea.git", "gitea"},
		{"https://example.com/group/project/", "project"},
		{"/srv/git/legacy.git", "legacy"},
		{"https://example.com/", ""},
	} {
		require.Equal(t, test.want, migrationName(test.source), test.source)
	}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePayload(t, `{"repo_name":"gitea","clone_addr":"https://github.com/go-gitea/gitea.git","service":"git","mirror":true,"mirror_interval":"8h"}`, r.Body)
		_, _ = w.Write([]byte(`{"full_name":"alice/gitea","mirror":true,"html_url":"https://gitea.test/alice/gitea"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "migrate", "https://github.com/go-gitea/gitea.git", "--mirror", "--mirror-interval", "8h"}))
	require.Equal(t, "https://gitea.test/alice/gitea\n", out.String())
	require.Equal(t, "✓ Imported https://github.com/go-gitea/gitea.git into alice/gitea (pull mirror, every 8h)\n", diagnostics.String())
	err := Execute(context.Background(), deps, []string{"repo", "migrate", "https://github.com/go-gitea/gitea.git", "--service", "GitHub", "--issues"})
	require.ErrorContains(t, err, "importing from github needs --auth-token or --auth-token-file")
}
