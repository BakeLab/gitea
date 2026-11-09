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

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

// optionsRepo is a disposable repository for the pull request option tests.
type optionsRepo struct {
	services.Repository
	deps    *Dependencies
	execute func(args ...string) []byte
}

func newOptionsRepo(t *testing.T, prefix string) optionsRepo {
	t.Helper()
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	return optionsRepo{Repository: repo, deps: &deps, execute: execute}
}

func (r optionsRepo) scoped(args ...string) []byte {
	return r.execute(append(args, "-R", r.FullName)...)
}

func (r optionsRepo) api(path, jq string) string {
	return strings.TrimSpace(string(r.execute("api", "repos/"+r.FullName+"/"+path, "--jq", jq)))
}

// commit writes a file to a branch through the API.
func (r optionsRepo) commit(branch, name string) {
	payload, err := json.Marshal(map[string]string{"branch": branch, "message": "Add " + name, "content": base64.StdEncoding.EncodeToString([]byte(name + "\n"))})
	if err != nil {
		panic(err)
	}
	r.deps.Streams.In = strings.NewReader(string(payload))
	r.execute("api", "repos/"+r.FullName+"/contents/"+name, "--input", "-")
}

// pull makes a branch with a commit and a pull request from it into the default branch.
func (r optionsRepo) pull(t *testing.T, branch string) (number, head string) {
	t.Helper()
	r.scoped("branch", "create", branch)
	r.commit(branch, branch+".txt")
	var pull services.Pull
	require.NoError(t, json.Unmarshal(r.scoped("pr", "create", "--title", "Change "+branch, "--body", "b", "--head", branch, "--base", r.DefaultBranch, "--json"), &pull))
	number = strconv.FormatInt(pull.Number, 10)
	return number, r.api("pulls/"+number, ".head.sha")
}

func (r optionsRepo) fails(args ...string) error {
	return Execute(context.Background(), *r.deps, append(args, "-R", r.FullName, "--no-input"))
}

func TestGiteaPullMergeAdminMatchHeadCommitAndDisableAuto(t *testing.T) {
	repo := newOptionsRepo(t, "gt-merge-options")

	t.Run("match-head-commit", func(t *testing.T) {
		number, reviewed := repo.pull(t, "reviewed")
		repo.commit("reviewed", "pushed-after-review.txt")
		moved := repo.api("pulls/"+number, ".head.sha")
		require.NotEqual(t, reviewed, moved)

		err := repo.fails("pr", "merge", number, "--squash", "--yes", "--match-head-commit", reviewed)
		require.Equal(t, clierr.HeadMismatch, clierr.ExitCode(err), "the head moved after the review")
		require.ErrorContains(t, err, "is "+moved[:7]+", not "+reviewed[:7])
		require.Equal(t, "false", repo.api("pulls/"+number, ".merged"), "nothing was merged")

		require.NoError(t, repo.fails("pr", "merge", number, "--squash", "--yes", "--match-head-commit", strings.ToUpper(moved[:10])))
		require.Equal(t, "true", repo.api("pulls/"+number, ".merged"), "the commit that was named is the head, so it merges")
	})

	t.Run("admin", func(t *testing.T) {
		repo.scoped("branch", "protect", repo.DefaultBranch, "--required-approvals", "1")
		number, _ := repo.pull(t, "needs-approval")
		require.Error(t, repo.fails("pr", "merge", number, "--squash", "--yes"), "the protection asks for an approval that there is none of")
		require.Equal(t, "false", repo.api("pulls/"+number, ".merged"))
		require.NoError(t, repo.fails("pr", "merge", number, "--squash", "--yes", "--admin"))
		require.Equal(t, "true", repo.api("pulls/"+number, ".merged"), "an administrator merges past the protection")
		repo.scoped("branch", "unprotect", repo.DefaultBranch, "--yes")
	})

	t.Run("disable-auto", func(t *testing.T) {
		repo.scoped("branch", "protect", repo.DefaultBranch, "--status-check", "ci/never")
		number, head := repo.pull(t, "scheduled")
		repo.execute("api", "repos/"+repo.FullName+"/statuses/"+head, "-f", "state=pending", "-f", "context=ci/never")

		require.NoError(t, repo.fails("pr", "merge", number, "--auto", "--squash", "--yes"))
		require.Equal(t, "false", repo.api("pulls/"+number, ".merged"), "the merge waits for the check")
		require.NoError(t, repo.fails("pr", "merge", number, "--disable-auto"))
		require.NoError(t, repo.fails("pr", "merge", number, "--disable-auto"), "a second cancel finds nothing scheduled and says so")
		repo.execute("api", "repos/"+repo.FullName+"/statuses/"+head, "-f", "state=success", "-f", "context=ci/never")
		time.Sleep(3 * time.Second)
		require.Equal(t, "false", repo.api("pulls/"+number, ".merged"), "the canceled merge does not happen when the check passes")
		require.Equal(t, clierr.Usage, clierr.ExitCode(repo.fails("pr", "merge", number, "--disable-auto", "--squash")))
	})
}

