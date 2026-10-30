// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestAliasCompletionUsesBuiltInCommandsAndFlags(t *testing.T) {
	deps, out, _ := testDeps(t)
	for _, definition := range [][]string{{"pulls", "pr"}, {"listing", "pulls list"}, {"inspect", "pr view $1"}} {
		require.NoError(t, Execute(context.Background(), deps, append([]string{"alias", "set"}, definition...)))
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "pulls", "ch"}, "checkout"},
		{[]string{"__complete", "listing"}, "listing"},
		{[]string{"__complete", "listing", "--auth"}, "--author"},
		{[]string{"__completeNoDesc", "--no-input", "listing", "--ba"}, "--base"},
		{[]string{"__complete", "inspect", "7", "--j"}, "--json"},
		{[]string{"__complete", "listing", "--json", "nu"}, "number"},
	} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, tc.args))
		require.Contains(t, out.String(), tc.want)
	}
}

// complete runs `gt __complete args...` and returns the candidates and the directive.
func complete(t *testing.T, deps Dependencies, out *bytes.Buffer, args ...string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append([]string{cobra.ShellCompRequestCmd}, args...)))
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	directive, err := strconv.Atoi(strings.TrimPrefix(lines[len(lines)-1], ":"))
	require.NoError(t, err, out.String())
	return lines[:len(lines)-1], cobra.ShellCompDirective(directive)
}

