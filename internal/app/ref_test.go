// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
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
		{raw: "project#12", want: ref{Number: 12, Repo: "project"}},
		{raw: "https://gitea.com/alice/project/issues/3", want: ref{Number: 3, Repo: "https://gitea.com/alice/project"}},
		{raw: "https://gitea.com/alice/project/issues/3#issuecomment-79", want: ref{Number: 3, Repo: "https://gitea.com/alice/project"}},
		{raw: "http://localhost:3001/base/alice/project/pulls/7/files?style=split", kind: pullRef, want: ref{Number: 7, Repo: "http://localhost:3001/base/alice/project"}},
		{raw: "https://gitea.com/alice/issues/issues/4", want: ref{Number: 4, Repo: "https://gitea.com/alice/issues"}},
		{raw: "https://gitea.com/alice/project/pulls/7", kind: issueRef, want: ref{Number: 7, Repo: "https://gitea.com/alice/project"}},
		{raw: "https://gitea.com/alice/project/actions/runs/34", kind: runRef, want: ref{Number: 34, Repo: "https://gitea.com/alice/project"}},
		{raw: "https://gitea.com/alice/project/actions/runs/34/jobs/2", kind: runRef, want: ref{Number: 34, Job: 2, Repo: "https://gitea.com/alice/project"}},
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
	for _, branch := range []string{"feature/login", "bob:fix", "main"} {
		_, err := parseRef(branch, pullRef)
		require.ErrorIs(t, err, errNotRef, branch)
	}
}

// refApplication returns an application authenticated against a server listing the open pull
// requests #3 (feat/one in alice/project), #4 (feat/two), #17 (fix in bob/project) and two
// pull requests from branch dup.
func refApplication(t *testing.T, gitResults map[string]string) (*application, string) {
	t.Helper()
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		writeFirstPage(w, r, `[
			{"number":3,"head":{"ref":"feat/one","repo":{"full_name":"alice/project"}}},
			{"number":4,"head":{"ref":"feat/two","repo":{"full_name":"alice/project"}}},
			{"number":17,"head":{"ref":"fix","repo":{"full_name":"bob/project"}}},
			{"number":20,"head":{"ref":"dup","repo":{"full_name":"alice/project"}}},
			{"number":21,"head":{"ref":"dup","repo":{"full_name":"carol/project"}}}
		]`)
	}))
	t.Cleanup(server.Close)
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	results := map[string]string{
		"remote get-url origin":              server.URL + "/alice/project.git",
		"remote get-url pulls/bob/project":   server.URL + "/bob/project.git",
		"remote get-url fork":                server.URL + "/bob/project.git",
		"remote get-url pulls/carol/project": server.URL + "/carol/project.git",
	}
	for key, value := range gitResults {
		results[key] = value
	}
	deps.Git = &fakeGit{results: results}
	return &application{deps: deps}, server.URL
}

func commandContext() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd
}

func TestPullSelectorDefaultsToTheCheckedOutBranch(t *testing.T) {
	const head = "symbolic-ref --short HEAD"
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
		{"recorded for another repository", branch("pr/9", map[string]string{"gitea-cli-pr": "9", "gitea-cli-base-remote": "fork", "remote": "fork", "merge": "refs/heads/fix"}), 17, ""},
		{"upstream branch and owner", branch("local", map[string]string{"remote": "origin", "merge": "refs/heads/feat/one"}), 3, ""},
		{"upstream owner tells forks apart", branch("dup", map[string]string{"remote": "pulls/carol/project", "merge": "refs/heads/dup"}), 21, ""},
		{"local branch name", branch("feat/two", nil), 4, ""},
		{"pr/N branch", branch("pr/99", nil), 99, ""},
		{"no pull request", branch("main", nil), 0, `no open pull request for branch "main" in alice/project; pass a number, URL or branch`},
		{"ambiguous branch", branch("dup", nil), 0, `branch "dup" has several open pull requests in alice/project (#20, #21); pass a number`},
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
	_, _, err := a.pullSelector(commandContext(), nil)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "pass a pull request number, URL or branch with --repo")
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

	resolved, numbers, err := a.resolveRefs(commandContext(), []string{"bob/tool#1", "#2", serverURL + "/bob/tool/issues/3"}, issueRef)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, numbers)
	require.Equal(t, "bob/tool", resolved.Repo)

	_, _, err = a.resolveRefs(commandContext(), []string{"bob/tool#1", "alice/project#2"}, issueRef)
	require.EqualError(t, err, "bob/tool#1 and alice/project#2 are in different repositories; run one command per repository")
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
