// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
	"github.com/stretchr/testify/require"
)

func TestBrowseURLs(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"release/1.0"}`)
		case "/api/v1/repos/alice/tracked":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/tracked","external_tracker":{"external_tracker_url":"https://tracker.test"}}`)
		case "/api/v1/repos/alice/project/issues/7":
			_, _ = fmt.Fprint(w, `{"number":7,"pull_request":{"merged":false}}`)
		case "/api/v1/repos/alice/project/issues/8":
			_, _ = fmt.Fprint(w, `{"number":8}`)
		case "/api/v1/repos/alice/project/issues/9":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	repo := server.URL + "/alice/project"
	for _, tc := range []struct {
		args     []string
		want     string
		requests []string
	}{
		{nil, repo, nil},
		// Gitea sends /issues/N of a pull request to an external tracker when the repository uses
		// one, and /pulls/N of an issue to /issues/N.
		{[]string{"7"}, repo + "/pulls/7", []string{"/api/v1/repos/alice/project/issues/7"}},
		{[]string{"8"}, repo + "/issues/8", []string{"/api/v1/repos/alice/project/issues/8"}},
		{[]string{"9"}, repo + "/pulls/9", []string{"/api/v1/repos/alice/project/issues/9"}},
		{[]string{"77507cd"}, repo + "/commit/77507cd", nil},
		{[]string{"--wiki"}, repo + "/wiki", nil},
		{[]string{"--branch", "feature/x"}, repo + "/src/branch/feature/x", nil},
		{[]string{"docs/a b.md:3-5", "-b", "feature/x"}, repo + "/src/branch/feature/x/docs/a%20b.md?display=source#L3-L5", nil},
		{[]string{"--commit=77507cd", "../docs/"}, repo + "/src/commit/77507cd/docs", nil},
		{[]string{"-c", "77507cd", "../docs/"}, repo + "/src/commit/77507cd/docs", nil},
		{[]string{"--commit", "77507cd"}, repo + "/src/commit/77507cd", nil},
		{[]string{"../docs/", "--commit", "77507cd"}, repo + "/src/commit/77507cd/docs", nil},
		{[]string{"cmd/main.go:7"}, repo + "/src/branch/release/1.0/cmd/main.go?display=source#L7", []string{"/api/v1/repos/alice/project"}},
	} {
		requests = nil
		deps, out, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, append([]string{"browse", "-R", "alice/project", "-n"}, tc.args...)), "%v", tc.args)
		require.Equal(t, tc.want+"\n", out.String(), "%v", tc.args)
		require.Equal(t, tc.requests, requests, "%v", tc.args)
	}
	// The clone in the working directory is not the repository of --repo.
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"browse", "-R", "alice/project", "--commit"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "--commit without a SHA requires a clone of alice/project")
	err = Execute(context.Background(), deps, []string{"browse", "-R", "alice/project", "-n", "#12"})
	require.Equal(t, clierr.Failure, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "issue or pull request #12 not found in alice/project")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"browse", "-R", "alice/missing", "-n", "12"}), "repository alice/missing not found")
	// An issue of an external tracker is not found through the API, but its link redirects there.
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"browse", "-R", "alice/tracked", "-n", "12"}))
	require.Equal(t, server.URL+"/alice/tracked/pulls/12\n", out.String())
	deps, out, _ = serverDeps(t, server.URL)
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"browse", "-R", "alice/project", "--releases"}))
	require.Equal(t, repo+"/releases", browser.resource)
	require.Empty(t, out.String())
}

func TestBrowseWithoutAnInstanceTakesTheCloneInstanceForARepository(t *testing.T) {
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": "https://gitea.example.test/alice/project.git"}}
	for _, args := range [][]string{{"--wiki"}, {"-R", "alice/project", "--wiki"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"browse", "-n"}, args...)), "%v", args)
		require.Equal(t, "https://gitea.example.test/alice/project/wiki\n", out.String(), "%v", args)
	}
}

func TestBrowseNumberWithoutAnAccount(t *testing.T) {
	var authorized []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized = append(authorized, r.Header.Get("Authorization"))
		if r.URL.Path == "/api/v1/repos/alice/project/issues/7" {
			_, _ = fmt.Fprint(w, `{"number":7,"pull_request":{"merged":false}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL})
	require.NoError(t, Execute(context.Background(), deps, []string{"browse", "-R", "alice/project", "-n", "7"}))
	require.Equal(t, server.URL+"/alice/project/pulls/7\n", out.String())
	require.Equal(t, []string{""}, authorized)
	// Without an account, a private repository looks missing, so its numbers keep their link.
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"browse", "-R", "alice/project", "-n", "8"}))
	require.Equal(t, server.URL+"/alice/project/pulls/8\n", out.String())
}

func TestBrowsePathsAndCommitsOfTheClone(t *testing.T) {
	gittest.Isolate(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet")
	run("remote", "add", "origin", "https://gitea.example.test/alice/project.git")
	run("commit", "--quiet", "--allow-empty", "--message=x")
	head := run("rev-parse", "HEAD")
	run("branch", "trunk")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "docs"), 0o755))
	deps, out, _ := testDeps(t)
	deps.Git = git.System{}
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": "https://gitea.example.test"})
	for args, want := range map[string]string{
		"--commit guide.md:2":               "/src/commit/" + head + "/docs/guide.md?display=source#L2",
		"--commit=HEAD /":                   "/src/commit/" + head,
		"--commit trunk":                    "/src/commit/" + head,
		"-c trunk guide.md":                 "/src/commit/" + head + "/docs/guide.md",
		"guide.md -c trunk":                 "/src/commit/" + head + "/docs/guide.md",
		"guide.md:2 --commit HEAD":          "/src/commit/" + head + "/docs/guide.md?display=source#L2",
		"-b main ../go.mod":                 "/src/branch/main/go.mod",
		"-R alice/project --commit":         "/src/commit/" + head,
		"-R alice/project -b main guide.md": "/src/branch/main/docs/guide.md",
	} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"browse", "-n", "--cwd", filepath.Join(dir, "docs")}, strings.Fields(args)...)), args)
		require.Equal(t, "https://gitea.example.test/alice/project"+want+"\n", out.String(), args)
	}
}
