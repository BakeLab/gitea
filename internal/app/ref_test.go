// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestParseRefAcceptsNumbersQualifiedNumbersAndURLs(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		kind refKind
		want ref
		err  string
	}{
		{raw: "12", want: ref{Number: 12}},
		{raw: "#12", want: ref{Number: 12}},
		{raw: " 12 ", want: ref{Number: 12}},
		{raw: "alice/project#12", want: ref{Number: 12, Repo: "alice/project"}},
		{raw: "localhost:3001/alice/project#12", want: ref{Number: 12, Repo: "localhost:3001/alice/project"}},
		{raw: "https://gitea.com/alice/project/issues/3", want: ref{Number: 3, Repo: "https://gitea.com/alice/project"}},
		{raw: "https://gitea.com/alice/project/issues/3#issuecomment-79", want: ref{Number: 3, Repo: "https://gitea.com/alice/project"}},
		{raw: "http://localhost:3001/base/alice/project/pulls/7/files?style=split", kind: pullRef, want: ref{Number: 7, Repo: "http://localhost:3001/base/alice/project"}},
		{raw: "https://gitea.com/alice/issues/issues/4", want: ref{Number: 4, Repo: "https://gitea.com/alice/issues"}},
		{raw: "https://gitea.com/alice/project/pulls/7", kind: issueRef, want: ref{Number: 7, Repo: "https://gitea.com/alice/project"}},
		{raw: "https://gitea.com/alice/project/actions/runs/34", kind: runRef, want: ref{Number: 34, Repo: "https://gitea.com/alice/project"}},
		{raw: "https://gitea.com/alice/project/actions/runs/34/jobs/2", kind: runRef, want: ref{Number: 34, Repo: "https://gitea.com/alice/project", Job: 2}},
		{raw: "#34", kind: runRef, want: ref{Number: 34}},
		{raw: "0", err: `invalid issue "0": use a number, #number, OWNER/REPO#number or URL`},
		{raw: "#abc", kind: pullRef, err: `invalid pull request "#abc"`},
		{raw: "alice/project#", err: `invalid issue "alice/project#"`},
		{raw: "alice/project#3", kind: runRef, err: `invalid run "alice/project#3": use a run ID or URL`},
		{raw: "https://gitea.com/alice/project", err: `"https://gitea.com/alice/project" is not an issue URL`},
		{raw: "https://gitea.com/alice/project/issues/3", kind: runRef, err: "is not a run URL"},
		{raw: "ftp://gitea.com/alice/project/issues/3", err: "is not an issue URL"},
	} {
		got, err := parseRef(tc.raw, tc.kind)
		if tc.err != "" {
			require.ErrorContains(t, err, tc.err, tc.raw)
			require.Equal(t, clierr.Usage, clierr.ExitCode(err), tc.raw)
			continue
		}
		require.NoError(t, err, tc.raw)
		require.Equal(t, tc.want, got, tc.raw)
	}
	for _, branch := range []string{"feature/login", "bob:fix", "main", "fix#12", "project#12", "bob:fix#3"} {
		_, err := parseRef(branch, pullRef)
		require.ErrorIs(t, err, errNotRef, branch)
	}
}

// refApplication returns an application authenticated against a server listing the open pull
// requests #3 (feat/one in alice/project), #4 (feat/two), #17 (fix in bob/project) and two
// pull requests from branch dup. Viewing one pull request finds it open, except the merged #30.
func refApplication(t *testing.T, gitResults map[string]string) (*application, string) {
	t.Helper()
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if number, ok := strings.CutPrefix(r.URL.Path, "/api/v1/repos/alice/project/pulls/"); ok {
			state := "open"
			if number == "30" {
				state = "closed"
			}
			_, _ = w.Write([]byte(`{"number":` + number + `,"state":"` + state + `","merged":true}`))
			return
		}
		require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		writeFirstPage(w, r, `[
			{"number":3,"head":{"ref":"feat/one","repo":{"full_name":"alice/project"}}},
			{"number":4,"head":{"ref":"feat/two","repo":{"full_name":"alice/project"}}},
			{"number":17,"head":{"ref":"fix","repo":{"full_name":"bob/project"}}},
			{"number":20,"head":{"ref":"dup","repo":{"full_name":"alice/project"}}},
			{"number":21,"head":{"ref":"dup","repo":{"full_name":"carol/project"}}},
			{"number":22,"head":{"ref":"main","repo":{"full_name":"bob/project"}}}
		]`)
	}))
	t.Cleanup(server.Close)
	return refApplicationWith(t, server.URL, gitResults)
}

