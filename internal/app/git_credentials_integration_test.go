// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/tea/internal/auth"
	"gitea.dev/tea/internal/git"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestGiteaGitCredentialHelperClonesPrivateRepository(t *testing.T) {
	gitea, deps, out, diagnostics := giteaDeps(t)
	deps.Settings = settings.New(filepath.Join(t.TempDir(), "configuration with spaces"))
	deps.Credentials.Dir = deps.Settings.Dir
	var repo services.Repository
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", fmt.Sprintf("tea-git-helper-%d", time.Now().UnixNano()), "--private", "--auto-init", "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &repo))
	require.True(t, repo.Private)
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	user, _, _ := strings.Cut(repo.FullName, "/")
	require.NoError(t, testManager(t, deps).Register(gitea.url, user, "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: gitea.token}))
	binaryDir := filepath.Join(t.TempDir(), "binary with 'quote'")
	require.NoError(t, os.MkdirAll(binaryDir, 0o700))
	binary := filepath.Join(binaryDir, "tea")
	if os.PathSeparator == '\\' {
		binary += ".exe"
	}
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, ".")
	build.Dir = filepath.Join("..", "..")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", output)
	deps.Executable = func() (string, error) { return binary, nil }
	// The temporary global file exercises setup's default scope without touching
	// the user's Git configuration. Git invokes the compiled helper as a child.
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_TRACE") || key == "GIT_CURL_VERBOSE" {
			continue
		}
		environment = append(environment, entry)
	}
	globalConfig := filepath.Join(t.TempDir(), "gitconfig")
	environment = append(environment, "GIT_CONFIG_GLOBAL="+globalConfig, "GIT_CONFIG_NOSYSTEM=1", "TEA_CONFIG_DIR="+deps.Settings.Dir, "GITEA_TOKEN=", "GITEA_INSTANCE_URL=", "TEA_HOST=", "TEA_USER=", "TEA_REPO=")
	runner := git.System{Env: environment, ErrOut: diagnostics}
	deps.Git = runner
	_, anonymousErr := runner.Output(context.Background(), "", false, "-c", "credential.helper=", "clone", "--", repo.CloneURL, filepath.Join(t.TempDir(), "anonymous-clone"))
	require.Error(t, anonymousErr, "private repository must reject an unauthenticated clone")
	out.Reset()
	diagnostics.Reset()
	teaClone := filepath.Join(t.TempDir(), "tea-private-clone")
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", repo.FullName, teaClone, "--no-input", "--json=id"}))
	require.JSONEq(t, fmt.Sprintf(`{"id":%d}`, repo.ID), out.String())
	_, localHelperErr := runner.Output(context.Background(), teaClone, false, "config", "--local", "--get-all", "credential."+gitea.url+".helper")
	var missingLocalHelper *git.CommandError
	require.ErrorAs(t, localHelperErr, &missingLocalHelper)
	require.Equal(t, 1, missingLocalHelper.ExitCode)
	// Build a changed PR on the private repository, then fetch it without setup.
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"branch", "create", "feature", "-R", repo.FullName}))
	deps.Streams.In = strings.NewReader(`{"branch":"feature","content":"dGVzdAo=","message":"Disposable private PR fixture"}`)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/" + repo.FullName + "/contents/change.txt", "--input", "-"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", repo.FullName, "--title", "Private checkout", "--head", "feature", "--base", repo.DefaultBranch, "--json"}))
	var pull services.Pull
	require.NoError(t, json.Unmarshal(out.Bytes(), &pull))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", fmt.Sprint(pull.Number), "-R", repo.FullName, "--cwd", teaClone, "--no-input", "--json"}))
	var checkout git.CheckoutResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &checkout))
	require.Equal(t, pull.HeadSHA, checkout.Commit)
	require.Equal(t, "pr/"+fmt.Sprint(pull.Number), checkout.Branch)
	branchesBefore, err := runner.Output(context.Background(), teaClone, false, "for-each-ref", "--format=%(refname):%(objectname)", "refs/heads/")
	require.NoError(t, err)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", fmt.Sprint(pull.Number), "-R", repo.FullName, "--cwd", teaClone, "--detach", "--no-input", "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &checkout))
	require.True(t, checkout.Detached)
	require.Empty(t, checkout.Branch)
	require.Equal(t, pull.HeadSHA, checkout.Commit)
	branchesAfter, err := runner.Output(context.Background(), teaClone, false, "for-each-ref", "--format=%(refname):%(objectname)", "refs/heads/")
	require.NoError(t, err)
	require.Equal(t, branchesBefore, branchesAfter)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", fmt.Sprint(pull.Number), "-R", repo.FullName, "--cwd", teaClone, "--no-input", "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &checkout))
	require.False(t, checkout.Detached)
	require.Equal(t, "pr/"+fmt.Sprint(pull.Number), checkout.Branch)
	branchesAfter, err = runner.Output(context.Background(), teaClone, false, "for-each-ref", "--format=%(refname):%(objectname)", "refs/heads/")
	require.NoError(t, err)
	require.Equal(t, branchesBefore, branchesAfter)
	_, err = runner.Output(context.Background(), teaClone, false, "switch", "--detach", pull.HeadSHA)
	require.NoError(t, err)
	_, headErr := runner.Output(context.Background(), teaClone, false, "symbolic-ref", "--quiet", "HEAD")
	var detachedHead *git.CommandError
	require.ErrorAs(t, headErr, &detachedHead)
	require.Equal(t, 1, detachedHead.ExitCode)
	deps.Streams.In = strings.NewReader(`{"branch":"feature","content":"bmV4dAo=","message":"Disposable fast-forward fixture"}`)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/" + repo.FullName + "/contents/next.txt", "--input", "-"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", fmt.Sprint(pull.Number), "-R", repo.FullName, "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &pull))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", fmt.Sprint(pull.Number), "-R", repo.FullName, "--cwd", teaClone, "--update", "--no-input", "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &checkout))
	require.Equal(t, pull.HeadSHA, checkout.Commit)
	localHead, err := runner.Output(context.Background(), teaClone, false, "rev-parse", "HEAD")
	require.NoError(t, err)
	require.Equal(t, pull.HeadSHA, localHead)
	deps.Streams.In = strings.NewReader(fmt.Sprintf(`{"branch":%q,"content":"c3luYwo=","message":"Disposable sync fixture"}`, repo.DefaultBranch))
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/" + repo.FullName + "/contents/sync.txt", "--input", "-"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "sync", "-R", repo.FullName, "--cwd", teaClone, "--no-input", "--json=branch,commit,remote"}))
	var synced git.SyncResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &synced))
	require.Equal(t, repo.DefaultBranch, synced.Branch)
	localHead, err = runner.Output(context.Background(), teaClone, false, "rev-parse", "HEAD")
	require.NoError(t, err)
	require.Equal(t, synced.Commit, localHead)
	contents, err := os.ReadFile(filepath.Join(teaClone, "sync.txt"))
	require.NoError(t, err)
	require.Equal(t, "sync\n", string(contents))
	if endpoint := os.Getenv("GITEA_TEA_TEST_SSH_ENDPOINT"); endpoint != "" {
		verifySSHCloneAndCheckout(t, deps, repo, pull, user, environment, endpoint)
	}
	// The per-operation helper does not create a global Git configuration file.
	_, err = os.Stat(globalConfig)
	require.True(t, os.IsNotExist(err))
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "setup-git", "--host", gitea.url, "--no-input"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "setup-git", "--host", gitea.url, "--no-input"}))
	require.Empty(t, out.String())
	destination := filepath.Join(t.TempDir(), "private-clone")
	_, err = runner.Output(context.Background(), "", false, "clone", "--", repo.CloneURL, destination)
	require.NoError(t, err, "private repository clone failed")
	inside, err := runner.Output(context.Background(), destination, false, "rev-parse", "--is-inside-work-tree")
	require.NoError(t, err)
	require.Equal(t, "true", inside)
	// No credential is embedded in either the persisted remote or helper command.
	remote, err := runner.Output(context.Background(), destination, false, "remote", "get-url", "origin")
	require.NoError(t, err)
	require.Equal(t, repo.CloneURL, remote)
	require.False(t, strings.Contains(remote, gitea.token), "remote contains credentials")
	helper, err := runner.Output(context.Background(), "", false, "config", "--global", "--get-all", "credential."+gitea.url+".helper")
	require.NoError(t, err)
	require.Len(t, strings.Split(helper, "\n"), 1)
	require.False(t, strings.Contains(helper, gitea.token), "helper command contains credentials")
}
