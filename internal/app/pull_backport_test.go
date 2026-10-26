// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
	"github.com/stretchr/testify/require"
)

// backportServer is a Gitea stand-in that serves alice/project over Git's smart HTTP, so the clone's
// remote names the instance, and answers the API for pull request #7, which merged two commits
// into main after release/v1 was cut, and for the pull requests gt opens.
type backportServer struct {
	t           *testing.T
	server      *httptest.Server
	root, work  string
	pusher      string
	releaseTip  string
	mu          sync.Mutex
	created     []map[string]any
	bodies      [][]byte
	failCreates int
	pullCommits []string
}

func newBackportServer(t *testing.T) *backportServer {
	t.Helper()
	gittest.Isolate(t)
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	s := &backportServer{t: t, root: t.TempDir()}
	bare := filepath.Join(s.root, "alice", "project.git")
	s.pusher, s.work = filepath.Join(s.root, "pusher"), filepath.Join(s.root, "work")
	s.git(s.root, "init", "--quiet", "--bare", "-b", "main", bare)
	s.git(bare, "config", "http.receivepack", "true")
	s.git(s.root, "clone", "--quiet", bare, s.pusher)
	s.commit("base.txt", "base\n", "base")
	s.git(s.pusher, "push", "--quiet", "origin", "HEAD:main", "HEAD:release/v1", "HEAD:release/v2")
	s.releaseTip = s.git(s.pusher, "rev-parse", "HEAD")
	s.git(s.pusher, "switch", "--quiet", "-c", "feature")
	s.pullCommits = []string{s.commit("one.txt", "one\n", "add one"), s.commit("two.txt", "two\n", "add two")}
	s.git(s.pusher, "push", "--quiet", "origin", "HEAD:refs/pull/7/head")
	s.git(s.pusher, "switch", "--quiet", "main")
	s.git(s.pusher, "merge", "--quiet", "--no-ff", "-m", "Merge pull request #7", "feature")
	merge := s.git(s.pusher, "rev-parse", "HEAD")
	s.git(s.pusher, "push", "--quiet", "origin", "HEAD:main")

	backend := &cgi.Handler{Path: gitPath, Args: []string{"http-backend"}, Env: []string{
		"GIT_PROJECT_ROOT=" + s.root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_GLOBAL=" + os.Getenv("GIT_CONFIG_GLOBAL"), "GIT_CONFIG_NOSYSTEM=1",
	}}
	s.server = newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/alice/project.git/"):
			backend.ServeHTTP(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/7":
			fmt.Fprintf(w, `{"number":7,"title":"Add one and two","body":"Adds one and two.","user":{"login":"bob"},"state":"closed",
				"merged":true,"merge_commit_sha":%q,"merge_base":%q,"base":{"ref":"main"},"head":{"ref":"feature","sha":%q},
				"labels":[{"id":3,"name":"kind/bug"},{"id":4,"name":"old","is_archived":true}],"milestone":{"id":9,"title":"1.3.0"}}`,
				merge, s.releaseTip, s.pullCommits[1])
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main","has_pull_requests":true,"permissions":{"push":true}}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/branches/"):
			name := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/branches/")
			if _, err := (git.System{}).Output(context.Background(), bare, false, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"name":%q}`, name)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/milestones":
			writeFirstPage(w, r, `[{"id":9,"title":"1.3.0"}]`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/milestones/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls" && s.failCreates > 0:
			s.failCreates--
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(body, &payload))
			s.mu.Lock()
			s.created, s.bodies = append(s.created, payload), append(s.bodies, body)
			number := 11 + len(s.created)
			s.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"number":%d,"title":%q,"html_url":"%s/alice/project/pulls/%d","base":{"ref":%q},"head":{"ref":%q}}`,
				number, payload["title"], s.server.URL, number, payload["base"], payload["head"])
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.server.Close)
	s.git(s.root, "clone", "--quiet", s.server.URL+"/alice/project.git", s.work)
	s.git(s.work, "switch", "--quiet", "-c", "work")
	return s
}

func (s *backportServer) git(dir string, args ...string) string {
	s.t.Helper()
	output, err := (git.System{}).Output(context.Background(), dir, false, args...)
	require.NoError(s.t, err, args)
	return output
}