// refApplicationWith is refApplication for an existing server.
func refApplicationWith(t *testing.T, serverURL string, gitResults map[string]string) (*application, string) {
	t.Helper()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(serverURL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	results := map[string]string{
		"remote get-url origin":              serverURL + "/alice/project.git",
		"remote get-url pulls/bob/project":   serverURL + "/bob/project.git",
		"remote get-url fork":                serverURL + "/bob/project.git",
		"remote get-url pulls/carol/project": serverURL + "/carol/project.git",
	}
	for key, value := range gitResults {
		results[key] = value
	}
	deps.Git = &fakeGit{results: results}
	return &application{deps: deps}, serverURL
}

func commandContext() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd
}

func TestPullSelectorDefaultsToTheCheckedOutBranch(t *testing.T) {
	const head = "symbolic-ref --quiet HEAD"
	branch := func(name string, keys map[string]string) map[string]string {
		results := map[string]string{head: name}
		for key, value := range keys {
			results["config --get branch."+name+"."+key] = value
		}
		return results
	}
	for _, tc := range []struct {
		name string
		git  map[string]string
		want int64
		err  string
	}{
		{"recorded by pr checkout", branch("pr/17", map[string]string{"gitea-cli-pr": "17", "gitea-cli-base-remote": "origin", "remote": "pulls/bob/project", "merge": "refs/heads/fix"}), 17, ""},
		{"head branch name kept by pr checkout", branch("fix", map[string]string{"gitea-cli-pr": "17", "gitea-cli-base-remote": "origin"}), 17, ""},
		{"recorded but merged, the head branch has a newer one", branch("fix", map[string]string{"gitea-cli-pr": "30", "gitea-cli-base-remote": "origin", "remote": "pulls/bob/project", "merge": "refs/heads/fix"}), 17, ""},
		{"recorded but merged, no open one", branch("gone", map[string]string{"gitea-cli-pr": "30", "gitea-cli-base-remote": "origin"}), 30, ""},
		{"recorded for another repository", branch("pr/9", map[string]string{"gitea-cli-pr": "9", "gitea-cli-base-remote": "fork", "remote": "fork", "merge": "refs/heads/fix"}), 17, ""},
		{"upstream branch and owner", branch("local", map[string]string{"remote": "origin", "merge": "refs/heads/feat/one"}), 3, ""},
		{"upstream owner tells forks apart", branch("dup", map[string]string{"remote": "pulls/carol/project", "merge": "refs/heads/dup"}), 21, ""},
		{"local branch name", branch("feat/two", nil), 4, ""},
		{"pr/N branch", branch("pr/99", nil), 99, ""},
		{"pr/N branch recording another pull request", branch("pr/2", map[string]string{"gitea-cli-pr": "30", "gitea-cli-base-remote": "origin"}), 30, ""},
		{"fork pull request from a branch named like the local one", branch("main", map[string]string{"remote": "origin", "merge": "refs/heads/main"}), 0, `no open pull request for branch "main" in alice/project; pass a number, URL or branch`},
		{"no pull request", branch("main", nil), 0, `no open pull request for branch "main" in alice/project; pass a number, URL or branch`},
		{"same-named fork branch is ignored", branch("dup", nil), 20, ""},
		{"detached", map[string]string{}, 0, "not on a branch; pass a pull request number, URL or branch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := refApplication(t, tc.git)
			_, n, err := a.pullSelector(commandContext(), nil)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, n)
		})
	}
	a, _ := refApplication(t, branch("feat/two", nil))
	a.options.repo = "alice/project"
	cmd := commandContext()
	cmd.Use = "update-branch"
	_, _, err := a.pullSelector(cmd, nil)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "update-branch requires a pull request number, URL or branch with --repo")
}

func TestPullSelectorAcceptsNumbersURLsAndBranches(t *testing.T) {
	a, serverURL := refApplication(t, nil)
	for _, tc := range []struct {
		arg  string
		want int64
		err  string
	}{
		{"5", 5, ""},
		{"#5", 5, ""},
		{serverURL + "/alice/project/pulls/5", 5, ""},
		{"alice/project#5", 5, ""},
		{"feat/one", 3, ""},
		{"bob:fix", 17, ""},
		{"alice:fix", 0, `no open pull request for branch "alice:fix" in alice/project; pass a number, URL or branch`},
		{"carol:dup", 21, ""},
		{"fix#12", 0, `no open pull request for branch "fix#12" in alice/project`},
		{"dup", 0, "several open pull requests"},
		{serverURL + "/alice/project", 0, "is not a pull request URL"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			resolved, n, err := a.pullSelector(commandContext(), []string{tc.arg})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, n)
			require.Equal(t, "alice/project", resolved.Repo)
		})
	}
}

