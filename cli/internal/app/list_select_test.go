// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

// labelPrompter answers selections with the choice whose label starts with the next answer.
type labelPrompter struct {
	scriptedPrompter
	answers []string
	titles  []string
	offered [][]string
}

func (p *labelPrompter) Choose(_ context.Context, title string, choices []terminal.Choice, _ string) (string, error) {
	p.titles = append(p.titles, title)
	labels := make([]string, len(choices))
	for i, choice := range choices {
		labels[i] = choice.Label
	}
	p.offered = append(p.offered, labels)
	if len(p.answers) == 0 {
		return "", errors.New("unexpected selection: " + title)
	}
	answer := p.answers[0]
	p.answers = p.answers[1:]
	for _, choice := range choices {
		if strings.HasPrefix(choice.Label, answer) {
			return choice.Value, nil
		}
	}
	return "", errors.New("no choice " + answer)
}

// selectServer answers the requests of --select and records the writes.
func selectServer(t *testing.T, issues string) (*httptest.Server, func() []string) {
	t.Helper()
	const pull = `"title":"Fix","head":{"ref":"fix","label":"fix","sha":"abc","repo":{"full_name":"alice/project"}},"base":{"ref":"main","repo":{"full_name":"alice/project"}}`
	var mu sync.Mutex
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			mu.Lock()
			writes = append(writes, r.Method+" "+r.URL.Path)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"number":5,"title":"Bug","state":"closed"}`))
			return
		}
		switch path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project"); path {
		case "":
			_, _ = w.Write([]byte(`{"id":1,"full_name":"alice/project","name":"project","owner":{"login":"alice"},"default_branch":"main","has_issues":true}`))
		case "/issues":
			_, _ = w.Write([]byte(issues))
		case "/issues/5":
			_, _ = w.Write([]byte(`{"number":5,"title":"Bug","state":"open","body":"Details of the bug","user":{"login":"bob"}}`))
		case "/pulls":
			_, _ = w.Write([]byte(`[{"number":7,"state":"open",` + pull + `},{"number":8,"state":"closed","merged":true,` + pull + `},{"number":9,"state":"closed",` + pull + `}]`))
		case "/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"state":"open",` + pull + `}`))
		case "/pulls/8":
			_, _ = w.Write([]byte(`{"number":8,"state":"closed","merged":true,` + pull + `}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return writes
	}
}

func selectDeps(t *testing.T, serverURL string, answers ...string) (Dependencies, *labelPrompter) {
	t.Helper()
	deps, _, _ := serverDeps(t, serverURL)
	prompter := &labelPrompter{answers: answers}
	deps.Prompter = prompter
	deps.Streams.InTTY, deps.Streams.ErrTTY, deps.Streams.OutTTY = true, true, true
	deps.Getenv = serverEnv(serverURL, map[string]string{"NO_COLOR": "1"})
	return deps, prompter
}

func TestListSelectViewsTheItemAndRunsAnAction(t *testing.T) {
	server, writes := selectServer(t, `[{"number":5,"title":"Bug","state":"open"},{"number":6,"title":"Other","state":"open"}]`)
	deps, prompter := selectDeps(t, server.URL, "#5 Bug", "Close")
	out, ok := deps.Streams.Out.(interface{ String() string })
	require.True(t, ok)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "--select", "-R", "alice/project"}))
	require.Equal(t, []string{"Select an issue (/ to filter)", "What next?"}, prompter.titles)
	require.Equal(t, []string{"#5 Bug", "#6 Other"}, prompter.offered[0])
	require.Equal(t, []string{"Comment", "Close", "Open in the browser", "Done"}, prompter.offered[1])
	require.Contains(t, out.String(), "Details of the bug")
	require.NotContains(t, out.String(), "#6", "the table is replaced by the selection")
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project/issues/5"}, writes())
}

func TestListSelectDoneChangesNothing(t *testing.T) {
	server, writes := selectServer(t, `[{"number":5,"title":"Bug","state":"open"}]`)
	deps, _ := selectDeps(t, server.URL, "#5", "Done")
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "--select", "-R", "alice/project"}))
	require.Empty(t, writes())
}

func TestListSelectWithNothingListedAsksNothing(t *testing.T) {
	server, _ := selectServer(t, `[]`)
	deps, prompter := selectDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "--select", "-R", "alice/project"}))
	require.Empty(t, prompter.titles)
	diagnostics, ok := deps.Streams.ErrOut.(interface{ String() string })
	require.True(t, ok)
	require.Contains(t, diagnostics.String(), "no open issues")
}

func TestListSelectOffersWhatTheStateOfAPullRequestAllows(t *testing.T) {
	server, _ := selectServer(t, `[]`)
	deps, prompter := selectDeps(t, server.URL, "#7", "Done")
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "--select", "--state", "all", "-R", "alice/project"}))
	require.Equal(t, "Select a pull request (/ to filter)", prompter.titles[0])
	require.Equal(t, []string{"Check out", "Show the diff", "Review", "Merge", "Comment", "Close", "Open in the browser", "Done"}, prompter.offered[1])

	deps, prompter = selectDeps(t, server.URL, "#8", "Done")
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "--select", "--state", "all", "-R", "alice/project"}))
	require.Equal(t, []string{"Check out", "Show the diff", "Comment", "Open in the browser", "Done"}, prompter.offered[1])
}

// TestListSelectRunsRealCommands keeps the commands --select runs in step with the command tree.
func TestListSelectRunsRealCommands(t *testing.T) {
	var commands [][]string
	collect := func(view []string, actions []selectAction) {
		commands = append(commands, view)
		for _, action := range actions {
			commands = append(commands, action.args)
		}
	}
	issues := issueSelection("alice/project", "all")
	for _, item := range []services.Issue{{Number: 1, State: "open"}, {Number: 1, State: "closed"}, {Number: 1, State: "open", Kind: services.KindPull}, {Number: 1, State: "merged", Kind: services.KindPull}} {
		collect(issues.view(item), issues.actions(item))
	}
	pulls := pullSelection("alice/project")
	for _, item := range []services.Pull{{Number: 1, State: "open"}, {Number: 1, State: "closed"}, {Number: 1, State: "closed", Merged: true}} {
		collect(pulls.view(item), pulls.actions(item))
	}
	releases := releaseSelection("alice/project")
	collect(releases.view(services.Release{Tag: "v1"}), releases.actions(services.Release{Tag: "v1"}))
	repos := repoSelection()
	collect(repos.view(services.Repository{FullName: "alice/project"}), repos.actions(services.Repository{FullName: "alice/project"}))

	for _, args := range commands {
		deps, _, _ := testDeps(t)
		root := NewRoot(deps)
		cmd, rest, err := root.Find(args)
		require.NoError(t, err, args)
		require.NotEqual(t, root, cmd, args)
		require.NoError(t, cmd.ParseFlags(append(rest, "--host", "https://gitea.test")), args)
		require.NoError(t, cmd.ValidateArgs(cmd.Flags().Args()), args)
	}
}
