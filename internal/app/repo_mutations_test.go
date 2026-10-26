// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

// upstreamCheckout discovers the repository from the upstream remote, as in a clone of a fork.
func upstreamCheckout(server *httptest.Server) *fakeGit {
	return &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":       "main",
		"config --get branch.main.remote": "upstream",
		"remote get-url upstream":         server.URL + "/alice/project.git",
	}}
}

func TestRepoDeleteAndArchiveRequireExplicitRepositoryWithYes(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, verb := range []string{"delete", "archive"} {
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = upstreamCheckout(server)
		err := Execute(context.Background(), deps, []string{"repo", verb, "--yes"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, "--yes requires an explicit repository, not one inferred from git remote upstream: gt repo "+verb+" alice/project --yes")
	}
	require.Empty(t, requests)
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	deps.Git = upstreamCheckout(server)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "rm", "alice/project", "--yes"}))
	require.Equal(t, []string{"DELETE /api/v1/repos/alice/project"}, requests)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Deleted repository alice/project\n", diagnostics.String())
}

func TestRepoDeleteAsksToTypeTheRepositoryName(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, test := range []struct {
		answer  string
		deleted bool
	}{{"alice/other", false}, {"", false}, {"alice/project", true}} {
		requests = nil
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = upstreamCheckout(server)
		prompt := &scriptedPrompter{inputs: map[string]string{"Delete alice/project (from git remote upstream)? This cannot be undone. Type alice/project to confirm:": test.answer}}
		deps.Prompter = prompt
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		err := Execute(context.Background(), deps, []string{"repo", "delete"})
		require.Len(t, prompt.asked, 1)
		require.Empty(t, prompt.confirmations)
		if !test.deleted {
			require.Equal(t, clierr.Canceled, clierr.ExitCode(err), "%v", err)
			require.Empty(t, requests)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, []string{"DELETE /api/v1/repos/alice/project"}, requests)
	}
}

func TestRepoArchiveStates(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		archived bool
		requests []string
		feedback string
	}{
		{"archive", []string{"repo", "archive", "alice/x", "--yes"}, false, []string{"GET", "PATCH"}, "✓ Archived repository alice/x\n"},
		{"already archived", []string{"repo", "archive", "alice/x", "--yes"}, true, []string{"GET"}, "! alice/x is already archived\n"},
		{"unarchive", []string{"repo", "unarchive", "alice/x"}, true, []string{"GET", "PATCH"}, "✓ Unarchived repository alice/x\n"},
		{"not archived", []string{"repo", "unarchive", "alice/x"}, false, []string{"GET"}, "! alice/x is not archived\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests []string
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/x", r.URL.Path)
				requests = append(requests, r.Method)
				archived := test.archived
				if r.Method == http.MethodPatch {
					requirePayload(t, fmt.Sprintf(`{"archived":%t}`, !archived), r.Body)
					archived = !archived
				}
				_, _ = fmt.Fprintf(w, `{"full_name":"alice/x","archived":%t}`, archived)
			}))
			defer server.Close()
			deps, out, diagnostics := repoTTYDeps(t, server.URL)
			require.NoError(t, Execute(context.Background(), deps, test.args))
			require.Equal(t, test.requests, requests)
			require.Empty(t, out.String())
			require.Equal(t, test.feedback, diagnostics.String())
		})
	}
}

func TestRepoArchiveAsksWithTheConsequence(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = upstreamCheckout(server)
	prompt := &scriptedPrompter{}
	deps.Prompter, deps.Streams.InTTY, deps.Streams.ErrTTY = prompt, true, true
	err := Execute(context.Background(), deps, []string{"repo", "archive"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err), "%v", err)
	require.Equal(t, []string{"Archive alice/project (from git remote upstream)? It becomes read-only."}, prompt.confirmations)
}