func TestResolveRefSelectsTheReferencedRepository(t *testing.T) {
	a, serverURL := refApplication(t, nil)
	resolved, n, err := a.resolveRef(commandContext(), serverURL+"/bob/tool/issues/8", issueRef)
	require.NoError(t, err)
	require.Equal(t, int64(8), n)
	require.Equal(t, "bob/tool", resolved.Repo)
	require.Equal(t, serverURL, resolved.Host)

	mixed := []string{"bob/tool#1", "#2", serverURL + "/bob/tool/issues/3"}
	requireBareRefAdvice(t, a, mixed, "#2 cannot be combined with "+serverURL+"/bob/tool/issues/3; qualify it as bob/tool#2, or run a separate command for alice/project#2", "GITEA_CLI_REPO")
	environment := a.deps.Getenv
	a.deps.Getenv = serverEnv(serverURL, nil)
	fake := a.deps.Git.(*fakeGit)
	fake.results["remote"] = "origin"
	requireBareRefAdvice(t, a, []string{"2", "bob/tool#1"}, "2 cannot be combined with bob/tool#1; qualify it as bob/tool#2, or run a separate command for alice/project#2", "a clone of another repository")
	for _, origin := range []string{serverURL + "/bob/tool.git", "https://github.com/foo/bar.git", "ssh://git@127.0.0.1:2222/alice/project.git"} {
		fake.results["remote get-url origin"] = origin
		resolved, numbers, err := a.resolveRefs(commandContext(), []string{"2", "bob/tool#1"}, issueRef)
		require.NoError(t, err, "a clone of %s, the referenced repository or one gt cannot act on", origin)
		require.Equal(t, []int64{2, 1}, numbers)
		require.Equal(t, "bob/tool", resolved.Repo)
	}
	delete(fake.results, "remote")
	resolved, numbers, err := a.resolveRefs(commandContext(), mixed, issueRef)
	require.NoError(t, err, "outside a clone")
	require.Equal(t, []int64{1, 2, 3}, numbers)
	require.Equal(t, "bob/tool", resolved.Repo)
	a.deps.Getenv = environment

	for _, second := range []string{"alice/project#2", "alice/project#1"} {
		_, _, err = a.resolveRefs(commandContext(), []string{"bob/tool#1", second}, issueRef)
		require.EqualError(t, err, "bob/tool#1 and "+second+" are in different repositories; run one command per repository")
	}
	_, _, err = a.resolveRefs(commandContext(), []string{"1", "#1"}, issueRef)
	require.EqualError(t, err, "#1 is selected more than once; supply unique numbers")
	_, _, err = a.resolveRef(commandContext(), "feature", issueRef)
	require.EqualError(t, err, `invalid issue "feature": use a number, #number, OWNER/REPO#number or URL`)

	a.options.repo = "BOB/tool"
	resolved, _, err = a.resolveRef(commandContext(), "bob/tool#1", issueRef)
	require.NoError(t, err, "the same repository as --repo")
	require.Equal(t, "bob/tool", resolved.Repo)
	a.options.repo = "bob/other"
	_, _, err = a.resolveRef(commandContext(), serverURL+"/bob/tool/issues/1", issueRef)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, serverURL+"/bob/tool/issues/1 points to bob/tool but --repo is bob/other")
	a.options.repo = "not a repo"
	_, _, err = a.resolveRef(commandContext(), serverURL+"/bob/tool/issues/1", issueRef)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `invalid repository "not a repo"`)
}

// requireBareRefAdvice checks that raws are refused with want, and that following either
// suggestion resolves: the qualified number in its place, or the other one on its own.
func requireBareRefAdvice(t *testing.T, a *application, raws []string, want, msg string) {
	t.Helper()
	_, _, err := a.resolveRefs(commandContext(), raws, issueRef)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), msg)
	require.EqualError(t, err, want, msg)
	bare, advice, _ := strings.Cut(err.Error(), " cannot be combined with ")
	_, advice, _ = strings.Cut(advice, "; qualify it as ")
	qualified, separate, _ := strings.Cut(advice, ", or run a separate command for ")
	followed := slices.Clone(raws)
	followed[slices.Index(raws, bare)] = qualified
	referenced, _, err := a.resolveRefs(commandContext(), followed, issueRef)
	require.NoError(t, err, msg)
	other, _, err := a.resolveRef(commandContext(), separate, issueRef)
	require.NoError(t, err, msg)
	require.NotEqual(t, referenced.Host+"/"+referenced.Repo, other.Host+"/"+other.Repo, msg)
}

