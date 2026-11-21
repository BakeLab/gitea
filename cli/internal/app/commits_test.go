// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

const sampleCommitSHA = "3f2a9c1e5d7b8a4c6e0f1a2b3c4d5e6f70819293"

// commitRefs are the refs that commitServer resolves, with the SHA of their commit.
var commitRefs = map[string]string{
	"3f2a9c1": sampleCommitSHA, "main": sampleCommitSHA, "v1.2.0": sampleCommitSHA, "release/1.2": sampleCommitSHA,
	"failing": strings.Repeat("f", 40), "running": strings.Repeat("a", 40), "none": strings.Repeat("0", 40), "paged": strings.Repeat("b", 40),
}

const commitJSON = `{"sha":"` + sampleCommitSHA + `","url":"http://x/api","html_url":"http://x/commit/3f2a9c1",
"commit":{"message":"Fix login\n\nThe form kept the old token.","author":{"name":"Alice A","email":"alice@example.com","date":"2026-10-11T17:00:00Z"},
"committer":{"name":"Bob B","email":"bob@example.com","date":"2026-10-11T18:00:00Z"},"verification":{"verified":true,"reason":"gpg.signature.good"}},
"author":{"login":"alice"},"committer":{"login":"bob"},"parents":[{"sha":"aaaaaaa1111111111111111111111111111111111"}],
"files":[{"filename":"internal/app/login.go"},{"filename":"internal/app/login_test.go"}],"stats":{"total":5,"additions":4,"deletions":1}}`

// commitServer serves the commit endpoints of alice/project and records the requests it gets.
type commitServer struct {
	mu       sync.Mutex
	requests []string
}

func (s *commitServer) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := s.requests
	s.requests = nil
	return requests
}