func TestRepoRenameHintsAtTheRemote(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch+" /api/v1/repos/alice/project", r.Method+" "+r.URL.Path)
		requirePayload(t, `{"name":"renamed"}`, r.Body)
		_, _ = w.Write([]byte(`{"name":"renamed","full_name":"alice/renamed"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":       "main",
		"config --get branch.main.remote": "origin",
		"remote get-url origin":           server.URL + "/alice/project.git",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "rename", "renamed", "--yes"}))
	require.Empty(t, out.String())
	require.Equal(t, "✓ Renamed repository alice/project to alice/renamed\n! Git remote origin still points to alice/project; update it with git remote set-url\n", diagnostics.String())
}

func TestRepoForkReportsAnExistingFork(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/bob/project/forks":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"repository is already forked by user [uname: alice, repo path: bob/project, fork path: alice/project-fork]"}`))
		case "GET /api/v1/repos/bob/project/forks":
			writeFirstPage(w, r, `[{"full_name":"carol/project","owner":{"login":"carol"}},{"full_name":"alice/project-fork","owner":{"login":"alice"}}]`)
		case "GET /api/v1/repos/alice/project-fork":
			_, _ = w.Write([]byte(`{"full_name":"alice/project-fork","fork":true,"html_url":"https://gitea.test/alice/project-fork","parent":{"full_name":"bob/project"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "fork", "bob/project"}))
	require.Equal(t, "! alice/project-fork already exists\n", diagnostics.String())
	require.Equal(t, "https://gitea.test/alice/project-fork\n", out.String())
	require.Equal(t, "GET /api/v1/repos/alice/project-fork", requests[len(requests)-1])
	// The existing fork has another name than --fork-name asks for, so it is not the fork asked for.
	err := Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--fork-name", "other", "--clone"})
	require.EqualError(t, err, "--fork-name other was not applied: alice already forked bob/project as alice/project-fork, and Gitea allows one fork per owner")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--fork-name", "Project-Fork"}))
	require.Equal(t, "! alice/project-fork already exists\n", diagnostics.String())
}

func TestRepoForkRefusesTheOwnersRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Sudo"))
		_, _ = w.Write([]byte(`{"full_name":"bob/project"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"repo", "fork", "alice/project"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "cannot fork alice/project into alice: it already owns it; pass --org")
	err = Execute(context.Background(), deps, []string{"--sudo", "bob", "repo", "fork", "bob/project"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "cannot fork bob/project into bob: it already owns it")
	require.Empty(t, requests)
	require.NoError(t, Execute(context.Background(), deps, []string{"--sudo", "bob", "repo", "fork", "alice/project"}))
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/forks bob"}, requests)
}

func TestRepoForkAddsTheForkAsOrigin(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/mine":
			_, _ = w.Write([]byte(`{"full_name":"alice/mine","fork":true,"parent":{"full_name":"bob/project"}}`))
		case "GET /api/v1/repos/alice/other":
			_, _ = w.Write([]byte(`{"full_name":"alice/other"}`))
		default:
			require.Equal(t, "POST /api/v1/repos/bob/project/forks", r.Method+" "+r.URL.Path)
			requirePayload(t, `{"name":"mine"}`, r.Body)
			_, _ = fmt.Fprintf(w, `{"full_name":"alice/mine","html_url":"https://gitea.test/alice/mine","clone_url":"http://%s/alice/mine.git"}`, r.Host)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	fork := server.URL + "/alice/mine.git"
	runner := &fakeGit{results: map[string]string{
		"rev-parse --is-inside-work-tree":  "true",
		"remote get-url origin":            server.URL + "/bob/project.git",
		"remote rename -- origin upstream": "",
		"remote add -- origin " + fork:     "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--fork-name", "mine", "--remote"}))
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "remote get-url origin", "remote get-url upstream", "remote rename -- origin upstream", "remote add -- origin " + fork}, runner.calls)
	require.Equal(t, "✓ Forked bob/project to alice/mine\n✓ Renamed Git remote origin to upstream\n✓ Added Git remote origin for alice/mine\n", diagnostics.String())
	require.Equal(t, "https://gitea.test/alice/mine\n", out.String())
	// A second run finds origin pointing to the fork.
	runner.results["remote get-url origin"], runner.calls = fork, nil
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--fork-name", "mine", "--remote"}))
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "remote get-url origin"}, runner.calls)
	require.Equal(t, "✓ Forked bob/project to alice/mine\n! Git remote origin already points to alice/mine\n", diagnostics.String())
	// Another repository of the fork owner is refused before forking.
	runner.results["remote get-url origin"], requests = server.URL+"/alice/other.git", nil
	err := Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--fork-name", "mine", "--remote"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "Git remote origin points to alice/other; pass --remote-name")
	require.Equal(t, []string{"GET /api/v1/repos/alice/other"}, requests)
}

func TestRepoForkRemoteChecksTheCloneBeforeForking(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	for name, results := range map[string]map[string]string{
		"outside a clone":         {"rev-parse --is-inside-work-tree": "false"},
		"origin points elsewhere": {"rev-parse --is-inside-work-tree": "true", "remote get-url origin": server.URL + "/carol/other.git"},
	} {
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = &fakeGit{results: results}
		err := Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--remote"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%s: %v", name, err)
	}
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", "remote get-url origin": "https://alice:secret@github.com/alice/project.git"}}
	err := Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--remote"})
	require.EqualError(t, err, "Git remote origin points to https://github.com/alice/project.git; pass --remote-name")
	deps.Git = &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", "remote get-url origin": server.URL + "/bob/project.git", "remote get-url upstream": server.URL + "/carol/project.git"}}
	err = Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--remote"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "Git remote origin points to bob/project and cannot be renamed to upstream, which exists; pass --remote-name")
	deps.Git = &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true", "remote get-url upstream": server.URL + "/bob/project.git"}}
	err = Execute(context.Background(), deps, []string{"repo", "fork", "bob/project", "--remote", "--remote-name", "upstream"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "Git remote upstream points to bob/project; pass another --remote-name")
}