func TestGiteaPullChecksRequired(t *testing.T) {
	repo := newOptionsRepo(t, "gt-required-checks")
	repo.scoped("branch", "protect", repo.DefaultBranch, "--status-check", "ci/required,lint/*")
	number, head := repo.pull(t, "checked")
	report := func(context, state string) {
		repo.execute("api", "repos/"+repo.FullName+"/statuses/"+head, "-f", "state="+state, "-f", "context="+context)
	}
	checks := func(args ...string) (string, error) {
		deps := *repo.deps
		buffer := &strings.Builder{}
		deps.Streams.Out = buffer
		err := Execute(context.Background(), deps, append([]string{"pr", "checks", number, "-R", repo.FullName, "--no-input", "--json", "name,state"}, args...))
		return buffer.String(), err
	}
	report("ci/required", "pending")
	report("ci/optional", "failure")
	report("lint/go", "success")

	out, err := checks()
	require.Equal(t, 1, clierr.ExitCode(err), "the optional check failed")
	require.JSONEq(t, `[{"name":"ci/optional","state":"failure"},{"name":"ci/required","state":"pending"},{"name":"lint/go","state":"success"}]`, out)

	repo.scoped("branch", "protect", repo.DefaultBranch, "--status-check", "ci/required,lint/*,ci/late")
	out, err = checks("--required")
	require.Equal(t, clierr.Pending, clierr.ExitCode(err))
	require.JSONEq(t, `[{"name":"ci/late","state":"pending"},{"name":"ci/required","state":"pending"},{"name":"lint/go","state":"success"}]`, out, "a required check that has not reported is pending")
	repo.scoped("branch", "protect", repo.DefaultBranch, "--status-check", "ci/required,lint/*")

	out, err = checks("--required")
	require.Equal(t, clierr.Pending, clierr.ExitCode(err), "the required check is still running")
	require.JSONEq(t, `[{"name":"ci/required","state":"pending"},{"name":"lint/go","state":"success"}]`, out, "the optional check is not shown, and lint/* matches lint/go")

	report("ci/required", "success")
	out, err = checks("--required", "--watch", "--interval", "1s", "--timeout", "30s")
	require.NoError(t, err, "with the required checks done the failed optional one does not hold --watch")
	require.JSONEq(t, `[{"name":"ci/required","state":"success"},{"name":"lint/go","state":"success"}]`, out)

	// A pull request into an unprotected branch has no required checks.
	repo.scoped("branch", "create", "unprotected")
	repo.scoped("branch", "create", "feature-2")
	repo.commit("feature-2", "feature-2.txt")
	var pull services.Pull
	require.NoError(t, json.Unmarshal(repo.scoped("pr", "create", "--title", "Into unprotected", "--head", "feature-2", "--base", "unprotected", "--json"), &pull))
	buffer := &strings.Builder{}
	deps := *repo.deps
	deps.Streams.Out = buffer
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"pr", "checks", strconv.FormatInt(pull.Number, 10), "-R", repo.FullName, "--required", "--no-input", "--json"}), "no required checks reported on the 'feature-2' branch")
	require.Empty(t, buffer.String())
}

// TestGiteaPullCreateFillFirstAndFillVerbose makes a branch of three commits in a clone and opens
// pull requests from it, which the flags fill from the commits.
func TestGiteaPullCreateFillFirstAndFillVerbose(t *testing.T) {
	gitea, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-fill-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=gt", "GIT_AUTHOR_EMAIL=gt@example.invalid", "GIT_COMMITTER_NAME=gt", "GIT_COMMITTER_EMAIL=gt@example.invalid")
	clone := filepath.Join(t.TempDir(), "clone")
	run := func(dir string, args ...string) {
		t.Helper()
		command := exec.CommandContext(context.Background(), "git", args...)
		command.Dir, command.Env = dir, env
		output, err := command.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), output)
	}
	user, _, _ := strings.Cut(repo.FullName, "/")
	authenticated := strings.Replace(gitea.url, "://", "://"+user+":"+gitea.token+"@", 1) + "/" + repo.FullName + ".git"
	run(t.TempDir(), "clone", "-q", authenticated, clone)
	run(clone, "checkout", "-q", "-b", "feature")
	for i, message := range []string{"Add the form\n\nIt validates the input.", "Fix a typo", "Handle empty input\n\nNeeds a test too."} {
		require.NoError(t, os.WriteFile(filepath.Join(clone, fmt.Sprintf("file-%d.txt", i)), []byte("x\n"), 0o600))
		run(clone, "add", ".")
		run(clone, "commit", "-q", "-m", message)
	}
	run(clone, "push", "-q", "-u", "origin", "feature")

	deps.Git = git.System{Env: env}
	var pull services.Pull
	create := func(args ...string) services.Pull {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "create", "-R", repo.FullName, "--cwd", clone, "--head", "feature", "--base", repo.DefaultBranch, "--json", "--no-input", "--yes"}, args...)))
		require.NoError(t, json.Unmarshal(out.Bytes(), &pull))
		return pull
	}
	closeLast := func() {
		execute("pr", "close", strconv.FormatInt(pull.Number, 10), "-R", repo.FullName)
	}

	filled := create("--fill")
	require.Equal(t, "Feature", filled.Title, "--fill takes the branch name when there are several commits")
	require.Empty(t, filled.Body)
	closeLast()

	filled = create("--fill-first")
	require.Equal(t, "Add the form", filled.Title)
	require.Equal(t, "It validates the input.", filled.Body)
	closeLast()

	filled = create("--fill-verbose")
	require.Equal(t, "Feature", filled.Title)
	require.Equal(t, "Add the form\n\nIt validates the input.\n\n---\n\nFix a typo\n\n---\n\nHandle empty input\n\nNeeds a test too.", filled.Body)
	closeLast()
}
