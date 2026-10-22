// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
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
		case "/api/v1/repos/alice/project/issues/7":
			_, _ = fmt.Fprint(w, `{"number":7,"pull_request":{"merged":false}}`)
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
		// Gitea sends /issues/N of a pull request to an external tracker when the repository uses one.
		{[]string{"#12"}, repo + "/issues/12", []string{"/api/v1/repos/alice/project/issues/12"}},
		{[]string{"7"}, repo + "/pulls/7", []string{"/api/v1/repos/alice/project/issues/7"}},
		{[]string{"9"}, repo + "/issues/9", []string{"/api/v1/repos/alice/project/issues/9"}},
		{[]string{"77507cd"}, repo + "/commit/77507cd", nil},
		{[]string{"--wiki"}, repo + "/wiki", nil},
		{[]string{"--branch", "feature/x"}, repo + "/src/branch/feature/x", nil},
		{[]string{"docs/a b.md:3-5", "-b", "feature/x"}, repo + "/src/branch/feature/x/docs/a%20b.md?display=source#L3-L5", nil},
		{[]string{"--commit=77507cd", "../docs/"}, repo + "/src/commit/77507cd/docs", nil},
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
	deps, out, _ := serverDeps(t, server.URL)
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"browse", "-R", "alice/project", "--releases"}))
	require.Equal(t, repo+"/releases", browser.resource)
	require.Empty(t, out.String())
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
	require.NoError(t, os.Mkdir(filepath.Join(dir, "docs"), 0o755))
	deps, out, _ := testDeps(t)
	deps.Git = git.System{}
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": "https://gitea.example.test"})
	for args, want := range map[string]string{
		"--commit guide.md:2":       "/src/commit/" + head + "/docs/guide.md?display=source#L2",
		"--commit=HEAD /":           "/src/commit/" + head,
		"-b main ../go.mod":         "/src/branch/main/go.mod",
		"-R alice/project --commit": "/src/commit/" + head,
		"5":                         "/issues/5", // without an account
	} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"browse", "-n", "--cwd", filepath.Join(dir, "docs")}, strings.Fields(args)...)), args)
		require.Equal(t, "https://gitea.example.test/alice/project"+want+"\n", out.String(), args)
	}
}
