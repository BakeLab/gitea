// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

const ciWorkflowFile = `name: CI
on:
  push:
  workflow_dispatch:
    inputs:
      target:
        description: Deploy target
        required: true
        default: staging
        type: choice
        options: [staging, production]
      dry_run:
        type: boolean
        default: true
      note:
        description: Free-form note
        required: true
jobs: {}
`

// contentsOf is the contents API response for a file.
func contentsOf(content string) string {
	data, _ := json.Marshal(map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
	return string(data)
}

func TestWorkflowListAndView(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows":                workflowsFixture,
		"GET actions/workflows/ci.yml/runs":    `{"total_count":1,"workflow_runs":[` + queuedRunFixture + `]}`,
		"GET contents/.gitea/workflows/ci.yml": contentsOf(ciWorkflowFile),
	})
	for _, tc := range []struct {
		name string
		tty  bool
		args []string
		want string
	}{
		{"list", true, []string{"list"}, "Showing 2 of 2 workflows in alice/project\n\nNAME     FILE          STATE\nCI       ci.yml        active\nNightly  nightly.yaml  disabled\n"},
		{"list piped", false, []string{"list"}, "CI\tci.yml\tactive\t.gitea/workflows/ci.yml\thttps://gitea.test/alice/project/src/branch/main/.gitea/workflows/ci.yml\n" +
			"Nightly\tnightly.yaml\tdisabled\t.gitea/workflows/nightly.yaml\t\n"},
		{"view by name", true, []string{"view", "ci"}, `CI ci.yml
active • .gitea/workflows/ci.yml

RECENT RUNS
STATUS  ID  WORKFLOW  TITLE          BRANCH  EVENT              ELAPSED  AGE
*       47  CI        Add workflows  main    workflow_dispatch           about 2 minutes ago

To see more runs: gt run list --workflow ci.yml -R alice/project
View this workflow on Gitea: https://gitea.test/alice/project/src/branch/main/.gitea/workflows/ci.yml
`},
		{"view the file", false, []string{"view", ".gitea/workflows/ci.yml", "--yaml"}, ciWorkflowFile},
		{"view as JSON", false, []string{"view", "CI", "--json", "id,state"}, `{"id":"ci.yml","state":"active"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := actionsDeps(t, server.URL, tc.tty)
			require.NoError(t, Execute(context.Background(), deps, append(append([]string{"workflow"}, tc.args...), "-R", "alice/project")))
			if strings.HasPrefix(tc.want, "{") {
				require.JSONEq(t, tc.want, out.String())
			} else {
				require.Equal(t, tc.want, out.String())
			}
		})
	}
	deps, _, _ := actionsDeps(t, server.URL, false)
	require.EqualError(t, Execute(context.Background(), deps, []string{"workflow", "view", "deploy", "-R", "alice/project"}), `workflow "deploy" not found in alice/project`)
}

func TestWorkflowRunChecksInputsBeforeDispatching(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/workflows":                workflowsFixture,
		"GET contents/.gitea/workflows/ci.yml": contentsOf(ciWorkflowFile),
	})
	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"-f", "note=x", "-f", "bogus=1"}, `unknown input "bogus" for CI (ci.yml); inputs: target, dry_run, note`},
		{[]string{"-f", "note=x", "-f", "target=moon"}, `invalid value "moon" for input target: use staging, production`},
		{[]string{"-f", "note=x", "-f", "dry_run=maybe"}, `invalid value "maybe" for input dry_run: use true or false`},
		{[]string{"-f", "target=production"}, "missing required input note for CI (ci.yml); pass -f note=<value>"},
		{nil, "missing required input note for CI (ci.yml); pass -f note=<value>"},
	} {
		*requests = nil
		deps, _, _ := actionsDeps(t, server.URL, false)
		err := Execute(context.Background(), deps, append([]string{"workflow", "run", "CI", "-R", "alice/project", "--ref", "main"}, tc.args...))
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", tc.args)
		require.EqualError(t, err, tc.err)
		require.Equal(t, []string{"GET actions/workflows", "GET contents/.gitea/workflows/ci.yml"}, *requests, "nothing is dispatched")
	}
	deps, _, _ := actionsDeps(t, server.URL, false)
	require.EqualError(t, Execute(context.Background(), deps, []string{"workflow", "run", "nightly", "-R", "alice/project"}),
		"workflow nightly.yaml is disabled; enable it with: gt workflow enable nightly.yaml -R alice/project")
}

func TestWorkflowRunExplainsWorkflowsComeFromTheDefaultBranch(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{"GET actions/workflows": workflowsFixture})
	deps, _, _ := actionsDeps(t, server.URL, false)
	err := Execute(context.Background(), deps, []string{"workflow", "run", "branch-only", "-R", "alice/project", "--ref", "feature"})
	require.EqualError(t, err, `workflow "branch-only" not found in alice/project; workflows are listed from the default branch and must exist there, even with --ref; see gt workflow list -R alice/project`)
	require.Equal(t, []string{"GET actions/workflows"}, *requests)
}

// dispatchingPrompter answers workflow inputs: selections with their first choice, confirmations
// with yes, and text by title.
type dispatchingPrompter struct {
	choosingPrompter
	asked []string
}

func (p *dispatchingPrompter) Ask(_ context.Context, title string, validate func(string) error) (string, error) {
	p.asked = append(p.asked, title)
	answer := p.inputs[title]
	return answer, validate(answer)
}

func (p *dispatchingPrompter) ConfirmDefault(_ context.Context, title string, defaultYes bool) (bool, error) {
	p.asked = append(p.asked, title)
	return !defaultYes, nil
}

func TestWorkflowRunDispatchesOnTheDefaultBranch(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET ":                                 `{"full_name":"alice/project","default_branch":"trunk"}`,
		"GET actions/workflows":                workflowsFixture,
		"GET contents/.gitea/workflows/ci.yml": contentsOf(ciWorkflowFile),
		"POST actions/workflows/ci.yml/dispatches": `{"workflow_run_id":51,"html_url":"https://gitea.test/alice/project/actions/runs/51"}`,
	})
	for _, tc := range []struct {
		name, out, err string
		args           []string
		prompter       *dispatchingPrompter
		payload        string
	}{
		{
			"flags", "https://gitea.test/alice/project/actions/runs/51\n", "✓ Dispatched CI (ci.yml) on trunk → run 51\n  Watch it: gt run watch 51 -R alice/project\n",
			[]string{"-f", "note=ship", "-f", "dry_run=FALSE"},
			nil, `{"ref":"trunk","inputs":{"dry_run":"false","note":"ship"}}`,
		},
		{
			"prompts", "https://gitea.test/alice/project/actions/runs/51\n", "✓ Dispatched CI (ci.yml) on trunk → run 51\n  Watch it: gt run watch 51 -R alice/project\n",
			nil, &dispatchingPrompter{choosingPrompter: choosingPrompter{scriptedPrompter: scriptedPrompter{inputs: map[string]string{"note — Free-form note": "ship"}}}},
			`{"ref":"trunk","inputs":{"dry_run":"false","note":"ship","target":"staging"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			*requests = nil
			deps, out, diagnostics := actionsDeps(t, server.URL, true)
			if tc.prompter != nil {
				deps.Prompter = tc.prompter
				deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			}
			require.NoError(t, Execute(context.Background(), deps, append([]string{"workflow", "run", "ci.yml", "-R", "alice/project"}, tc.args...)))
			require.Equal(t, tc.out, out.String())
			require.Equal(t, tc.err, diagnostics.String())
			require.Equal(t, []string{"GET actions/workflows", "GET ", "GET contents/.gitea/workflows/ci.yml", "POST actions/workflows/ci.yml/dispatches " + tc.payload}, *requests)
			if tc.prompter != nil {
				require.Equal(t, []string{"target — Deploy target"}, tc.prompter.titles)
				require.Equal(t, []string{"dry_run", "note — Free-form note"}, tc.prompter.asked)
			}
		})
	}
}