func (s *commitServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		s.mu.Lock()
		if path != "" {
			s.requests = append(s.requests, path+"?"+r.URL.RawQuery)
		}
		s.mu.Unlock()
		switch {
		case path == "":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
		case path == "/commits":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[` + commitJSON + `]`))
		case path == "/git/commits/"+sampleCommitSHA+".diff":
			_, _ = w.Write([]byte(sampleDiff))
		case path == "/git/commits/"+sampleCommitSHA+".patch":
			_, _ = w.Write([]byte("From 3f2a9c1 Mon Sep 17 00:00:00 2001\nSubject: [PATCH] Fix login\n\n---\n" + sampleDiff))
		case commitRefs[strings.TrimPrefix(path, "/git/commits/")] != "":
			_, _ = w.Write([]byte(strings.Replace(commitJSON, sampleCommitSHA, commitRefs[strings.TrimPrefix(path, "/git/commits/")], 1)))
		case path == "/git/commits/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case path == "/compare/main...feature":
			_, _ = w.Write([]byte(`{"total_commits":1,"commits":[` + commitJSON + `]}`))
		case strings.HasPrefix(path, "/statuses/"):
			statuses := `[]`
			switch strings.TrimPrefix(path, "/statuses/") + " page " + r.URL.Query().Get("page") {
			case sampleCommitSHA + " page 1":
				statuses = `[{"id":1,"context":"ci/build","status":"success","description":"Built","target_url":"/alice/project/actions/runs/1"}]`
			case commitRefs["failing"] + " page 1":
				statuses = `[{"id":1,"context":"ci/test","status":"failure","description":"2 failed"},{"id":2,"context":"ci/build","status":"success"}]`
			case commitRefs["running"] + " page 1":
				statuses = `[{"id":1,"context":"ci/build","status":"pending"}]`
			case commitRefs["paged"] + " page 1":
				page := make([]string, 100)
				for i := range page {
					page[i] = fmt.Sprintf(`{"id":%d,"context":"ci/%d","status":"success"}`, i+1, i)
				}
				statuses = "[" + strings.Join(page, ",") + "]"
			case commitRefs["paged"] + " page 2":
				statuses = `[{"id":101,"context":"ci/last","status":"failure"}]`
			}
			_, _ = w.Write([]byte(statuses))
		case path == "/commits/3f2a9c1/pull":
			_, _ = w.Write([]byte(dependencyPullJSON))
		default:
			t.Errorf("unexpected request %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestCommitReads(t *testing.T) {
	state := &commitServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"commit"}, args...))
		return out.String(), err
	}

	out, err := run("list", "release", "--path", "internal/", "--not", "v1", "--limit", "5")
	require.NoError(t, err)
	require.Equal(t, "3f2a9c1e5d7b8a4c6e0f1a2b3c4d5e6f70819293\tFix login\talice\t2026-10-11T18:00:00Z\n", out, "piped output keeps the full SHA")
	require.Equal(t, []string{"/commits?files=false&limit=5&not=v1&page=1&path=internal%2F&sha=release&stat=false&verification=false"}, sortedQueries(state.take()))

	out, err = run("list", "--json", "sha,additions,verified")
	require.NoError(t, err)
	require.JSONEq(t, `[{"sha":"3f2a9c1e5d7b8a4c6e0f1a2b3c4d5e6f70819293","additions":4,"verified":true}]`, out)
	requests := state.take()
	require.Contains(t, requests[0], "stat=true")
	require.Contains(t, requests[0], "verification=true")
	require.Contains(t, requests[0], "files=false", "files are read only when the output includes them")

	out, err = run("view", "3f2a9c1", "--json", "sha,message,author_login,committer_login,parents,files,additions,deletions,verified,verification_reason,url")
	require.NoError(t, err)
	require.JSONEq(t, `{"sha":"3f2a9c1e5d7b8a4c6e0f1a2b3c4d5e6f70819293","message":"Fix login\n\nThe form kept the old token.","author_login":"alice","committer_login":"bob","parents":["aaaaaaa1111111111111111111111111111111111"],"files":["internal/app/login.go","internal/app/login_test.go"],"additions":4,"deletions":1,"verified":true,"verification_reason":"gpg.signature.good","url":"http://x/commit/3f2a9c1"}`, out)
	out, err = run("view", "3f2a9c1")
	require.NoError(t, err)
	for _, want := range []string{"title:\tFix login", "author:\talice <alice@example.com>", "committer:\tbob <bob@example.com>", "parents:\taaaaaaa", "changes:\t2 files, +4 -1", "files:\tinternal/app/login.go, internal/app/login_test.go", "The form kept the old token."} {
		require.Contains(t, out, want)
	}
	_, err = run("view", "missing")
	require.ErrorContains(t, err, `commit "missing"`)

	out, err = run("compare", "main...feature", "--json", "base,head,total_commits")
	require.NoError(t, err)
	require.JSONEq(t, `{"base":"main","head":"feature","total_commits":1}`, out)
	out, err = run("compare", "main", "feature")
	require.NoError(t, err)
	require.Contains(t, out, "Fix login")
	out, err = run("pr", "3f2a9c1", "--json", "number,title")
	require.NoError(t, err)
	require.JSONEq(t, `{"number":7,"title":"Fix"}`, out)
}

func TestCommitDiffWebOpensTheCommitOfARef(t *testing.T) {
	server := newAuthenticatedServer((&commitServer{}).handler(t))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"commit", "diff", "main", "--web"}))
	require.Equal(t, "http://x/commit/3f2a9c1", browser.resource, "the web route takes only a SHA")
}

func TestCommitCompareCountsCommitsOnATerminal(t *testing.T) {
	server := newAuthenticatedServer((&commitServer{}).handler(t))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"commit", "compare", "main...feature"}))
	require.Equal(t, "Showing 1 of 1 commits that feature has and main has not\n", firstLine(out.String()))
}

func TestCommitPerson(t *testing.T) {
	require.Equal(t, "alice <alice@example.com>", commitPerson("alice", "alice@example.com"))
	require.Equal(t, "alice", commitPerson("alice", ""))
	require.Equal(t, "<alice@example.com>", commitPerson("", "alice@example.com"))
}

func TestCommitStatusExitCodes(t *testing.T) {
	server := newAuthenticatedServer((&commitServer{}).handler(t))
	defer server.Close()
	run := func(ref string, extra ...string) (string, error) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"commit", "status", ref}, extra...))
		return out.String(), err
	}

	out, err := run("main")
	require.NoError(t, err)
	require.Equal(t, "ci/build\tsuccess\tBuilt\t"+server.URL+"/alice/project/actions/runs/1\n", out, "like gt pr checks, with an absolute link")

	out, err = run("failing")
	require.Equal(t, 1, clierr.ExitCode(err), "a failed status fails the command")
	require.Contains(t, out, "ci/test\tfailure\t2 failed")

	_, err = run("running")
	require.Equal(t, clierr.Pending, clierr.ExitCode(err))

	out, err = run("none")
	require.NoError(t, err, "no statuses is not a failure")
	require.Empty(t, out)

	_, err = run("paged")
	require.Equal(t, 1, clierr.ExitCode(err), "a failure past the first page of statuses still fails")

	out, err = run("failing", "--json", "name,state")
	require.Equal(t, 1, clierr.ExitCode(err), "--json keeps the exit status")
	require.JSONEq(t, `[{"name":"ci/build","state":"success"},{"name":"ci/test","state":"failure"}]`, out)
}

func TestCommitDiffNarrowsToFiles(t *testing.T) {
	state := &commitServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"commit", "diff", "3f2a9c1"}, args...))
		return out.String(), err
	}

	out, err := run()
	require.NoError(t, err)
	require.Equal(t, sampleDiff, out, "piped, the diff is written unchanged")

	out, err = run("--path", "internal/", "--exclude", "*_test.go")
	require.NoError(t, err)
	require.Contains(t, out, "diff --git a/internal/app/main.go")
	require.NotContains(t, out, "main_test.go")
	require.NotContains(t, out, "logo.png")
	require.True(t, strings.HasPrefix(out, "diff --git "), "what is left is still a diff that git apply takes")

	out, err = run("--name-only", "-e", "docs/")
	require.NoError(t, err)
	require.Equal(t, "internal/app/main.go\ninternal/app/main_test.go\nlogo.png\nold.txt\n", out)

	out, err = run("--name-status")
	require.NoError(t, err)
	require.Equal(t, "M\tinternal/app/main.go\nA\tinternal/app/main_test.go\nR\tdocs/old name.md\tdocs/new name.md\nM\tlogo.png\nD\told.txt\n", out)

	out, err = run("--stat", "--path", "*.go")
	require.NoError(t, err)
	require.Contains(t, out, "2 files changed, 4 insertions(+), 1 deletion(-)")

	out, err = run("--patch")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, "From 3f2a9c1"))

	for _, ref := range []string{"v1.2.0", "release/1.2"} {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, []string{"commit", "diff", ref}), "a ref with a dot or a slash is resolved to its SHA")
		require.Equal(t, sampleDiff, out.String())
	}

	for _, args := range [][]string{{"--name-only", "--stat"}, {"--patch", "--stat"}, {"--patch", "--path", "x"}, {"--path", ""}} {
		_, err := run(args...)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", args)
	}
	_, err = run("--path", "nothing")
	require.NoError(t, err, "a filter that leaves nothing is not an error")
}

func TestPullDiffNarrowsToFilesToo(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project") {
		case "":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
		case "/pulls/7":
			_, _ = w.Write([]byte(dependencyPullJSON))
		case "/pulls/7.diff":
			_, _ = w.Write([]byte(sampleDiff))
		default:
			t.Errorf("unexpected request %s", r.URL.RequestURI())
		}
	}))
	defer server.Close()
	run := func(args ...string) string {
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "diff", "7"}, args...)))
		return out.String()
	}
	require.Equal(t, sampleDiff, run())
	require.Equal(t, "internal/app/main.go\n", run("--path", "internal/", "--exclude", "*_test.go", "--name-only"))
	require.Contains(t, run("--stat", "-e", "docs/"), "4 files changed")
}
