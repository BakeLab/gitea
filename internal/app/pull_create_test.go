// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func publishCreateEnv(t *testing.T, creates *int) (Dependencies, *fakeGit, string) {
	t.Helper()
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		*creates++
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "feature", payload["head"])
		_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
	}))
	t.Cleanup(server.Close)
	deps, _, _ := serverDeps(t, server.URL)
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "--set-upstream", "--quiet", "--", "origin", "refs/heads/feature")
	runner := &fakeGit{results: map[string]string{
		"rev-parse --is-inside-work-tree":                "true",
		"rev-parse --verify refs/heads/feature^{commit}": "localsha",
		"remote":                             "origin",
		"remote get-url origin":              server.URL + "/alice/project.git",
		"config --get branch.feature.remote": "origin",
		pushKey:                              "",
	}}
	deps.Git = runner
	return deps, runner, pushKey
}

func TestPullCreateAgitPushesToMatchingRemoteWithoutRemoteFlag(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main","permissions":{"push":true}}`))
		case r.URL.Path == "/api/v1/orgs/alice", strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	pushKey := credentialCall(t, deps, server.URL, "alice", "push", "-o", "title=Title", "--", "gitea", "refs/heads/feature:refs/for/main/feature")
	runner := &fakeGit{results: map[string]string{
		"remote":                          "origin gitea",
		"remote get-url origin":           server.URL + "/bob/project.git",
		"remote get-url gitea":            server.URL + "/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		pushKey:                           "",
	}}
	deps.Git = runner
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.PullForm = &recordingPullForm{values: terminal.PullFormValues{
		IssueFormValues: terminal.IssueFormValues{Title: "Title"}, Agit: true, Base: "main", Head: "feature",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--title", "Title", "--head", "feature", "--base", "main", "--no-input"}))
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--interactive"}))
	pushes := 0
	for _, call := range runner.calls {
		if strings.Contains(call, " push ") {
			require.Equal(t, pushKey, call)
			pushes++
		}
	}
	require.Equal(t, 2, pushes)
	runner.results["remote"] = "one two"
	runner.results["remote get-url one"], runner.results["remote get-url two"] = server.URL+"/alice/project.git", server.URL+"/alice/project.git"
	err := Execute(context.Background(), deps, []string{"pr", "create", "--agit", "-R", "alice/project", "--title", "Title", "--head", "feature", "--base", "main", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "select --remote")
}

func TestPullCreateSkipsPushWhenHeadAlreadyPublished(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	runner.results["for-each-ref --format=%(objectname) refs/remotes/origin/feature"] = "localsha"
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"}))
	require.Equal(t, 1, creates)
	require.NotContains(t, runner.calls, pushKey)
	for _, call := range runner.calls {
		require.NotContains(t, call, "push --")
	}
}

func TestPullCreatePushesWithYesWhenHeadUnpublished(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input", "--yes"}))
	require.Equal(t, 1, creates)
	require.Contains(t, runner.calls, pushKey)
}

func TestPullCreateRequiresYesWhenHeadUnpublishedWithoutInput(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--yes")
	require.Zero(t, creates)
	require.NotContains(t, runner.calls, pushKey)
}

func TestPullCreateCanceledWhenPublishDeclined(t *testing.T) {
	creates := 0
	deps, runner, pushKey := publishCreateEnv(t, &creates)
	prompt := &scriptedPrompter{accept: false}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	require.Len(t, prompt.confirmations, 1)
	require.Zero(t, creates)
	require.NotContains(t, runner.calls, pushKey)
}

func TestPullCreateAllowMaintainerEditsDefaultAndOverride(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  bool
	}{
		{"default true", nil, true},
		{"explicit false", []string{"--allow-maintainer-edits=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				_, _ = w.Write([]byte(`{"number":7,"title":"PR"}`))
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			args := append([]string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number", "--no-input"}, tc.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.want, payload["allow_maintainer_edit"])
		})
	}
}

func TestPullCreateRequestsReviewsAndUploadsAttachments(t *testing.T) {
	var reviewPayload map[string]any
	var uploaded []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/alice":
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v1/orgs/alice/teams" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(`[{"id":2,"name":"core"}]`))
		case r.URL.Path == "/api/v1/orgs/alice/teams":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			_, _ = w.Write([]byte(`{"number":7,"title":"PR","html_url":"https://example.test/pulls/7"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/7/requested_reviewers":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reviewPayload))
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"PR","requested_reviewers":[{"login":"bob"},{"login":"carol"}],"requested_reviewers_teams":[{"name":"core"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/issues/7/assets":
			file, header, err := r.FormFile("attachment")
			require.NoError(t, err)
			_ = file.Close()
			uploaded = append(uploaded, header.Filename)
			if header.Filename == "broken.txt" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"id":1,"name":"` + header.Filename + `"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	for _, name := range []string{"log.txt", "broken.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	deps, out, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{
		"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--json=number,reviewers,reviewer_teams", "--no-input",
		"--reviewer", "bob,carol", "--reviewer-team", "core", "--attachment", filepath.Join(dir, "log.txt"), "--attachment", filepath.Join(dir, "broken.txt"),
	})
	require.ErrorContains(t, err, "pull request #7 was created")
	require.ErrorContains(t, err, "broken.txt")
	require.Equal(t, map[string]any{"reviewers": []any{"bob", "carol"}, "team_reviewers": []any{"core"}}, reviewPayload)
	require.Equal(t, []string{"log.txt", "broken.txt"}, uploaded)
	require.JSONEq(t, `{"number":7,"reviewers":["bob","carol"],"reviewer_teams":["core"]}`, out.String())
}

func TestPullCreateRejectsUnknownReviewerTeamBeforeCreating(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/alice":
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/api/v1/orgs/alice/teams":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--no-input", "--reviewer-team", "ghost"})
	require.ErrorContains(t, err, `team "ghost" is unavailable`)
}

func TestPullCreateDerivesTitleAndBodyFromSingleCommit(t *testing.T) {
	message := "Add login form\n\nExplains why."
	for _, tc := range []struct {
		name, title, body string
		results           map[string]string
		flags             []string
	}{
		{"single commit", "Add login form", "Explains why.", map[string]string{"rev-list --count origin/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, nil},
		{"base remote", "Add login form", "Explains why.", map[string]string{"rev-list --count upstream/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, nil},
		{"local base", "Add login form", "", map[string]string{"rev-list --count main..feature": "1", "log -1 --format=%s%n%n%b feature": "Add login form"}, nil},
		{"explicit body kept", "Add login form", "Mine", map[string]string{"rev-list --count origin/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, []string{"--body", "Mine"}},
		{"explicit title kept", "Mine", "", map[string]string{"rev-list --count origin/main..feature": "1", "log -1 --format=%s%n%n%b feature": message}, []string{"--title", "Mine"}},
		{"several commits", "Feature", "", map[string]string{"rev-list --count origin/main..feature": "2", "log -1 --format=%s%n%n%b feature": message}, nil},
		{"git failure", "Feature", "", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				_, _ = w.Write([]byte(`{"number":7}`))
			}))
			defer server.Close()
			deps, _, diagnostics := serverDeps(t, server.URL)
			deps.Git = &fakeGit{results: tc.results}
			args := append([]string{"pr", "create", "-R", "alice/project", "--head", "feature", "--base", "main", "--json=number", "--no-input"}, tc.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.title, payload["title"])
			require.Equal(t, tc.body, payload["body"])
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestPullCreateEditorSeedsPullRequestTemplate(t *testing.T) {
	var payload map[string]any
	var mu sync.Mutex
	var templateRequests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			require.Equal(t, "main", r.URL.Query().Get("ref"))
			mu.Lock()
			templateRequests = append(templateRequests, strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"))
			mu.Unlock()
			if r.URL.Path != "/api/v1/repos/alice/project/raw/.gitea/PULL_REQUEST_TEMPLATE.md" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte("## Checklist\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = w.Write([]byte(`{"number":7}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	editor, _ := newDraftEditor(t, "filled")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "feature", "--base", "main", "--editor", "--json=number", "--no-input"}))
	require.Equal(t, "## Checklist\n", editor.initial)
	require.ElementsMatch(t, []string{
		"PULL_REQUEST_TEMPLATE.md", "pull_request_template.md",
		".gitea/PULL_REQUEST_TEMPLATE.md", ".gitea/pull_request_template.md",
		".github/PULL_REQUEST_TEMPLATE.md", ".github/pull_request_template.md",
	}, templateRequests)
	require.Equal(t, "filled", payload["body"])
}

func TestPullCreateHelpListsDefaultsOnce(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "--help"}))
	require.Contains(t, out.String(), "(default true)")
	require.NotContains(t, out.String(), "(default true) (default true)")
}