func TestWorkflowRunNeedsTheCheckedOutBranchOnTheServer(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET ":                    `{"full_name":"alice/project","default_branch":"main"}`,
		"GET actions/workflows":   workflowsFixture,
		"GET branches/local-only": `404 {"message":"branch not found"}`,
	})
	deps, _, _ := actionsDeps(t, server.URL, false)
	deps.Git = &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git",
		"symbolic-ref --quiet HEAD": "local-only", "config --get branch.local-only.remote": "origin",
	}}
	err := Execute(context.Background(), deps, []string{"workflow", "run", "ci.yml", "-f", "note=x"})
	require.EqualError(t, err, "branch local-only is not on alice/project; push it or pass --ref main")
	require.Equal(t, []string{"GET actions/workflows", "GET branches/local-only", "GET "}, *requests)
}

func TestWorkflowStateChangesNeedNoConfirmation(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/workflows":                     workflowsFixture,
		"PUT actions/workflows/nightly.yaml/enable": "204 ",
		"GET actions/workflows/nightly.yaml":        `{"id":"nightly.yaml","name":"Nightly","path":".gitea/workflows/nightly.yaml","state":"active"}`,
	})
	for _, tc := range []struct {
		args     []string
		out, err string
		requests []string
	}{
		{[]string{"enable", "Nightly"}, "", "✓ Enabled Nightly (nightly.yaml)\n", []string{"GET actions/workflows", "PUT actions/workflows/nightly.yaml/enable", "GET actions/workflows/nightly.yaml"}},
		{[]string{"enable", "nightly", "--json", "state"}, `{"state":"active"}`, "", []string{"GET actions/workflows", "PUT actions/workflows/nightly.yaml/enable", "GET actions/workflows/nightly.yaml"}},
		{[]string{"disable", "NIGHTLY"}, "", "! Nightly (nightly.yaml) is already disabled\n", []string{"GET actions/workflows"}},
	} {
		*requests = nil
		deps, out, diagnostics := actionsDeps(t, server.URL, true)
		prompter := &scriptedPrompter{}
		deps.Prompter = prompter
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		require.NoError(t, Execute(context.Background(), deps, append(append([]string{"workflow"}, tc.args...), "-R", "alice/project")))
		if tc.out != "" {
			require.JSONEq(t, tc.out, out.String())
		} else {
			require.Empty(t, out.String())
		}
		require.Equal(t, tc.err, diagnostics.String())
		require.Equal(t, tc.requests, *requests)
		require.Empty(t, prompter.confirmations)
	}
}

