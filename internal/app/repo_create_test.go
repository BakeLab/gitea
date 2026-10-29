// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestRepositoryCreateGrantsTeamAccess(t *testing.T) {
	var requests []string
	owner, repoAdmins, member := true, false, http.StatusOK
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/users/alice/orgs/acme/permissions":
			_, _ = fmt.Fprintf(w, `{"is_owner":%t,"is_admin":true}`, owner)
		case "GET /api/v1/orgs/acme":
			_, _ = fmt.Fprintf(w, `{"name":"acme","repo_admin_change_team_access":%t}`, repoAdmins)
		case "GET /api/v1/teams/3/members/alice":
			w.WriteHeader(member)
			_, _ = w.Write([]byte(`{"login":"alice"}`))
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
	permissions := "GET /api/v1/users/alice/orgs/acme/permissions"
	require.Equal(t, append(teamPages, permissions, "POST /api/v1/orgs/acme/repos", "PUT /api/v1/teams/3/repos/acme/project"), requests)
	require.JSONEq(t, `{"full_name":"acme/project"}`, out.String())
	// An organization administrator who is no owner cannot grant teams access: nothing is created.
	owner, requests = false, nil
	out.Reset()
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "developers", "--no-input"})
	require.EqualError(t, err, "--team requires an owner of acme, a site administrator, or a member of team Developers when acme lets repository administrators change team access")
	require.Equal(t, append(teamPages, permissions, "GET /api/v1/orgs/acme"), requests)
	// Unless the organization lets repository administrators change team access and the creator,
	// who administers the new repository, is a member of the team.
	repoAdmins, requests = true, nil
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "developers", "--no-input"}))
	membership := "GET /api/v1/teams/3/members/alice"
	require.Equal(t, append(teamPages, permissions, "GET /api/v1/orgs/acme", membership, "POST /api/v1/orgs/acme/repos", "PUT /api/v1/teams/3/repos/acme/project"), requests)
	member, requests = http.StatusForbidden, nil
	err = Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "developers", "--no-input"})
	require.ErrorContains(t, err, "--team requires an owner of acme")
	require.Equal(t, append(teamPages, permissions, "GET /api/v1/orgs/acme", membership), requests)
	requests = nil
	out.Reset()
	err = Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "missing", "--no-input"})
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.ErrorContains(t, err, `team "missing" not found in organization acme`)
	require.Equal(t, teamPages, requests)
	require.Empty(t, out.String())
}