func TestCompareRefEscapesBranchNames(t *testing.T) {
	require.Equal(t, "feature/login", compareRef("feature/login"))
	require.Equal(t, "fix%2312", compareRef("fix#12"))
	require.Equal(t, "bob:fix%25x", compareRef("bob:fix%x"))
}

func TestPullCreateRefusesHeadEqualToBaseBeforePushing(t *testing.T) {
	creates := 0
	deps, runner, _ := publishCreateEnv(t, &creates)
	err := Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--title", "PR", "--head", "main", "--base", "main", "--no-input", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `must be on a branch named differently than "main"`)
	require.Zero(t, creates)
	for _, call := range runner.calls {
		require.NotContains(t, call, "push")
	}
}

type recordingPullForm struct {
	calls   int
	values  terminal.PullFormValues
	choices terminal.PullFormChoices
}

func (f *recordingPullForm) CreatePull(_ context.Context, choices terminal.PullFormChoices) (terminal.PullFormValues, error) {
	f.calls++
	f.choices = choices
	return f.values, nil
}

func TestPullCreateImplicitInteractiveForm(t *testing.T) {
	deadline := time.Date(2028, 1, 15, 0, 0, 0, 0, time.UTC)
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/raw/.github/pull_request_template.md":
			require.Equal(t, "main", r.URL.Query().Get("ref"))
			_, _ = w.Write([]byte("## Summary\n"))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
			_, _ = w.Write([]byte(`{"number":9,"title":"Interactive","html_url":"https://example.org/alice/project/pulls/9","state":"open","base":{"ref":"main"},"head":{"ref":"feature"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	allow := true
	form := &recordingPullForm{values: terminal.PullFormValues{
		IssueFormValues: terminal.IssueFormValues{
			Title: "Interactive", Body: "Body text", Assignees: []string{"alice"}, Milestone: 4, Labels: []int64{2}, Deadline: &deadline,
		},
		Base: "main", Head: "feature", AllowMaintainerEdits: allow,
	}}
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.PullForm = form
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":                                       "feature",
		"rev-parse --is-inside-work-tree":                                 "true",
		"rev-parse --verify refs/heads/feature^{commit}":                  "localsha",
		"for-each-ref --format=%(objectname) refs/remotes/origin/feature": "localsha",
		"remote":                                "origin",
		"remote get-url origin":                 server.URL + "/alice/project.git",
		"config --get branch.feature.remote":    "origin",
		"rev-list --count origin/main..feature": "1",
		"log -1 --format=%s%n%n%b feature":      "Add login\n\nDetails",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--json=number,url", "--interactive"}))
	require.Equal(t, 1, form.calls)
	require.Equal(t, "Add login", form.choices.Title)
	require.Equal(t, "## Summary\n", form.choices.Body)
	require.NotNil(t, form.choices.EditBody)
	require.True(t, form.choices.CanPush)
	require.Equal(t, "main", form.choices.Base)
	require.Equal(t, "feature", form.choices.HeadBranch)
	require.Equal(t, []string{"alice", "bob"}, form.choices.Assignees)
	require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, form.choices.Milestones)
	require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, form.choices.Labels)
	require.Empty(t, prompt.confirmations, "submitting the form creates the pull request")
	require.Equal(t, "alice/project", form.choices.Repo)
	require.Equal(t, "Interactive", createPayload["title"])
	require.Equal(t, "Body text", createPayload["body"])
	require.Equal(t, "main", createPayload["base"])
	require.Equal(t, "feature", createPayload["head"])
	require.Equal(t, true, createPayload["allow_maintainer_edit"])
	require.Equal(t, []any{"alice"}, createPayload["assignees"])
	require.Equal(t, float64(4), createPayload["milestone"])
	require.Equal(t, []any{float64(2)}, createPayload["labels"])
	require.Equal(t, "2028-01-15T00:00:00Z", createPayload["due_date"])
	require.JSONEq(t, `{"number":9,"url":"https://example.org/alice/project/pulls/9"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestPullCreateDefaultsFromForkBranchWithoutTTY(t *testing.T) {
	for _, remote := range []string{"fork", "pulls/bob/project"} {
		t.Run(remote, func(t *testing.T) { testPullCreateDefaultsFromForkBranch(t, remote) })
	}
}

func testPullCreateDefaultsFromForkBranch(t *testing.T, remote string) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = w.Write([]byte(`{"number":1,"html_url":"https://example.org/pulls/1"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	form := &recordingPullForm{}
	deps, _, _ := serverDeps(t, server.URL)
	deps.PullForm = form
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":           "über-fix",
		"config --get branch.über-fix.remote": remote,
		"remote get-url " + remote:            server.URL + "/bob/project.git",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--no-input"}))
	require.Zero(t, form.calls)
	require.Equal(t, "Über Fix", payload["title"])
	require.Equal(t, "bob:über-fix", payload["head"])
	require.Equal(t, "main", payload["base"])
}