func TestRepoRenameUpdatesTheSavedDefaultRepository(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name":"renamed","full_name":"alice/renamed"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	saved := "config --local gt.repository " + server.URL + "/alice/renamed"
	runner := &fakeGit{results: map[string]string{
		"config --get gt.repository": server.URL + "/alice/project",
		saved:                        "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "rename", "renamed", "--yes"}))
	require.Contains(t, runner.calls, saved)
	require.Equal(t, "✓ Renamed repository alice/project to alice/renamed\n✓ Updated the default repository of this clone to alice/renamed\n", diagnostics.String())
}

func TestRepoTransfer(t *testing.T) {
	var requests []string
	status := http.StatusAccepted
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/teams":
			writeFirstPage(w, r, `[{"id":3,"name":"Developers"}]`)
		case "GET /api/v1/users/bob":
			_, _ = w.Write([]byte(`{"login":"bob"}`))
		case "POST /api/v1/repos/alice/project/transfer":
			var body struct {
				NewOwner string   `json:"new_owner"`
				TeamIDs  *[]int64 `json:"team_ids"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			switch body.NewOwner {
			case "nobody":
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"The new owner does not exist or cannot be found"}`))
				return
			case "acme":
				require.Equal(t, &[]int64{3}, body.TeamIDs)
			default:
				require.Nil(t, body.TeamIDs)
			}
			w.WriteHeader(status)
			fullName := body.NewOwner + "/project"
			if status == http.StatusCreated {
				fullName = "alice/project"
			}
			_, _ = fmt.Fprintf(w, `{"name":"project","full_name":%q}`, fullName)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "transfer", "acme", "--team", "developers", "-R", "alice/project", "--yes"}))
	require.Empty(t, out.String())
	require.Equal(t, "✓ Transferred repository alice/project to acme/project\n", diagnostics.String())
	require.Equal(t, "POST /api/v1/repos/alice/project/transfer", requests[len(requests)-1])
	// Gitea answers 201 Created for a transfer that waits for the new owner.
	status, requests = http.StatusCreated, nil
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "transfer", "bob", "-R", "alice/project", "--yes"}))
	require.Equal(t, "✓ Requested the transfer of alice/project to bob; it completes when bob accepts it\n", diagnostics.String())
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/transfer"}, requests)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "transfer", "bob", "-R", "alice/project", "--yes", "--json=full_name,transfer_pending"}))
	require.JSONEq(t, `{"full_name":"alice/project","transfer_pending":true}`, out.String())
	err := Execute(context.Background(), deps, []string{"repo", "transfer", "nobody", "-R", "alice/project", "--yes"})
	require.EqualError(t, err, `user or organization "nobody" not found`)
	for args, want := range map[string]string{
		"nobody --team developers": `user or organization "nobody" not found`,
		"bob --team developers":    `--team requires an organization as the new owner: organization "bob" not found`,
		"acme --team missing":      `team "missing" not found in organization acme`,
	} {
		err = Execute(context.Background(), deps, append(strings.Fields("repo transfer "+args), "-R", "alice/project", "--yes"))
		require.EqualError(t, err, want)
		require.Equal(t, clierr.Failure, clierr.ExitCode(err), args)
	}
	// Without --yes, nothing is transferred without a terminal to confirm on.
	deps, _, _ = serverDeps(t, server.URL)
	requests = nil
	require.Error(t, Execute(context.Background(), deps, []string{"repo", "transfer", "bob", "-R", "alice/project"}))
	require.Empty(t, requests)
	err = Execute(context.Background(), deps, []string{"repo", "transfer", "Alice", "-R", "alice/project", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "alice/project is already owned by alice")
	require.Empty(t, requests)
	requireUsageBeforeRequests(t, "new owner cannot be empty", "repo", "transfer", " ", "--yes")
}

func TestRepoArchiveTakesSeveralRepositories(t *testing.T) {
	var archived []string
	users := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			users++
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/")
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprintf(w, `{"full_name":%q,"archived":%t}`, name, name == "acme/old")
			return
		}
		require.Equal(t, http.MethodPatch, r.Method)
		var edit map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&edit))
		require.Equal(t, true, edit["archived"])
		archived = append(archived, name)
		_, _ = w.Write([]byte(`{"full_name":"` + name + `","archived":true}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "archive", "acme/one", "acme/old", "acme/two", "--yes"}))
	require.Equal(t, []string{"acme/one", "acme/two"}, archived)
	require.Equal(t, 1, users, "the GITEA_TOKEN account is checked once")
	require.Equal(t, "! acme/old is already archived\n", withoutANSI(diagnostics.String()))
	requireUsageBeforeRequests(t, "--yes", "repo", "archive", "acme/one", "acme/two")
}