func (s *backportServer) commit(name, content, message string) string {
	s.t.Helper()
	require.NoError(s.t, os.WriteFile(filepath.Join(s.pusher, name), []byte(content), 0o600))
	s.git(s.pusher, "add", name)
	s.git(s.pusher, "commit", "--quiet", "-m", message)
	return s.git(s.pusher, "rev-parse", "HEAD")
}

func (s *backportServer) run(args ...string) (string, string, error) {
	s.t.Helper()
	deps, out, diagnostics := serverDeps(s.t, s.server.URL)
	deps.Git = git.System{}
	err := Execute(context.Background(), deps, append(args, "--repo", "alice/project", "--cwd", s.work))
	return out.String(), diagnostics.String(), err
}

func (s *backportServer) branchCommits(branch string) []string {
	s.t.Helper()
	bare := filepath.Join(s.root, "alice", "project.git")
	return strings.Split(s.git(bare, "rev-list", "--reverse", s.releaseTip+".."+branch), "\n")
}

func TestPullBackportOpensAPullRequestPerTarget(t *testing.T) {
	s := newBackportServer(t)
	out, _, err := s.run("pr", "backport", "7", "--to", "release/v1", "--to", "release/v2", "--yes", "--json")
	require.NoError(t, err)

	require.Len(t, s.created, 2)
	for i, target := range []string{"release/v1", "release/v2"} {
		branch := "backport-7-" + strings.ReplaceAll(target, "/", "-")
		requirePayload(t, fmt.Sprintf(`{"title":"Add one and two (#7)","body":"Backport #7 by @bob\n\nAdds one and two.","base":%q,"head":%q,
			"labels":[3],"allow_maintainer_edit":true}`, target, branch), bytes.NewReader(s.bodies[i]))
		commits := s.branchCommits(branch)
		require.Len(t, commits, 2, "pushed to the base repository")
		for j, commit := range commits {
			require.Contains(t, s.git(s.work, "log", "-1", "--format=%B", commit), "(cherry picked from commit "+s.pullCommits[j]+")")
		}
		require.Equal(t, fmt.Sprint(12+i), s.git(s.work, "config", "--get", "branch."+branch+".gitea-cli-pr"))
	}
	require.Equal(t, "work", s.git(s.work, "branch", "--show-current"))

	var outputs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &outputs))
	require.Len(t, outputs, 2)
	require.Equal(t, "release/v1", outputs[0]["target"])
	require.Equal(t, []any{s.pullCommits[0], s.pullCommits[1]}, outputs[0]["picked"])
	require.Equal(t, float64(12), outputs[0]["pull"].(map[string]any)["number"])
}

