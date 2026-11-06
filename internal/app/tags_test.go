// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

// tagServer serves the tags and tag protection rules of alice/project and records its writes.
type tagServer struct {
	mu         sync.Mutex
	writes     []string
	ruleLists  int
	tagObjects int
	org        bool
}

func (s *tagServer) takeTagObjects() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	reads := s.tagObjects
	s.tagObjects = 0
	return reads
}

func (s *tagServer) takeRuleLists() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	lists := s.ruleLists
	s.ruleLists = 0
	return lists
}

func (s *tagServer) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	writes := s.writes
	s.writes = nil
	return writes
}

func (s *tagServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		var body string
		if r.Method != http.MethodGet {
			data, _ := io.ReadAll(r.Body)
			body = strings.TrimSpace(string(data))
			s.mu.Lock()
			s.writes = append(s.writes, strings.TrimSpace(r.Method+" "+path+" "+body))
			s.mu.Unlock()
		}
		switch r.Method + " " + path {
		case "GET ":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
		case "GET /tags":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"name":"v1.1.0","message":"The commit's message\n","id":"c111","commit":{"sha":"c111"}},{"name":"v1.2.0","message":"Version 1.2.0\n\nNotes","id":"t222","commit":{"sha":"c222"}}]`))
		case "GET /tags/v1.1.0":
			_, _ = w.Write([]byte(`{"name":"v1.1.0","message":"The commit's message\n","id":"c111","commit":{"sha":"c111"},"tarball_url":"http://x/v1.1.0.tar.gz","zipball_url":"http://x/v1.1.0.zip"}`))
		case "GET /tags/v1.2.0":
			_, _ = w.Write([]byte(`{"name":"v1.2.0","message":"Version 1.2.0\n\nNotes","id":"t222","commit":{"sha":"c222"},"tarball_url":"http://x/v1.2.0.tar.gz","zipball_url":"http://x/v1.2.0.zip"}`))
		case "GET /tags/gone":
			_, _ = w.Write([]byte(`{"name":"gone","message":"Gone","id":"t404","commit":{"sha":"c404"}}`))
		case "GET /git/tags/t404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "GET /tags/broken":
			_, _ = w.Write([]byte(`{"name":"broken","message":"Broken","id":"t400","commit":{"sha":"c400"}}`))
		case "GET /git/tags/t400":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"cannot read the tag"}`))
		case "GET /git/tags/t222":
			s.mu.Lock()
			s.tagObjects++
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"tag":"v1.2.0","sha":"t222","tagger":{"name":"Bob","email":"bob@example.com","date":"2026-10-11T18:00:00Z"},"verification":{"verified":true,"reason":"gpg.signature.good"}}`))
		case "GET /tags/rel/v1#2":
			_, _ = w.Write([]byte(`{"name":"rel/v1#2","message":"","id":"c444","commit":{"sha":"c444"}}`))
		case "GET /tags/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "POST /tags":
			if strings.Contains(body, "nowhere") {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"target not found: object does not exist"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"v2.0.0","message":"Two","id":"t333","commit":{"sha":"c333"}}`))
		case "DELETE /tags/v1.1.0", "DELETE /tags/v1.2.0":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /tags/released":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"a tag attached to a release cannot be deleted directly"}`))
		case "GET /tag_protections": // Gitea answers every rule at once, whatever the page
			s.mu.Lock()
			s.ruleLists++
			s.mu.Unlock()
			_, _ = w.Write([]byte(`[{"id":4,"name_pattern":"v*","whitelist_usernames":["alice"],"whitelist_teams":["release"],"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-02T10:00:00Z"}]`))
		case "POST /tag_protections":
			w.WriteHeader(http.StatusCreated)
			if strings.Contains(body, "unreadable-*") { // Gitea lists only those who can read the repository
				_, _ = w.Write([]byte(`{"id":6,"name_pattern":"unreadable-*","whitelist_usernames":[],"whitelist_teams":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":5,"name_pattern":"rel-*","whitelist_usernames":["bob"]}`))
		case "PATCH /tag_protections/4":
			if strings.Contains(body, `"whitelist_usernames":[]`) {
				_, _ = w.Write([]byte(`{"id":4,"name_pattern":"v*","whitelist_usernames":[],"whitelist_teams":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":4,"name_pattern":"v*","whitelist_usernames":["dave"],"whitelist_teams":["release"]}`))
		case "PATCH /tag_protections/5":
			_, _ = w.Write([]byte(`{"id":5,"name_pattern":"rel-*","whitelist_usernames":["bob"]}`))
		case "GET /api/v1/orgs/alice":
			if s.org {
				_, _ = w.Write([]byte(`{"id":9,"name":"alice"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "DELETE /tag_protections/4":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestTagCommands(t *testing.T) {
	state := &tagServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"tag"}, args...))
		return out.String(), err
	}

	out, err := run("list")
	require.NoError(t, err)
	require.Equal(t, "v1.1.0\tc111\tlightweight\t\nv1.2.0\tc222\tannotated\tVersion 1.2.0\n", out)
	out, err = run("list", "--json", "name,annotated,commit,id")
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"v1.1.0","annotated":false,"commit":"c111","id":"c111"},{"name":"v1.2.0","annotated":true,"commit":"c222","id":"t222"}]`, out)

	out, err = run("view", "v1.2.0", "--json", "name,tagger,tagged_at,verified,message")
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"v1.2.0","tagger":"Bob","tagged_at":"2026-10-11T18:00:00Z","verified":true,"message":"Version 1.2.0\n\nNotes"}`, out)
	require.Equal(t, 1, state.takeTagObjects())
	out, err = run("view", "v1.2.0", "--json", "name,commit")
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"v1.2.0","commit":"c222"}`, out)
	require.Zero(t, state.takeTagObjects(), "fields of the tag alone need no second request")
	deps, terminalOut, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"tag", "view", "v1.2.0"}))
	require.Contains(t, terminalOut.String(), "commit verified", "the signature that Gitea checks is the commit's")
	require.Equal(t, 1, state.takeTagObjects())
	out, err = run("view", "gone", "--json", "name,tagger")
	require.NoError(t, err, "a tag object that is not found only leaves out who made the tag")
	require.JSONEq(t, `{"name":"gone","tagger":""}`, out)
	_, err = run("view", "broken")
	require.ErrorContains(t, err, "cannot read the tag", "any other failure is reported")
	out, err = run("view", "v1.1.0", "--json", "message")
	require.NoError(t, err)
	require.JSONEq(t, `{"message":""}`, out, "a lightweight tag has no message, whatever Gitea answers")
	out, err = run("view", "v1.1.0")
	require.NoError(t, err)
	require.NotContains(t, out, "The commit's message")
	require.Contains(t, out, "commit:\tc111")
	require.NotContains(t, out, "tagger", "a lightweight tag has no tagger")
	_, err = run("view", "missing")
	require.ErrorContains(t, err, `tag "missing"`)
	out, err = run("view", "rel/v1#2")
	require.NoError(t, err)
	require.Contains(t, out, server.URL+"/alice/project/src/tag/rel/v1%232\n", "a # in the name would start a fragment")
	deps, _, _ = testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"tag", "view", "rel/v1#2", "--web"}))
	require.Equal(t, server.URL+"/alice/project/src/tag/rel/v1%232", browser.resource)
	deps, _, _ = testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Browser = &recordingBrowser{}
	require.NoError(t, Execute(context.Background(), deps, []string{"tag", "view", "v1.2.0", "--web"}))
	require.Zero(t, state.takeTagObjects(), "--web needs no second request")
	_, err = run("view", "bad..name")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))

	state.take()
	out, err = run("create", "v2.0.0", "--target", "release/2", "--message", "Two", "--json", "name,annotated")
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"v2.0.0","annotated":true}`, out)
	require.Equal(t, []string{`POST /tags {"tag_name":"v2.0.0","message":"Two","target":"release/2"}`}, state.take())

	_, err = run("create", "v2.0.0", "--target", "nowhere")
	require.ErrorContains(t, err, `target "nowhere" not found in alice/project`)
	state.take()

	_, err = run("delete", "v1.1.0", "v1.2.0", "--yes")
	require.NoError(t, err)
	require.Equal(t, []string{"DELETE /tags/v1.1.0", "DELETE /tags/v1.2.0"}, state.take())
	_, err = run("delete", "v1.1.0")
	require.Error(t, err, "without --yes and a terminal there is nobody to ask")
	require.Empty(t, state.take())
	_, err = run("delete", "released", "--yes")
	require.ErrorContains(t, err, "delete the release too with gt release delete released --delete-tag", "a tag of a release says how to delete both")
}

func TestTagCreateResolvesTheTargetInTheClone(t *testing.T) {
	const local = "1111111111111111111111111111111111111111"
	var posted string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		posted = string(body)
		if strings.Contains(posted, `"target":"v2"`) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"target not found: object does not exist"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"v1","id":"` + local + `","commit":{"sha":"` + local + `"}}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git",
		"rev-parse --verify --quiet --end-of-options HEAD^{commit}": local,
		"rev-parse --verify --quiet refs/tags/v2":                   "abc",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"tag", "create", "v1", "--target", "HEAD"}))
	require.Contains(t, posted, `"target":"`+local+`"`, "Gitea would read HEAD as its default branch")

	err := Execute(context.Background(), deps, []string{"tag", "create", "v3", "--target", "v2"})
	require.EqualError(t, Explain(context.Background(), deps, err), `target "v2" not found in alice/project; push it first: git push origin v2`)
}

func TestTagProtection(t *testing.T) {
	state := &tagServer{org: true}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"tag"}, args...))
		return out.String(), err
	}
	diagnose := func(args ...string) (string, error) {
		deps, _, diagnostics := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		deps.Streams.OutTTY = true // gt reports what it did only to people
		err := Execute(context.Background(), deps, append([]string{"tag"}, args...))
		return diagnostics.String(), err
	}

	out, err := run("protection")
	require.NoError(t, err)
	require.Contains(t, out, "List the tag protection rules", "the group alone shows its help")
	_, err = run("protections")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "the plural has no alias")
	out, err = run("protection", "list")
	require.NoError(t, err)
	require.Equal(t, "v*\talice\trelease\n", out)
	out, err = run("protection", "list", "--json", "name_pattern,users,teams")
	require.NoError(t, err)
	require.JSONEq(t, `[{"name_pattern":"v*","users":["alice"],"teams":["release"]}]`, out)

	_, err = run("protect", "rel-*", "--allow-team", "release", "--allow-user", "bob,carol")
	require.NoError(t, err)
	require.Equal(t, []string{`POST /tag_protections {"name_pattern":"rel-*","whitelist_usernames":["bob","carol"],"whitelist_teams":["release"]}`}, state.take())

	_, err = run("protect", "v*", "--allow-user", "dave")
	require.NoError(t, err)
	writes := state.take()
	require.Len(t, writes, 1)
	require.Contains(t, writes[0], `PATCH /tag_protections/4 `)
	require.Contains(t, writes[0], `"whitelist_usernames":["dave"]`)
	require.NotContains(t, writes[0], `"whitelist_teams":["release"]`, "only the lists that are named change")

	_, err = run("protect", "v*", "--allow-user=")
	require.NoError(t, err)
	require.Equal(t, []string{`PATCH /tag_protections/4 {"name_pattern":null,"whitelist_usernames":[],"whitelist_teams":null}`}, state.take(), "an empty flag clears its list, an unset one leaves it")

	state.takeRuleLists()
	_, err = run("protect", "rel-*", "rel-*", "--allow-user", "bob")
	require.NoError(t, err)
	require.Equal(t, []string{
		`POST /tag_protections {"name_pattern":"rel-*","whitelist_usernames":["bob"],"whitelist_teams":[]}`,
		`PATCH /tag_protections/5 {"name_pattern":null,"whitelist_usernames":["bob"],"whitelist_teams":null}`,
	}, state.take(), "a repeated pattern changes the rule that the first one added")
	require.Equal(t, 1, state.takeRuleLists())

	stderr, err := diagnose("protect", "v*", "--allow-user", "dave")
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(stderr, "\n"), "an update says so once: %s", stderr)
	require.Contains(t, stderr, "Updated the rule for tags v*")
	state.take()

	stderr, err = diagnose("protect", "v*", "--allow-user=")
	require.NoError(t, err)
	require.Contains(t, stderr, "nobody can create matching tags", "a rule left empty locks everyone out")
	state.take()
	stderr, err = diagnose("protect", "unreadable-*", "--allow-user", "stranger")
	require.NoError(t, err)
	require.Contains(t, stderr, "nobody can create matching tags", "Gitea may list nobody of those a new rule names")
	state.take()
	stderr, err = diagnose("protect", "v*", "--allow-user=", "--dry-run")
	require.NoError(t, err)
	require.NotContains(t, stderr, "nobody can create", "a dry run has no rule to judge")
	require.Empty(t, state.take())

	state.org = false
	_, err = run("protect", "rel-*", "--allow-team", "release")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--allow-team requires a repository owned by an organization", "Gitea would drop the team")
	require.Empty(t, state.take())
	_, err = run("protect", "v*", "--allow-team=")
	require.NoError(t, err, "clearing the teams needs no organization")
	state.take()
	state.org = true

	state.takeRuleLists()
	_, err = run("unprotect", "v*", "v*", "--yes")
	require.ErrorContains(t, err, `tag protection "v*"`, "a rule that is gone cannot be removed again")
	require.Equal(t, []string{"DELETE /tag_protections/4"}, state.take())
	require.Equal(t, 1, state.takeRuleLists())

	_, err = run("unprotect", "v*", "--yes")
	require.NoError(t, err)
	require.Equal(t, []string{"DELETE /tag_protections/4"}, state.take())
	_, err = run("unprotect", "nothing-*", "--yes")
	require.ErrorContains(t, err, "nothing-*")
	require.Empty(t, state.take())
	state.take()
	_, err = run("protect", "new-*", "--allow-user=")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "Gitea refuses a new rule that nobody may use, so gt does not send it")
	require.ErrorContains(t, err, "name them with --allow-user or --allow-team")
	require.Empty(t, state.take())
}