func TestPullCompletionStopsAfterTheLastPositionalArgument(t *testing.T) {
	requests := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`[{"id":9,"number":9,"title":"More"}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, args := range []string{"pr view 5 ", "pr review list 5 ", "pr review view 5 9 ", "pr review submit 5 9 ", "pr review reply 5 9 "} {
		items, directive := complete(t, deps, out, append(strings.Fields(args), "-R", "owner/repo", "")...)
		require.Empty(t, items, args)
		require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive, args)
	}
	require.Zero(t, requests, "no argument is left to complete")
}

func TestEnumFlagsShareValidationAndCompletion(t *testing.T) {
	deps, out, _ := testDeps(t)
	var state string
	var events []string
	run := func(args ...string) error {
		root := NewRoot(deps)
		probe := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
		enumFlag(probe, &state, "state", "s", "open", "Filter by state", "open", "closed", "all")
		probe.Flags().StringSliceVar(&events, "events", nil, "Events to deliver")
		enumValues(probe, "events", "push", "pull_request", "release")
		root.AddCommand(probe)
		root.SetArgs(args)
		out.Reset()
		return root.ExecuteContext(context.Background())
	}
	require.NoError(t, run("probe", "-s", "CLOSED", "--events", "Push,release"))
	require.Equal(t, "closed", state)
	require.Equal(t, []string{"push", "release"}, events)
	err := run("probe", "--state", "merged")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "--state must be open, closed, or all")
	require.EqualError(t, run("probe", "--events", "push,nope"), "--events must be push, pull_request, or release")
	require.NoError(t, run("__complete", "probe", "--state", "c"))
	require.Equal(t, "closed\n:4\n", out.String())
	require.NoError(t, run("__complete", "probe", "--events", "push,"))
	require.Equal(t, "push,pull_request\npush,release\n:6\n", out.String())
	probe := &cobra.Command{Use: "probe"}
	enumFlag(probe, &state, "state", "s", "open", "Filter by state", "open", "closed", "all")
	require.Equal(t, "Filter by state {open|closed|all}", probe.Flag("state").Usage)
	require.Equal(t, "s", probe.Flag("state").Shorthand)
}

func TestJSONFieldCompletion(t *testing.T) {
	deps, out, _ := testDeps(t)
	values, directive := complete(t, deps, out, "issue", "list", "--json", "")
	require.Equal(t, jsonFields[services.Issue](), values)
	require.Equal(t, cobra.ShellCompDirectiveNoSpace|cobra.ShellCompDirectiveNoFileComp, directive)
	for args, want := range map[string][]string{
		"issue list --json number,ti": {"number,title"},
		"issue list --json=num":       {"number"},
		"issue list --csv number,ti":  {"number,title"},
		"issue list --json=state,ti":  {"state,title"},
		"pr view --json head_r":       {"head_remote_deleted", "head_repo"},
		"version --json ":             {},
	} {
		values, _ := complete(t, deps, out, strings.Split(args, " ")...)
		require.Equal(t, want, values, args)
	}
	values, _ = complete(t, deps, out, "issue", "list", "--json", "number,")
	require.Contains(t, values, "number,title")
	require.NotContains(t, values, "number,number")
}

func TestCompletionOffersPathsOnlyWherePathsAreExpected(t *testing.T) {
	deps, out, _ := testDeps(t)
	for args, want := range map[string]cobra.ShellCompDirective{
		"browse ":                        cobra.ShellCompDirectiveDefault,
		"browse docs ":                   cobra.ShellCompDirectiveNoFileComp,
		"issue view ":                    cobra.ShellCompDirectiveNoFileComp,
		"issue view 5 ":                  cobra.ShellCompDirectiveNoFileComp,
		"issue list --state ":            cobra.ShellCompDirectiveNoFileComp,
		"issue create --body-file ":      cobra.ShellCompDirectiveDefault,
		"issue create --attachment ":     cobra.ShellCompDirectiveDefault,
		"label export --output ":         cobra.ShellCompDirectiveDefault,
		"run download 5 --output ":       cobra.ShellCompDirectiveDefault,
		"config set ssh_key ":            cobra.ShellCompDirectiveDefault,
		"api --input ":                   cobra.ShellCompDirectiveDefault,
		"auth login --ssh-key ":          cobra.ShellCompDirectiveDefault,
		"ssh-key create ":                cobra.ShellCompDirectiveDefault,
		"release asset upload ":          cobra.ShellCompDirectiveNoFileComp,
		"release asset upload v1 ":       cobra.ShellCompDirectiveDefault,
		"release asset upload v1 a.txt ": cobra.ShellCompDirectiveDefault,
		"repo clone alice/project ":      cobra.ShellCompDirectiveFilterDirs,
		"repo clone alice/project dir ":  cobra.ShellCompDirectiveNoFileComp,
		"issue attachment upload 5 ":     cobra.ShellCompDirectiveDefault,
		"release create v1 ":             cobra.ShellCompDirectiveDefault,
		"--cwd ":                         cobra.ShellCompDirectiveFilterDirs,
		"--jq ":                          cobra.ShellCompDirectiveNoFileComp,
	} {
		_, directive := complete(t, deps, out, strings.Split(args, " ")...)
		require.Equal(t, want, directive, args)
	}
	// No other argument or flag value falls back to file names.
	walkCommands(NewRoot(deps), func(cmd *cobra.Command) {
		if !cmd.HasParent() || !cmd.IsAvailableCommand() {
			return
		}
		path := strings.Fields(cmd.CommandPath())[1:]
		_, directive := complete(t, deps, out, append(path, "")...)
		takesFile := slices.Contains([]string{"gt browse", "gt label import", "gt ssh-key create", "gt repo deploy-key add"}, cmd.CommandPath())
		require.Equal(t, takesFile, directive == cobra.ShellCompDirectiveDefault, "%s <TAB>: %d", cmd.CommandPath(), directive)
		for _, flags := range []*pflag.FlagSet{cmd.LocalNonPersistentFlags(), cmd.PersistentFlags()} {
			flags.VisitAll(func(flag *pflag.Flag) {
				if flag.NoOptDefVal != "" || flag.Hidden {
					return
				}
				_, directive := complete(t, deps, out, append(path, "--"+flag.Name, "")...)
				path := strings.HasSuffix(flag.Name, "-file") || slices.Contains(pathFlags, flag.Name)
				require.Equal(t, path, directive == cobra.ShellCompDirectiveDefault, "%s --%s <TAB>: %d", cmd.CommandPath(), flag.Name, directive)
			})
		}
	})
}

func TestOfflineCompletions(t *testing.T) {
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{
		`config --get-regexp ^remote\..*\.url$`:                "remote.origin.url https://gitea.example.com/alice/project.git\nremote.my.fork.url git@gitea.example.com:bob/project.git\nremote.ci.url https://bot:s3cret@gitea.example.com/alice/project.git?token=x\n",
		"for-each-ref --format=%(refname:lstrip=2) refs/heads": "main\nfeature/login\n",
	}}
	configureAccounts(t, deps, "https://gitea.example.com", "alice", "alice", "bob")
	for args, want := range map[string][]string{
		"--host ":   {"https://gitea.example.com\talice, bob"},
		"--user ":   {"alice\thttps://gitea.example.com (active)", "bob\thttps://gitea.example.com"},
		"--remote ": {"origin\thttps://gitea.example.com/alice/project.git", "my.fork\tgit@gitea.example.com:bob/project.git", "ci\thttps://gitea.example.com/alice/project.git"},
	} {
		values, directive := complete(t, deps, out, strings.Split(args, " ")...)
		require.Equal(t, want, values, args)
		require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive, args)
	}
	a := &application{deps: deps}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	for _, tc := range []struct {
		complete cobra.CompletionFunc
		args     []string
		prefix   string
		want     []string
	}{
		{a.completeConfig, []string{"default_host"}, "", []string{"https://gitea.example.com\talice, bob"}},
		{a.completeConfig, []string{"color", "auto"}, "", []string{}},
		{a.completeConfig, nil, "v", []string{}},
		{a.completeConfig, nil, "us", []string{}},
		{a.completeLocalBranches, nil, "f", []string{"feature/login\tlocal branch"}},
	} {
		values, _ := tc.complete(cmd, tc.args, tc.prefix)
		require.Equal(t, tc.want, append([]string{}, values...), "%v %q", tc.args, tc.prefix)
	}
}

func TestConfigGetCompletesOnlyTheKey(t *testing.T) {
	deps, out, _ := testDeps(t)
	values, _ := complete(t, deps, out, "config", "get", "v")
	require.Equal(t, []string{"version"}, values)
	values, _ = complete(t, deps, out, "config", "get", "color", "")
	require.Empty(t, values)
}

func TestLocalBranchCompletionIgnoresATagWithTheSameName(t *testing.T) {
	gittest.Isolate(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "--allow-empty", "--message=x"},
		{"branch", "feature"},
		{"tag", "feature"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	deps, _, _ := testDeps(t)
	deps.Git = git.System{}
	a := &application{deps: deps}
	a.options.cwd = dir
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	values, _ := a.completeLocalBranches(cmd, nil, "f")
	require.Equal(t, []string{"feature\tlocal branch"}, append([]string{}, values...))
}

func TestRepositoryCompletionUsesRemotesAndInstance(t *testing.T) {
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/user/repos", "/api/v1/users/alice/repos":
			_, _ = w.Write([]byte(`[{"full_name":"alice/project","description":"Demo"},{"full_name":"alice/other"},{"full_name":"alice/x\u001b[2J"}]`))
		case "/api/v1/users/alice":
			_, _ = w.Write([]byte(`{"id":1,"login":"alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{
		`config --get-regexp ^remote\..*\.url$`: "remote.origin.url " + server.URL + "/alice/project.git\nremote.github.url https://github.com/cli/cli.git\n",
	}}
	configureAccounts(t, deps, server.URL, "alice", "alice")
	values, directive := complete(t, deps, out, "-R", "")
	require.Equal(t, []string{"alice/project\tremote origin", "alice/other"}, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Equal(t, []string{"token alice-token"}, tokens)
	// An owner before the slash lists that owner's repositories.
	values, _ = complete(t, deps, out, "issue", "list", "-R", "alice/o")
	require.Equal(t, []string{"alice/other"}, values)
	require.Contains(t, tokens, "token alice-token")
}

func TestLabelCompletionDiscoversTheRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/labels":
			_, _ = w.Write([]byte(`[{"name":"bug","description":"Broken"},{"name":"docs"}]`))
		case "/api/v1/users/alice":
			_, _ = w.Write([]byte(`{"id":1,"login":"alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git",
	}}
	configureAccounts(t, deps, server.URL, "alice", "alice")
	values, _ := complete(t, deps, out, "label", "delete", "")
	require.Equal(t, []string{"bug\tBroken", "docs"}, values)
}

func TestJobCompletionUsesTheRepositoryOfTheRunURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/bob/tool/actions/runs/44/jobs":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`{"jobs":[{"id":9,"name":"Build","status":"success"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
			}
		case "/api/v1/users/alice":
			_, _ = w.Write([]byte(`{"id":1,"login":"alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git"}}
	configureAccounts(t, deps, server.URL, "alice", "alice")
	values, _ := complete(t, deps, out, "run", "logs", server.URL+"/bob/tool/actions/runs/44", "--job", "")
	require.Equal(t, []string{"9\tBuild · success"}, values)
}

func TestActionValueCompletionNeedsNoRepositoryForAnOrganization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/actions/variables":
			_, _ = w.Write([]byte(`[{"name":"MODE","description":"Build mode"}]`))
		case "/api/v1/users/alice":
			_, _ = w.Write([]byte(`{"id":1,"login":"alice"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	values, _ := complete(t, deps, out, "variable", "delete", "--org", "acme", "")
	require.Equal(t, []string{"MODE\tBuild mode"}, values)
}

func TestAPICompletionNeverRefreshesOrFails(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	now := time.Now()
	deps.Now = func() time.Time { return now }
	require.NoError(t, testManager(t, deps).Register(server.URL, "alice", settings.StoreFile, settings.AuthOAuth, auth.HostOptions{}, auth.Credential{AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: now.Add(-time.Minute)}))
	values, directive := complete(t, deps, out, "-R", "")
	require.Empty(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Zero(t, requests, "an expired OAuth token must not be refreshed while completing")
	credential, err := deps.Credentials.Load(server.URL, "alice", settings.StoreFile)
	require.NoError(t, err)
	require.Equal(t, "refresh", credential.RefreshToken)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	values, directive = complete(t, deps, out, "-R", "")
	require.Empty(t, values)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	require.Equal(t, 1, requests, "completion requests are not retried")
}

func TestInstanceCompletions(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls":
			require.Equal(t, "open", r.URL.Query().Get("state"))
			_, _ = w.Write([]byte(`[{"number":13,"title":"Add login support"},{"number":2,"title":"Fix\u001b]0;pwned\u0007 docs"},{"number":17,"title":"Improve greeting"}]`))
		case "/api/v1/repos/alice/project/labels":
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug","description":"Something is broken"},{"id":2,"name":"docs"},{"id":3,"name":"bad\u001b[31m"}]`))
		case "/api/v1/orgs/alice/labels":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/repos/alice/project/assignees":
			_, _ = w.Write([]byte(`[{"login":"alice","full_name":"Alice"},{"login":"bob"}]`))
		case "/api/v1/user/orgs":
			_, _ = w.Write([]byte(`[{"id":4,"username":"acme","full_name":"Acme Inc"}]`))
		case "/api/v1/orgs/acme/teams":
			_, _ = w.Write([]byte(`[{"id":2,"name":"developers","description":"Developers"}]`))
		case "/api/v1/repos/alice/project/issues":
			require.Equal(t, "issues", r.URL.Query().Get("type"))
			_, _ = w.Write([]byte(`[{"number":4,"title":"Crash when config is missing"}]`))
		case "/api/v1/repos/alice/project/milestones":
			_, _ = w.Write([]byte(`[{"id":1,"title":"v1.0","description":"First release"},{"id":25,"title":"23"}]`))
		case "/api/v1/repos/alice/project/branches":
			_, _ = w.Write([]byte(`[{"name":"main","protected":true},{"name":"docs/guide"}]`))
		case "/api/v1/repos/alice/project/tags":
			_, _ = w.Write([]byte(`[{"name":"v0.1.0"}]`))
		case "/api/v1/repos/alice/project/releases":
			_, _ = w.Write([]byte(`[{"id":1,"tag_name":"v0.1.0","name":"First release"}]`))
		case "/api/v1/repos/alice/project/actions/workflows":
			_, _ = w.Write([]byte(`{"total_count":1,"workflows":[{"id":"ci.yml","name":"CI"}]}`))
		case "/api/v1/repos/alice/project/actions/runs":
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":47,"status":"completed","conclusion":"failure","display_title":"Add workflows"}]}`))
		case "/api/v1/repos/alice/project/actions/secrets":
			_, _ = w.Write([]byte(`[{"name":"DEPLOY_TOKEN","description":"Deploy key"}]`))
		case "/api/v1/orgs/acme/actions/variables":
			_, _ = w.Write([]byte(`[{"name":"REGION","data":"eu"}]`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	a := &application{deps: deps}
	a.options.repo = "alice/project"
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	for _, tc := range []struct {
		name      string
		complete  cobra.CompletionFunc
		args      []string
		prefix    string
		want      []string
		directive cobra.ShellCompDirective
	}{
		{"pulls by prefix", a.completePulls("open"), nil, "1", []string{"13\tAdd login support", "17\tImprove greeting"}, cobra.ShellCompDirectiveNoFileComp},
		{"pulls skip given arguments and sanitize titles", withoutGiven(a.completePulls("open")), []string{"13", "17"}, "", []string{"2\tFix]0;pwned docs"}, cobra.ShellCompDirectiveNoFileComp},
		{"flag values may equal a positional argument", a.completePulls("open"), []string{"13"}, "13", []string{"13\tAdd login support"}, cobra.ShellCompDirectiveNoFileComp},
		{"labels are comma-separated", a.completeLabels(), nil, "bug,", []string{"bug,docs"}, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp},
		{"labels with descriptions", a.completeLabels(), nil, "b", []string{"bug\tSomething is broken"}, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp},
		{"users", a.completeUsers(), nil, "", []string{"alice\tAlice", "bob"}, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp},
		{"teams start with organizations", a.completeTeams(), nil, "", []string{"acme/\tAcme Inc"}, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp},
		{"teams of an organization", a.completeTeams(), nil, "acme/d", []string{"acme/developers\tDevelopers (ID 2)"}, cobra.ShellCompDirectiveNoFileComp},
		{"issues", a.completeIssues("open"), nil, "", []string{"4\tCrash when config is missing"}, cobra.ShellCompDirectiveNoFileComp},
		{"milestones", a.completeMilestones(), nil, "", []string{"v1.0\tFirst release", "25\t23"}, cobra.ShellCompDirectiveNoFileComp},
		{"branches", a.completeBranches(), nil, "", []string{"main\tprotected", "docs/guide"}, cobra.ShellCompDirectiveNoFileComp},
		{"tags", a.completeTags(), nil, "v", []string{"v0.1.0"}, cobra.ShellCompDirectiveNoFileComp},
		{"releases", a.completeReleases(), nil, "", []string{"v0.1.0\tFirst release"}, cobra.ShellCompDirectiveNoFileComp},
		{"workflows", a.completeWorkflows(), nil, "", []string{"ci.yml\tCI"}, cobra.ShellCompDirectiveNoFileComp},
		{"runs", a.completeRuns(), nil, "4", []string{"47\tcompleted · failure · Add workflows"}, cobra.ShellCompDirectiveNoFileComp},
		{"repository secrets", a.completeSecrets(func() services.ActionScope { return services.ActionScope{} }), nil, "", []string{"DEPLOY_TOKEN\tDeploy key"}, cobra.ShellCompDirectiveNoFileComp},
		{"organization variables", a.completeVariables(func() services.ActionScope { return services.ActionScope{Organization: "acme"} }), nil, "", []string{"REGION"}, cobra.ShellCompDirectiveNoFileComp},
	} {
		values, directive := tc.complete(cmd, tc.args, tc.prefix)
		require.Equal(t, tc.want, values, tc.name)
		require.Equal(t, tc.directive, directive, tc.name)
	}
}

func TestCompleteArgumentBranchesUsesTheNamedRepository(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/bob/tool/branches", r.URL.Path)
		_, _ = w.Write([]byte(`[{"name":"develop"}]`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	a := &application{deps: deps}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	values, _ := a.completeArgumentBranches()(cmd, []string{"bob/tool"}, "")
	require.Equal(t, []string{"develop"}, values)
}

func TestCompletionInstallHelp(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"completion", "bash", "--help"}))
	require.Contains(t, out.String(), "$ eval \"$(gt completion bash)\"\n")
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"completion", shell, "--help"}))
		require.Contains(t, out.String(), "USAGE\n  gt completion "+shell+" [flags]\n", shell)
		require.NotContains(t, out.String(), "####", shell)
		require.NotContains(t, out.String(), "\t", shell)
	}
}

func TestCompletionTargetAcceptsABareTokenHost(t *testing.T) {
	deps, _, _ := testDeps(t)
	instance := "http://127.0.0.1:3999"
	require.NoError(t, testManager(t, deps).Register(instance, "alice", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "stored-token"}))
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": "127.0.0.1:3999", "GITEA_TOKEN": "environment-token"})
	a := &application{deps: deps}
	resolved, err := a.completionTarget(context.Background(), "", false)
	require.NoError(t, err)
	require.Equal(t, instance, resolved.Host)
}

func TestAccountCompletionUsesInstanceOfRepository(t *testing.T) {
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": "https://a.example.org/alice/project.git"}}
	configureHost(t, deps, "https://a.example.org", settings.Host{User: "alice", Users: tokenAccounts(settings.StoreKeyring, "alice", "adam")})
	configureHost(t, deps, "https://b.example.org", settings.Host{User: "bob", Users: tokenAccounts(settings.StoreKeyring, "bob")})
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.DefaultHost = "https://b.example.org"; return nil }))
	for _, command := range []string{"switch", "logout"} {
		values, _ := complete(t, deps, out, "auth", command, "")
		require.Equal(t, []string{"adam\thttps://a.example.org", "alice\thttps://a.example.org (active)"}, values, command)
	}
}

func TestActionValueCompletionFollowsTheScope(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/actions/variables":
			_, _ = w.Write([]byte(`[{"name":"REGION","data":"eu"}]`))
		case "/api/v1/user/actions/variables":
			_, _ = w.Write([]byte(`[{"name":"HOME_REGION","data":"eu"}]`))
		case "/api/v1/repos/alice/project/actions/variables":
			_, _ = w.Write([]byte(`[{"name":"REPO_REGION","data":"eu"}]`))
		case "/api/v1/repos/alice/project/actions/secrets":
			_, _ = w.Write([]byte(`[{"name":"REPO_TOKEN"}]`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	a := &application{deps: deps}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	for name, tc := range map[string]struct {
		scope services.ActionScope
		want  []string
	}{
		"organization": {services.ActionScope{Organization: "acme"}, []string{"REGION"}},
		"account":      {services.ActionScope{Account: true}, []string{"HOME_REGION"}},
		"repository":   {services.ActionScope{}, nil},
	} {
		values, _ := a.completeVariables(func() services.ActionScope { return tc.scope })(cmd, nil, "")
		require.ElementsMatch(t, tc.want, values, name)
	}
	deps.Git = doctorGit(server.URL)
	values, _ := complete(t, deps, out, "variable", "get", "")
	require.Equal(t, []string{"REPO_REGION"}, values)
	values, _ = complete(t, deps, out, "secret", "delete", "")
	require.Equal(t, []string{"REPO_TOKEN"}, values)
}

func TestMilestoneCompletionOffersTheMilestonesTheCommandAccepts(t *testing.T) {
	var states []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/milestones" {
			states = append(states, r.URL.Query().Get("state"))
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	a := &application{deps: deps}
	a.options.repo = "alice/project"
	root := a.milestoneCommand()
	for _, path := range [][]string{{"reopen"}, {"close"}, {"view"}, {"issue", "list"}} {
		cmd, _, err := root.Find(path)
		require.NoError(t, err)
		cmd.SetContext(context.Background())
		cmd.ValidArgsFunction(cmd, nil, "")
	}
	require.Equal(t, []string{"closed", "open", "all", "all"}, states)
}