func TestPullBackportConflicts(t *testing.T) {
	conflicting := func(t *testing.T) *backportServer {
		s := newBackportServer(t)
		s.git(s.pusher, "switch", "--quiet", "-c", "v1", "origin/release/v1")
		s.commit("two.txt", "different\n", "conflicting two")
		s.git(s.pusher, "push", "--quiet", "origin", "HEAD:release/v1")
		return s
	}
	t.Run("undone while the other targets go ahead", func(t *testing.T) {
		s := conflicting(t)
		out, _, err := s.run("pr", "backport", "7", "--to", "release/v1", "--to", "release/v2", "--yes")
		require.ErrorContains(t, err, "cherry-picking to release/v1 conflicts in two.txt; nothing was changed. Run again with --keep")
		require.Equal(t, clierr.Failure, clierr.ExitCode(err))
		require.Len(t, s.created, 1)
		require.Equal(t, "release/v2", s.created[0]["base"])
		require.Equal(t, s.server.URL+"/alice/project/pulls/12\n", out)
		require.Equal(t, "work", s.git(s.work, "branch", "--show-current"))
		require.Empty(t, s.git(s.work, "branch", "--list", "backport-7-release-v1"))
	})
	t.Run("a target that already has it does not stop the others", func(t *testing.T) {
		s := newBackportServer(t)
		s.git(s.pusher, "push", "--quiet", "origin", "main:release/v1")
		_, _, err := s.run("pr", "backport", "7", "--to", "release/v1", "--to", "release/v2", "--yes")
		require.EqualError(t, err, "pull request #7 is already on release/v1")
		require.Len(t, s.created, 1)
		require.Equal(t, "release/v2", s.created[0]["base"])
	})
	t.Run("kept, resolved and continued", func(t *testing.T) {
		s := conflicting(t)
		_, _, err := s.run("pr", "backport", "7", "--to", "release/v1", "--keep", "--yes")
		require.ErrorContains(t, err, "resolve the conflicts on branch backport-7-release-v1")
		require.Empty(t, s.created)
		require.Equal(t, "backport-7-release-v1", s.git(s.work, "branch", "--show-current"))

		_, _, err = s.run("pr", "backport", "--continue", "--yes")
		require.ErrorContains(t, err, "a cherry-pick is in progress")

		require.NoError(t, os.WriteFile(filepath.Join(s.work, "two.txt"), []byte("two\n"), 0o600))
		s.git(s.work, "add", "two.txt")
		s.git(s.work, "-c", "core.editor=true", "cherry-pick", "--continue")
		out, _, err := s.run("pr", "backport", "--continue", "--yes")
		require.NoError(t, err)
		require.Len(t, s.created, 1)
		require.Equal(t, "release/v1", s.created[0]["base"])
		require.Equal(t, "backport-7-release-v1", s.created[0]["head"])
		require.Equal(t, s.server.URL+"/alice/project/pulls/12\n", out)
		require.Equal(t, "work", s.git(s.work, "branch", "--show-current"))
	})
}

// What would fail only when pushing or opening the pull request fails before the branch exists,
// so that rerunning as the error says works.
func TestPullBackportChecksBeforeCherryPicking(t *testing.T) {
	s := newBackportServer(t)
	s.git(s.pusher, "push", "--quiet", "origin", "HEAD~1:refs/heads/backport-7-release-v2")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--to", "release/v1"}, "pr backport requires --yes without a terminal"},
		{[]string{"--to", "release/v1", "--milestone", "nosuch", "--yes"}, `milestone "nosuch" not found`},
		{[]string{"--to", "release/v2", "--yes"}, "branch backport-7-release-v2 already exists in alice/project; choose another with --branch"},
	} {
		_, _, err := s.run(append([]string{"pr", "backport", "7"}, tc.args...)...)
		require.ErrorContains(t, err, tc.want)
		require.Empty(t, s.git(s.work, "branch", "--list", "backport-*"), tc.args)
	}
	require.Empty(t, s.created)
	_, _, err := s.run("pr", "backport", "7", "--to", "release/v1", "--milestone", "1.3.0", "--yes")
	require.NoError(t, err)
	require.Equal(t, float64(9), s.created[0]["milestone"])
}

func TestPullBackportRefusesAnOpenPullRequest(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/7", r.URL.Path)
		fmt.Fprint(w, `{"number":7,"state":"open","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123"}}`)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{
		"rev-parse --is-inside-work-tree": "true", "remote": "origin", "remote get-url origin": server.URL + "/alice/project.git",
	}}
	err := Execute(context.Background(), deps, []string{"pr", "backport", "7", "--to", "release/v1", "--repo", "alice/project", "--yes"})
	require.ErrorContains(t, err, "pull request #7 is not merged")
}

func TestPullBackportRetriesAFailedPullRequestWithContinue(t *testing.T) {
	s := newBackportServer(t)
	s.failCreates = 1
	_, _, err := s.run("pr", "backport", "7", "--to", "release/v1", "--yes")
	require.ErrorContains(t, err, "pushed branch backport-7-release-v1, but could not open its pull request into release/v1")
	require.ErrorContains(t, err, "to retry, switch to it and run gt pr backport --continue")
	require.Empty(t, s.created)

	s.git(s.work, "switch", "--quiet", "backport-7-release-v1")
	out, _, err := s.run("pr", "backport", "--continue", "--yes")
	require.NoError(t, err)
	require.Len(t, s.created, 1)
	require.Equal(t, s.server.URL+"/alice/project/pulls/12\n", out)
	require.Equal(t, "work", s.git(s.work, "branch", "--show-current"))
}