func TestRepositoryCreateForTheActingAccount(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Sudo"))
		if strings.HasSuffix(r.URL.Path, "/generate") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"this is not a template repo"}`))
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"owner/project"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"repo", "create", "project", "--org", "ALICE"}, "POST /api/v1/user/repos "},
		{[]string{"--sudo", "bob", "repo", "create", "bob/project"}, "POST /api/v1/user/repos bob"},
		{[]string{"--sudo", "bob", "repo", "create", "alice/project"}, "POST /api/v1/orgs/alice/repos bob"},
	} {
		requests = nil
		require.NoError(t, Execute(context.Background(), deps, append(test.args, "--no-input")))
		require.Equal(t, []string{test.want}, requests, "%v", test.args)
	}
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--template", "alice/plain", "--no-input"})
	require.EqualError(t, err, "--template requires a template repository; mark it with gt repo edit alice/plain --as-template")
}

func TestRepositoryCreateClonesNewRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = fmt.Fprintf(w, `{"id":7,"name":"project","full_name":"alice/project","clone_url":%q}`, "http://"+r.Host+"/alice/project.git")
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	call := credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/alice/project.git", "project")
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

type repoFormFunc func(terminal.RepoFormChoices) (terminal.RepoFormValues, error)

func (f repoFormFunc) CreateRepo(_ context.Context, choices terminal.RepoFormChoices) (terminal.RepoFormValues, error) {
	return f(choices)
}

func TestRepositoryCreateInteractiveForm(t *testing.T) {
	var mu sync.Mutex // the form's choices load concurrently
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/user/orgs":
			writeFirstPage(w, r, `[{"username":"acme"}]`)
		case "GET /api/v1/licenses":
			_, _ = w.Write([]byte(`[{"key":"MIT","name":"MIT"}]`))
		case "GET /api/v1/gitignore/templates":
			_, _ = w.Write([]byte(`["Go","Node"]`))
		case "POST /api/v1/orgs/acme/repos":
			requirePayload(t, `{"name":"tool","description":"A tool","private":true,"auto_init":true,"license":"MIT","gitignores":"Go,Node"}`, r.Body)
			_, _ = fmt.Fprintf(w, `{"name":"tool","full_name":"acme/tool","clone_url":%q}`, "http://"+r.Host+"/acme/tool.git")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	call := credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/acme/tool.git", "tool")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	var offered terminal.RepoFormChoices
	deps.RepoForm = repoFormFunc(func(choices terminal.RepoFormChoices) (terminal.RepoFormValues, error) {
		offered = choices
		return terminal.RepoFormValues{Owner: "acme", Name: "tool", Description: "A tool", Private: true, AutoInit: true, License: "MIT", Gitignores: []string{"Go", "Node"}, Clone: true}, nil
	})
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "--yes", "--json=full_name", "--interactive"}))
	require.Equal(t, []string{"alice", "acme"}, offered.Owners)
	require.Equal(t, []string{"MIT"}, offered.Licenses)
	require.Equal(t, []string{"Go", "Node"}, offered.Gitignores)
	require.Contains(t, requests, "POST /api/v1/orgs/acme/repos")
	require.Equal(t, []string{call}, runner.calls)
	require.JSONEq(t, `{"full_name":"acme/tool"}`, out.String())

	// --no-input or a flag skips the form.
	deps.RepoForm = repoFormFunc(func(terminal.RepoFormChoices) (terminal.RepoFormValues, error) {
		t.Fatal("form should not run")
		return terminal.RepoFormValues{}, nil
	})
	err := Execute(context.Background(), deps, []string{"repo", "create", "--no-input"})
	require.EqualError(t, err, "repo create requires a repository name argument")
	requests = nil
	err = Execute(context.Background(), deps, []string{"repo", "create", "--license", "MIT"})
	require.EqualError(t, err, "--license requires --auto-init")
	require.Empty(t, requests)
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
	created := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		created++
		requirePayload(t, `{"name":"project","object_format_name":"sha256"}`, r.Body)
		_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","html_url":"https://gitea.test/alice/project","clone_url":"http://%s/alice/project.git","empty":true,"default_branch":"main"}`, r.Host)
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	dir := t.TempDir()
	remote := server.URL + "/alice/project.git"
	push := credentialCall(t, deps, server.URL, "alice", "push", "--quiet", "--set-upstream", "--", "origin", "HEAD")
	head, branch, format := "rev-parse --verify --quiet HEAD^{commit}", "symbolic-ref --quiet HEAD", "rev-parse --show-object-format"
	runner := &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", head: "abc", branch: "refs/heads/trunk", format: "sha256", "remote add -- origin " + remote: "", push: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--public", "--source", dir, "--push"}))
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "remote get-url origin", head, branch, format, "remote add -- origin " + remote, push}, runner.calls)
	require.Equal(t, []string{dir, dir, dir, dir, dir, dir, dir}, runner.dirs)
	require.Equal(t, "✓ Created repository alice/project\n✓ Added Git remote origin for alice/project\n✓ Pushed the current branch to alice/project\n", diagnostics.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--push", "--json=empty,default_branch"}))
	require.JSONEq(t, `{"empty":false,"default_branch":"trunk"}`, out.String())
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--auto-init"})
	require.ErrorContains(t, err, "--source publishes a local repository")
	runner.calls, created = nil, 0
	err = Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--remote-name", "-x"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "--remote-name requires a valid Git remote name")
	require.Empty(t, runner.calls)
	require.Zero(t, created)
	err = Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--object-format", "sha1"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "--object-format sha1 cannot be combined with --source "+dir+", a sha256 repository")
	require.Zero(t, created)
	// On a detached HEAD, nothing is created.
	delete(runner.results, branch)
	runner.calls = nil
	created = 0
	err = Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--push"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "--push requires a checked-out branch in --source "+dir)
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "remote get-url origin", head, branch}, runner.calls)
	require.Zero(t, created)
	// Without a commit, nothing is created.
	delete(runner.results, head)
	runner.calls = nil
	created = 0
	err = Execute(context.Background(), deps, []string{"repo", "create", "project", "--source", dir, "--push"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "--push requires a commit in --source "+dir+"; commit first")
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "remote get-url origin", head}, runner.calls)
	require.Zero(t, created)
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
}

func TestRepositoryMigrateFromAForgeWithoutAToken(t *testing.T) {
	var payloads []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		payloads = append(payloads, string(body))
		_, _ = w.Write([]byte(`{"full_name":"alice/gitea","html_url":"https://gitea.test/alice/gitea"}`))
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	deps.Streams.In = strings.NewReader("source-secret\n")
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "migrate", "https://github.com/go-gitea/gitea.git", "--service", "GitHub", "--issues"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "migrate", "https://gitlab.com/acme/tool", "--service", "gitlab", "--auth-user", "bob", "--auth-password-file", "-"}))
	require.Len(t, payloads, 2)
	requirePayload(t, `{"repo_name":"gitea","clone_addr":"https://github.com/go-gitea/gitea.git","service":"github","issues":true}`, strings.NewReader(payloads[0]))
	requirePayload(t, `{"repo_name":"tool","clone_addr":"https://gitlab.com/acme/tool","service":"gitlab","auth_username":"bob","auth_password":"source-secret"}`, strings.NewReader(payloads[1]))
}

func TestRepositoryMigrateDebugRedactsSourceCredentials(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Password string `json:"auth_password"`
			Token    string `json:"auth_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		secret := payload.Password + payload.Token
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = fmt.Fprintf(w, `{"message":"cannot clone https://bob:%s@source.test/repo with %s"}`, url.QueryEscape(secret), secret)
	}))
	defer server.Close()
	for _, credential := range [][]string{{"--auth-password", "p@ss/w", "--auth-user", "bob"}, {"--auth-token", "source-pat"}} {
		deps, _, diagnostics := repoTTYDeps(t, server.URL)
		err := Execute(context.Background(), deps, append([]string{"--debug", "repo", "migrate", "https://gitlab.com/acme/tool", "--service", "gitlab"}, credential...))
		require.Error(t, err)
		require.Contains(t, diagnostics.String(), "[debug] POST "+server.URL+"/api/v1/repos/migrate -> 422 Unprocessable Entity", credential[0])
		require.Contains(t, diagnostics.String(), ": cannot clone https://bob:[REDACTED]@source.test/repo with [REDACTED]\n", credential[0])
		for _, form := range []string{credential[1], url.QueryEscape(credential[1])} {
			require.NotContains(t, diagnostics.String(), form, credential[0])
			require.NotContains(t, err.Error(), form, credential[0])
		}
	}
}
