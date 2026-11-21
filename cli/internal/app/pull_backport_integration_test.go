// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"

	"github.com/stretchr/testify/require"
)

// TestGiteaPullBackport merges a two-commit pull request in each of Gitea's merge styles and
// backports each to a release branch cut before them, checking that the backport carries exactly
// the commits each style left on main.
func TestGiteaPullBackport(t *testing.T) {
	gitea, deps, out, diagnostics := giteaDeps(t)
	deps.Settings = settings.New(t.TempDir())
	deps.Credentials.Dir = deps.Settings.Dir
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-backport-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	user, _, _ := strings.Cut(repo.FullName, "/")
	require.NoError(t, testManager(t, deps).Register(gitea.url, user, "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: gitea.token}))
	// Git pushes with gt as its credential helper, so the helper must be a real gt binary.
	binary := filepath.Join(t.TempDir(), "gt")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "./cmd/gt")
	build.Dir = filepath.Join("..", "..")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", output)
	deps.Executable = func() (string, error) { return binary, nil }
	deps.Git = git.System{Env: append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"GITEA_CLI_CONFIG_DIR="+deps.Settings.Dir, "GITEA_TOKEN=", "GITEA_INSTANCE_URL=", "GITEA_CLI_HOST=", "GITEA_CLI_USER=", "GITEA_CLI_REPO=",
		"GIT_AUTHOR_NAME=gt", "GIT_AUTHOR_EMAIL=gt@example.invalid", "GIT_COMMITTER_NAME=gt", "GIT_COMMITTER_EMAIL=gt@example.invalid")}

	execute("branch", "create", "release/v1", "-R", repo.FullName)
	execute("label", "create", "kind/bug", "--color", "ff0000", "-R", repo.FullName)
	commitFile := func(branch, name string) {
		payload, err := json.Marshal(map[string]string{"branch": branch, "message": "Add " + name, "content": base64.StdEncoding.EncodeToString([]byte(name + "\n"))})
		require.NoError(t, err)
		deps.Streams.In = strings.NewReader(string(payload))
		execute("api", "repos/"+repo.FullName+"/contents/"+name, "--input", "-")
	}
	open := func(style string) string {
		branch := "feature-" + style
		execute("branch", "create", branch, "-R", repo.FullName)
		commitFile(branch, style+"-one.txt")
		commitFile(branch, style+"-two.txt")
		var pull services.Pull
		require.NoError(t, json.Unmarshal(execute("pr", "create", "-R", repo.FullName, "--head", branch, "--base", repo.DefaultBranch,
			"--title", "Add "+style, "--body", "Body of "+style, "--label", "kind/bug", "--json"), &pull))
		return strconv.FormatInt(pull.Number, 10)
	}
	merge := func(number, style string) {
		// Gitea checks mergeability in the background after a pull request is created or its base moves.
		var err error
		for range 30 {
			out.Reset()
			diagnostics.Reset()
			if err = Execute(context.Background(), deps, []string{"pr", "merge", number, "-R", repo.FullName, "--method", style, "--yes"}); err == nil {
				return
			}
			time.Sleep(time.Second)
		}
		require.NoError(t, err, style)
	}
	// The rebase styles must rewrite commits, so their branches start before main moves on.
	styles := []string{"merge", "squash", "rebase", "rebase-merge"}
	numbers := map[string]string{}
	for _, style := range styles {
		numbers[style] = open(style)
	}
	for _, style := range styles {
		merge(numbers[style], style)
	}
	numbers["fast-forward-only"] = open("fast-forward-only")
	merge(numbers["fast-forward-only"], "fast-forward-only")
	styles = append(styles, "fast-forward-only")

	work := filepath.Join(t.TempDir(), "work")
	execute("repo", "clone", repo.FullName, work, "--no-input")
	picked := map[string]int{"merge": 2, "squash": 1, "rebase": 2, "rebase-merge": 2, "fast-forward-only": 2}
	for _, style := range styles {
		var results []backportOutput
		require.NoError(t, json.Unmarshal(execute("pr", "backport", numbers[style], "--to", "release/v1", "-R", repo.FullName, "--cwd", work, "--yes", "--json"), &results), style)
		require.Len(t, results, 1, style)
		result := results[0]
		require.Len(t, result.Picked, picked[style], style)
		require.Len(t, result.Commits, picked[style], style)
		require.Equal(t, "release/v1", result.Pull.Base, style)
		require.Equal(t, fmt.Sprintf("Add %s (#%s)", style, numbers[style]), result.Pull.Title, style)
		var backport services.Pull
		require.NoError(t, json.Unmarshal(execute("pr", "view", strconv.FormatInt(result.Pull.Number, 10), "-R", repo.FullName, "--json"), &backport))
		require.Equal(t, fmt.Sprintf("Backport #%s by @%s\n\nBody of %s", numbers[style], user, style), backport.Body, style)
		require.Len(t, backport.Labels, 1, style)
		require.Equal(t, "kind/bug", backport.Labels[0].Name, style)
		var files []struct {
			Filename string `json:"filename"`
		}
		require.NoError(t, json.Unmarshal(execute("api", fmt.Sprintf("repos/%s/pulls/%d/files", repo.FullName, result.Pull.Number)), &files), style)
		names := make([]string, 0, len(files))
		for _, file := range files {
			names = append(names, file.Filename)
		}
		require.ElementsMatch(t, []string{style + "-one.txt", style + "-two.txt"}, names, "only the pull request's changes, %s", style)
	}
}