func TestBareRefAdviceNamesAnotherInstance(t *testing.T) {
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		servers[i] = newAuthenticatedServer(http.NotFoundHandler())
		t.Cleanup(servers[i].Close)
	}
	defaultURL, cloneURL := servers[0].URL, servers[1].URL
	a, _ := refApplicationWith(t, defaultURL, map[string]string{"remote": "origin", "remote get-url origin": cloneURL + "/alice/project.git"})
	a.deps.Getenv = envFunc(nil)
	configureAccounts(t, a.deps, defaultURL, "alice", "alice")
	configureAccounts(t, a.deps, cloneURL, "alice", "alice")
	require.NoError(t, a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.DefaultHost = defaultURL; return nil }))
	bare := strings.TrimPrefix(cloneURL, "http://")
	requireBareRefAdvice(t, a, []string{"bob/tool#1", "2"}, "2 cannot be combined with bob/tool#1; qualify it as bob/tool#2, or run a separate command for "+bare+"/alice/project#2", "a clone on another instance")
}

func TestInferredRepositoryCommandNamesTheInstanceOfTheClone(t *testing.T) {
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		servers[i] = newAuthenticatedServer(http.NotFoundHandler())
		t.Cleanup(servers[i].Close)
	}
	defaultURL, cloneURL := servers[0].URL, servers[1].URL
	a, _ := refApplicationWith(t, defaultURL, map[string]string{"remote": "origin", "remote get-url origin": cloneURL + "/alice/project.git"})
	a.deps.Getenv = envFunc(nil)
	configureAccounts(t, a.deps, defaultURL, "alice", "alice")
	configureAccounts(t, a.deps, cloneURL, "alice", "alice")
	require.NoError(t, a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.DefaultHost = defaultURL; return nil }))
	resolved, err := a.resolve(context.Background(), true)
	require.NoError(t, err)
	a.options.yes = true
	_, err = a.inferredRepository(resolved, "delete", true)
	require.ErrorContains(t, err, ": gt repo delete "+cloneURL+"/alice/project --yes", "alice/project alone would delete the repository of that name on the default instance")

	configureAccounts(t, a.deps, cloneURL, "alice", "alice", "bob")
	a.options.user, a.options.sudo = "bob", "carol"
	resolved, err = a.resolve(context.Background(), true)
	require.NoError(t, err)
	_, err = a.inferredRepository(resolved, "archive", true)
	require.ErrorContains(t, err, ": gt repo archive "+cloneURL+"/alice/project --user bob --sudo carol --yes", "without them the command would run as alice")
}

func TestExpandMeUsesTheLoginTeaActsAs(t *testing.T) {
	alice := target{User: "alice"}
	require.Equal(t, "alice", alice.expandMe("@me"))
	require.Equal(t, "bob", alice.expandMe("bob"))
	assignees := []string{"@me", "carol"}
	alice.expandMeAll(assignees)
	require.Equal(t, []string{"alice", "carol"}, assignees)
	a, _ := refApplication(t, nil)
	a.options.sudo = "bob"
	resolved, err := a.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "bob", resolved.expandMe("@me"))
}

func TestResolveRefsKeepsTheReferenceThatNamesAnInstance(t *testing.T) {
	a, serverURL := refApplication(t, nil)
	// GITEA_TOKEN belongs to serverURL, so selecting the other instance is what fails here.
	other := strings.Replace(serverURL, "127.0.0.1", "localhost", 1)
	const mismatch = "matching the selected instance"
	for _, raws := range [][]string{
		{other + "/bob/tool/issues/1", "bob/tool#2"},
		{"bob/tool#2", other + "/bob/tool/issues/1"},
	} {
		_, _, err := a.resolveRefs(commandContext(), raws, issueRef)
		require.ErrorContains(t, err, mismatch, raws)
	}
	a.options.repo = other + "/bob/tool"
	_, _, err := a.resolveRef(commandContext(), "bob/tool#2", issueRef)
	require.ErrorContains(t, err, mismatch, "an argument without an instance keeps the --repo one")
}