func TestWorkflowRunFollowStreamsTheLogsOfItsJobs(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows":                    workflowsFixture,
		"GET contents/.gitea/workflows/ci.yml":     contentsOf("on: workflow_dispatch\n"),
		"POST actions/workflows/ci.yml/dispatches": `{"workflow_run_id":11,"html_url":"https://gitea.test/alice/project/actions/runs/11"}`,
		"GET actions/runs/11/jobs":                 failedJobsFixture,
		"GET actions/runs/11":                      `{"id":11,"path":"ci.yml@refs/heads/main","status":"completed","conclusion":"failure"}`,
		"GET actions/jobs/30/logs":                 "compiled\n",
		"GET actions/jobs/31/logs":                 "boom\n",
	})
	deps, out, diagnostics := actionsDeps(t, server.URL, false)
	// Like gt run watch, following a run that fails fails.
	err := Execute(context.Background(), deps, []string{"workflow", "run", "ci", "-R", "alice/project", "--ref", "main", "--follow", "--interval=1ms"})
	require.EqualError(t, err, "run 11 (CI) completed with failure")
	require.Equal(t, 1, clierr.ExitCode(err))
	require.Equal(t, "https://gitea.test/alice/project/actions/runs/11\nbuild\tcompiled\ntest\tboom\n", out.String())
	require.Equal(t, "build (job 30)\ntest (job 31)\n", diagnostics.String())
	succeeded, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows":                    workflowsFixture,
		"GET contents/.gitea/workflows/ci.yml":     contentsOf("on: workflow_dispatch\n"),
		"POST actions/workflows/ci.yml/dispatches": `{"workflow_run_id":11}`,
		"GET actions/runs/11/jobs":                 `{"total_count":1,"jobs":[{"id":30,"name":"build","status":"completed","conclusion":"success","started_at":"2028-01-01T11:00:05Z"}]}`,
		"GET actions/runs/11":                      `{"id":11,"status":"completed","conclusion":"success"}`,
		"GET actions/jobs/30/logs":                 "compiled\n",
	})
	deps, _, _ = actionsDeps(t, succeeded.URL, false)
	require.NoError(t, Execute(context.Background(), deps, []string{"workflow", "run", "ci", "-R", "alice/project", "--ref", "main", "--follow", "--interval=1ms"}))
	err = Execute(context.Background(), deps, []string{"workflow", "run", "ci.yml", "-R", "alice/project", "--ref", "main", "--follow", "--json"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--follow cannot be combined with --json")
}
