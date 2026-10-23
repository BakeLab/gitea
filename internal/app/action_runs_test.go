// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/mattn/go-runewidth"
	"github.com/stretchr/testify/require"
)

// actionsNow is the clock of the Actions fixtures.
var actionsNow = time.Date(2028, 1, 1, 12, 0, 0, 0, time.UTC)

const (
	workflowsFixture = `{"total_count":2,"workflows":[
		{"id":"ci.yml","name":"CI","path":".gitea/workflows/ci.yml","state":"active","html_url":"https://gitea.test/alice/project/src/branch/main/.gitea/workflows/ci.yml"},
		{"id":"nightly.yaml","name":"Nightly","path":".gitea/workflows/nightly.yaml","state":"disabled_manually"}]}`
	queuedRunFixture = `{"id":47,"run_number":10,"display_title":"Add workflows","path":"ci.yml@refs/heads/main","event":"workflow_dispatch",
		"head_branch":"main","head_sha":"f0c151dde0c0ef6c","status":"queued","run_attempt":1,"actor":{"login":"alice"},
		"created_at":"2028-01-01T11:58:00Z","started_at":"1970-01-01T00:00:00Z","completed_at":"1970-01-01T00:00:00Z",
		"html_url":"https://gitea.test/alice/project/actions/runs/47"}`
	failedRunFixture = `{"id":29,"run_number":9,"display_title":"Refactor the deployment pipeline","path":"nightly.yaml@refs/heads/feature/x",
		"event":"push","head_branch":"feature/x","head_sha":"209d545ac554","status":"completed","conclusion":"failure","run_attempt":2,
		"actor":{"login":"bob"},"created_at":"2028-01-01T11:00:00Z","started_at":"2028-01-01T11:00:05Z","completed_at":"2028-01-01T11:01:17Z",
		"html_url":"https://gitea.test/alice/project/actions/runs/29"}`
	queuedJobsFixture = `{"total_count":2,"jobs":[
		{"id":48,"name":"build","status":"queued","labels":["ubuntu-latest"],"html_url":"https://gitea.test/alice/project/actions/runs/47/jobs/48","started_at":"1970-01-01T00:00:00Z","completed_at":"1970-01-01T00:00:00Z"},
		{"id":49,"name":"test","status":"waiting","labels":["ubuntu-latest"],"started_at":"1970-01-01T00:00:00Z","completed_at":"1970-01-01T00:00:00Z"}]}`
	failedJobsFixture = `{"total_count":2,"jobs":[
		{"id":30,"name":"build","status":"completed","conclusion":"success","started_at":"2028-01-01T11:00:05Z","completed_at":"2028-01-01T11:00:50Z",
			"steps":[{"number":1,"name":"Set up job","status":"completed","conclusion":"success","started_at":"2028-01-01T11:00:05Z","completed_at":"2028-01-01T11:00:07Z"}]},
		{"id":31,"name":"test","status":"completed","conclusion":"failure","started_at":"2028-01-01T11:00:50Z","completed_at":"2028-01-01T11:01:17Z",
			"steps":[{"number":1,"name":"Run tests","status":"completed","conclusion":"failure","started_at":"2028-01-01T11:00:51Z","completed_at":"2028-01-01T11:01:16Z"}]}]}`
)

// actionsAPI serves canned responses for alice/project, keyed by method and path below the
// repository ("GET actions/runs"). A response that starts with a status code and a space, such as
// "404 {...}", is sent with that status. It records the keys of all requests, followed by their
// bodies, and fails the test on requests without a response.
func actionsAPI(t *testing.T, responses map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project"), "/")
		if payload, _ := io.ReadAll(r.Body); len(payload) > 0 {
			requests = append(requests, key+" "+string(payload))
		} else {
			requests = append(requests, key)
		}
		body, ok := responses[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if status, rest, found := strings.Cut(body, " "); found && len(status) == 3 {
			if code, err := strconv.Atoi(status); err == nil {
				w.WriteHeader(code)
				body = rest
			}
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// actionsDeps returns dependencies for an actionsAPI server at the fixtures' clock, on a terminal
// without colors or links when tty is set.
func actionsDeps(t *testing.T, serverURL string, tty bool) (Dependencies, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	deps, out, diagnostics := serverDeps(t, serverURL)
	deps.Getenv = serverEnv(serverURL, map[string]string{"TERM": "dumb"})
	deps.Now = func() time.Time { return actionsNow }
	deps.Streams.OutTTY = tty
	return deps, out, diagnostics
}

func TestRunTimeBoundsUseOneClockReading(t *testing.T) {
	deps, _, _ := testDeps(t)
	reads := 0
	deps.Now = func() time.Time { reads++; return actionsNow }
	err := Execute(context.Background(), deps, []string{"run", "list", "--since=1h", "--until=2h"})
	require.ErrorContains(t, err, "--since must not be later than --until")
	require.Equal(t, 1, reads)
}

func TestRunListResolvesAShortCommitInTheClone(t *testing.T) {
	full := "f0c151dde0c0ef6c" + strings.Repeat("0", 24)
	var commits []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		commits = append(commits, r.URL.Query().Get("head_sha"))
		_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
	}))
	defer server.Close()
	deps, _, _ := actionsDeps(t, server.URL, false)
	deps.Git = &fakeGit{results: map[string]string{"rev-parse --verify --quiet --end-of-options f0c151d^{commit}": full}}
	for _, commit := range []string{"f0c151d", strings.ToUpper(full)} {
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--commit", commit}))
	}
	require.Equal(t, []string{full, full}, commits)
	// Outside a clone that knows it, a short SHA would match no run.
	deps.Git = &fakeGit{}
	err := Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--commit", "f0c151d"})
	require.EqualError(t, err, `--commit requires a full commit SHA, or a revision of the local clone; "f0c151d" is neither`)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Len(t, commits, 2)
}

func TestRunListDoesNotOfferJobs(t *testing.T) {
	// Only gt run view and gt run watch fetch the jobs of a run.
	for _, args := range [][]string{{"list"}, {"cancel", "1"}, {"rerun", "1"}} {
		requireUsageBeforeRequests(t, `unknown JSON field "jobs"`, append(append([]string{"run"}, args...), "--json", "jobs")...)
	}
	deps, _, _ := testDeps(t)
	for _, command := range []string{"view", "watch"} {
		cmd, _, err := NewRoot(deps).Find([]string{"run", command})
		require.NoError(t, err)
		require.Contains(t, commandOutputFields(cmd), "jobs", command)
	}
	server, _ := actionsAPI(t, map[string]string{"GET actions/runs": `{"total_count":1,"workflow_runs":[` + queuedRunFixture + `]}`, "GET actions/workflows": workflowsFixture})
	deps, out, _ := actionsDeps(t, server.URL, false)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--json"}))
	require.NotContains(t, out.String(), `"jobs"`)
}

func TestRunListShowsStatusWorkflowAndTiming(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs":      `{"total_count":2,"workflow_runs":[` + queuedRunFixture + `,` + failedRunFixture + `]}`,
		"GET actions/workflows": workflowsFixture,
	})
	for _, tc := range []struct {
		name string
		tty  bool
		args []string
		want string
	}{
		{"terminal", true, nil, `Showing 2 of 2 runs in alice/project

STATUS  ID  WORKFLOW  TITLE                             BRANCH     EVENT              ELAPSED  AGE
*       47  CI        Add workflows                     main       workflow_dispatch           about 2 minutes ago
X       29  Nightly   Refactor the deployment pipeline  feature/x  push               1m 12s   about 1 hour ago
`},
		{"piped", false, nil, "queued\t\t47\tCI\tAdd workflows\tmain\tworkflow_dispatch\t\t2028-01-01T11:58:00Z\thttps://gitea.test/alice/project/actions/runs/47\n" +
			"completed\tfailure\t29\tNightly\tRefactor the deployment pipeline\tfeature/x\tpush\t1m 12s\t2028-01-01T11:00:00Z\thttps://gitea.test/alice/project/actions/runs/29\n"},
		{"json", false, []string{"--json", "id,workflow,workflow_path,run_number,started_at"}, `[{"id":47,"workflow":"CI","workflow_path":"ci.yml","run_number":10,"started_at":null},` +
			`{"id":29,"workflow":"Nightly","workflow_path":"nightly.yaml","run_number":9,"started_at":"2028-01-01T11:00:05Z"}]` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := actionsDeps(t, server.URL, tc.tty)
			require.NoError(t, Execute(context.Background(), deps, append([]string{"run", "list", "-R", "alice/project"}, tc.args...)))
			if strings.HasPrefix(tc.want, "[") {
				require.JSONEq(t, tc.want, out.String())
			} else {
				require.Equal(t, tc.want, out.String())
			}
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestRunListAcceptsWorkflowFilesMissingFromTheDefaultBranch(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/workflows":               workflowsFixture,
		"GET actions/workflows/old.yml/runs":  `{"total_count":1,"workflow_runs":[` + failedRunFixture + `]}`,
		"GET actions/workflows/gone.yml/runs": `404 {"message":"workflow \"gone.yml\" not found"}`,
	})
	deps, out, _ := actionsDeps(t, server.URL, false)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--workflow", "old.yml", "--json", "id"}))
	require.JSONEq(t, `[{"id":29}]`, out.String())
	err := Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--workflow", "gone.yml"})
	require.EqualError(t, err, `workflow "gone.yml" not found in alice/project`)
	err = Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--workflow", "Missing name"})
	require.EqualError(t, err, `workflow "Missing name" not found in alice/project`)
	require.NotContains(t, *requests, "GET actions/workflows/Missing name/runs")
}

func TestRunListResolvesWorkflowNamesAndReportsEmptyFilters(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/workflows":             workflowsFixture,
		"GET actions/workflows/ci.yml/runs": `{"total_count":0,"workflow_runs":[]}`,
	})
	deps, out, diagnostics := actionsDeps(t, server.URL, true)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "-w", "ci", "-s", "FAILURE"}))
	require.Empty(t, out.String())
	require.Equal(t, "no runs match your filters in alice/project\n", diagnostics.String())
	require.Contains(t, *requests, "GET actions/workflows/ci.yml/runs")
	requireUsageBeforeRequests(t, "--status must be queued, in_progress, waiting", "run", "list", "--status", "nope")
}

func TestRunListExplainsTheAccountFlag(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{"GET actions/runs": `{"workflow_runs":[]}`})
	deps, _, diagnostics := actionsDeps(t, server.URL, true)
	deps.Streams.ErrTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "list", "-R", "alice/project", "--user", "alice"}))
	require.Equal(t, "note: --user selects the account gt acts as (alice); to list the runs someone triggered, use --actor\nno runs in alice/project\n", diagnostics.String())
}

func TestRunViewShowsJobsStepsAndNextSteps(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows":    workflowsFixture,
		"GET actions/runs/47":      queuedRunFixture,
		"GET actions/runs/47/jobs": queuedJobsFixture,
		"GET actions/runs/29":      failedRunFixture,
		"GET actions/runs/29/jobs": failedJobsFixture,
	})
	for _, tc := range []struct {
		name string
		tty  bool
		args []string
		want string
	}{
		{"queued", true, []string{"47"}, `* Add workflows CI #10 · run 47
queued • main • f0c151d • alice triggered it via workflow_dispatch about 2 minutes ago

JOBS
*  build  queued     (ID 48)
*  test   waiting    (ID 49)

Waiting for a runner for "ubuntu-latest"

Watch it: gt run watch 47 -R alice/project
View this run on Gitea: https://gitea.test/alice/project/actions/runs/47
`},
		{"completed from its URL", true, []string{"{server}/alice/project/actions/runs/29"}, `X Refactor the deployment pipeline Nightly #9 · run 29
failure • feature/x • 209d545 • bob triggered it via push about 1 hour ago • took 1m 12s • attempt 2

JOBS
✓  build           success  45s  (ID 30)
     ✓ Set up job           2s
X  test            failure  27s  (ID 31)
     X Run tests            25s

Logs of a job: gt run logs 29 -R alice/project --job <id>
View this run on Gitea: https://gitea.test/alice/project/actions/runs/29
`},
		{"piped", false, []string{"47"}, "title:\tAdd workflows\nid:\t47\nworkflow:\tCI\nworkflow_path:\tci.yml\nrun_number:\t10\nstatus:\tqueued\nconclusion:\t\n" +
			"branch:\tmain\ncommit:\tf0c151dde0c0ef6c\nevent:\tworkflow_dispatch\nactor:\talice\nattempt:\t1\ncreated_at:\t2028-01-01T11:58:00Z\nstarted_at:\t\n" +
			"completed_at:\t\nelapsed:\t\nurl:\thttps://gitea.test/alice/project/actions/runs/47\n--\n" +
			"queued\t\t48\tbuild\t\thttps://gitea.test/alice/project/actions/runs/47/jobs/48\nwaiting\t\t49\ttest\t\t\n"},
		{"json with jobs", false, []string{"29", "--json", "id,jobs", "--jq", "[.id, [.jobs[] | .name, .conclusion, .steps[0].name]]"}, `[29,["build","success","Set up job","test","failure","Run tests"]]` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := actionsDeps(t, server.URL, tc.tty)
			args := append([]string{"run", "view", strings.ReplaceAll(tc.args[0], "{server}", server.URL)}, tc.args[1:]...)
			if !strings.Contains(tc.args[0], "{server}") {
				args = append(args, "-R", "alice/project")
			}
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestRunViewFitsStepsToTheTerminalWidth(t *testing.T) {
	run := services.Run{ID: 5, Status: "completed", Conclusion: "success", Jobs: []services.Job{{
		ID: 6, Name: "build", Status: "completed", Conclusion: "success",
		Steps: []services.Step{{Name: "Run for i in 1 2 3; do echo $i; sleep 1; done && make test", Status: "completed", Conclusion: "success"}},
	}}}
	text := renderRun(run, terminal.Display{TTY: true, Width: 40, Now: actionsNow}, nil)
	_, jobs, _ := strings.Cut(text, "JOBS\n")
	require.Equal(t, "✓  build               success    (ID 6)\n     ✓ Run for i in …\n", jobs)
	for line := range strings.Lines(jobs) {
		require.LessOrEqual(t, runewidth.StringWidth(strings.TrimSuffix(line, "\n")), 40, line)
	}
}

func TestRunViewFitsItsHeadingAndFactsToTheTerminalWidth(t *testing.T) {
	run := services.Run{
		ID: 1, RunNumber: 3, Workflow: "integration", Title: "Make every command fit an eighty-column terminal, including the run view",
		Status: "completed", Conclusion: "failure", Branch: "main", Commit: "991205e0c0ef6c", Actor: "integration", Event: "push",
		CreatedAt: "2028-01-01T06:00:00Z", StartedAt: new("2028-01-01T06:00:00Z"), CompletedAt: new("2028-01-01T06:00:03Z"),
	}
	text := renderRun(run, terminal.Display{TTY: true, Width: 80, Now: actionsNow}, nil)
	require.Equal(t, `X Make every command fit an eighty-column terminal, incl… integration #3 · run 1
failure • main • 991205e • integration triggered it via push about 6 hours ago
took 3s
`, text)
	// A fact or a workflow wider than the terminal is cut too.
	run.Branch = strings.Repeat("b", 50)
	run.Workflow = strings.Repeat("w", 50)
	text = renderRun(run, terminal.Display{TTY: true, Width: 40, Now: actionsNow}, nil)
	for line := range strings.Lines(text) {
		require.LessOrEqual(t, runewidth.StringWidth(strings.TrimSuffix(line, "\n")), 40, line)
	}
	require.Contains(t, text, "X Make ev… "+strings.Repeat("w", 17)+"… #3 · run 1\n")
	// Without a width, nothing is cut or wrapped.
	text = renderRun(run, terminal.Display{TTY: true, Now: actionsNow}, nil)
	require.Contains(t, text, run.Title+" "+run.Workflow+" #3 · run 1\n")
	require.Contains(t, text, " • took 3s\n")
}

// choosingPrompter answers selections with the choice at index choose.
type choosingPrompter struct {
	scriptedPrompter
	choose  int
	choices []terminal.Choice
	titles  []string
}

func (p *choosingPrompter) Choose(_ context.Context, title string, choices []terminal.Choice, _ string) (string, error) {
	p.titles = append(p.titles, title)
	p.choices = choices
	if p.choose >= len(choices) {
		return "", errors.New("no such choice")
	}
	return choices[p.choose].Value, nil
}

func TestRunCommandsAskForARunWithoutAnArgument(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/runs":         `{"workflow_runs":[` + failedRunFixture + `,` + queuedRunFixture + `]}`,
		"GET actions/workflows":    workflowsFixture,
		"GET actions/runs/29/jobs": failedJobsFixture,
	})
	deps, out, _ := actionsDeps(t, server.URL, false)
	prompter := &choosingPrompter{}
	deps.Prompter = prompter
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "jobs", "-R", "alice/project", "--json", "id", "--interactive"}))
	require.JSONEq(t, `[{"id":30},{"id":31}]`, out.String())
	require.Equal(t, []string{"Select a run"}, prompter.titles)
	require.Equal(t, []terminal.Choice{
		{Label: "X Refactor the deployment pipeline · Nightly · feature/x (run 29, about 1 hour ago)", Value: "29"},
		{Label: "* Add workflows · CI · main (run 47, about 2 minutes ago)", Value: "47"},
	}, prompter.choices)

	*requests = nil
	prompter.choose = 1
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"run", "watch", "-R", "alice/project"}), "no such choice")
	require.Equal(t, []string{"GET actions/runs", "GET actions/workflows"}, *requests)
	require.Equal(t, []terminal.Choice{{Label: "* Add workflows · CI · main (run 47, about 2 minutes ago)", Value: "47"}}, prompter.choices, "watch offers runs in progress")

	deps.Streams.InTTY = false
	err := Execute(context.Background(), deps, []string{"run", "view", "-R", "alice/project"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "missing run ID\nUsage: gt run view [<run-id>] [flags]")
}

func TestRunWatchExitStatusAndTimeout(t *testing.T) {
	succeeded := `{"id":7,"path":"ci.yml@refs/heads/main","status":"completed","conclusion":"success","created_at":"2028-01-01T11:59:00Z",
		"started_at":"2028-01-01T11:59:00Z","completed_at":"2028-01-01T11:59:30Z"}`
	for _, tc := range []struct {
		name, run, jobs, want, err, progress string
		code                                 int
	}{
		{"success", succeeded, `{"jobs":[]}`, `{"id":7,"conclusion":"success","workflow":"CI"}`, "", "run 7: success\n", 0},
		{"failure", strings.Replace(succeeded, `"success"`, `"failure"`, 1), `{"jobs":[]}`, `{"id":7,"conclusion":"failure","workflow":"CI"}`, "run 7 (CI) completed with failure", "run 7: failure\n", clierr.Unsuccessful},
		{"request error", `400 {"message":"bad request"}`, `{"jobs":[]}`, "", "Gitea API returned HTTP 400 for GET /api/v1/repos/alice/project/actions/runs/7: bad request", "", clierr.Failure},
		{
			"timeout", strings.Replace(queuedRunFixture, `"id":47`, `"id":7`, 1), queuedJobsFixture, "",
			`timed out after 100ms waiting for run 7 (still queued; is a runner registered for "ubuntu-latest"?)`, "run 7: queued (build: queued, test: waiting)\n", clierr.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := actionsAPI(t, map[string]string{"GET actions/workflows": workflowsFixture, "GET actions/runs/7": tc.run, "GET actions/runs/7/jobs": tc.jobs})
			deps, out, diagnostics := actionsDeps(t, server.URL, false)
			err := Execute(context.Background(), deps, []string{"run", "watch", "7", "-R", "alice/project", "--json=id,conclusion,workflow", "--interval=1ms", "--timeout=100ms"})
			require.Equal(t, tc.code, clierr.ExitCode(err), "%v", err)
			if tc.err == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.err)
			}
			if tc.want == "" {
				require.Empty(t, out.String())
			} else {
				require.JSONEq(t, tc.want, out.String())
			}
			require.Equal(t, tc.progress, diagnostics.String())
		})
	}
}

func TestRunWatchExpectHeadFailsBeforeWaitingForAnotherCommit(t *testing.T) {
	full := "f0c151dde0c0ef6c" + strings.Repeat("0", 24)
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/workflows":    workflowsFixture,
		"GET actions/runs/47":      strings.Replace(queuedRunFixture, `"f0c151dde0c0ef6c"`, `"`+full+`"`, 1),
		"GET actions/runs/47/jobs": queuedJobsFixture,
	})
	deps, out, _ := actionsDeps(t, server.URL, false)
	deps.Git = &fakeGit{results: map[string]string{"rev-parse --verify --quiet --end-of-options HEAD^{commit}": "209d545a" + strings.Repeat("0", 32)}}
	watch := func(head string) error {
		return Execute(context.Background(), deps, []string{"run", "watch", "47", "-R", "alice/project", "--interval=1ms", "--timeout=50ms", "--expect-head", head})
	}
	err := watch("HEAD")
	require.EqualError(t, err, "run 47 is for commit "+full+", not 209d545a"+strings.Repeat("0", 32))
	require.Equal(t, clierr.HeadMismatch, clierr.ExitCode(err))
	require.Equal(t, []string{"GET actions/workflows", "GET actions/runs/47", "GET actions/runs/47/jobs"}, *requests, "no waiting")
	require.Empty(t, out.String())
	require.EqualError(t, watch("209D545"), "run 47 is for commit "+full+", not 209d545")
	// A matching full or abbreviated SHA waits for the run.
	for _, head := range []string{"F0C151D", full} {
		require.Equal(t, clierr.Canceled, clierr.ExitCode(watch(head)))
	}
	err = watch("main")
	require.EqualError(t, err, `--expect-head requires a commit SHA, or a revision of the local clone; "main" is neither`)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
}

func TestRunWatchSummaryBypassesThePager(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows":   workflowsFixture,
		"GET actions/runs/7":      strings.Replace(failedRunFixture, `"id":29`, `"id":7`, 1),
		"GET actions/runs/7/jobs": failedJobsFixture,
	})
	deps, out, _ := actionsDeps(t, server.URL, true)
	pager := &recordingPager{}
	deps.Pager = pager
	deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "dumb", "PAGER": "fake-pager"})
	err := Execute(context.Background(), deps, []string{"run", "watch", "7", "-R", "alice/project", "--interval=1ms"})
	require.EqualError(t, err, "run 7 (Nightly) completed with failure")
	require.Zero(t, pager.calls, "the pager would hold the command back after the run completed")
	require.Contains(t, out.String(), "Run tests")
}

func TestRunWatchErrorsDoNotCarryServerControlCharacters(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows": `{"workflows":[]}`,
		"GET actions/runs/7": `{"id":7,"path":"\u001b]0;pwned\u0007ci.yml@refs/heads/main","status":"completed","conclusion":"failure",
			"created_at":"2028-01-01T11:59:00Z","started_at":"2028-01-01T11:59:00Z","completed_at":"2028-01-01T11:59:30Z"}`,
		"GET actions/runs/7/jobs": `{"jobs":[]}`,
	})
	deps, _, _ := actionsDeps(t, server.URL, false)
	err := Execute(context.Background(), deps, []string{"run", "watch", "7", "-R", "alice/project", "--interval=1ms"})
	require.EqualError(t, err, "run 7 (]0;pwnedci) completed with failure")
}

func TestRunWatchRedrawsOnATerminalAndSummarizes(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/workflows": workflowsFixture,
		"GET actions/runs/7": `{"id":7,"display_title":"Ship it","path":"ci.yml@refs/heads/main","status":"completed","conclusion":"success",
			"created_at":"2028-01-01T11:59:00Z","started_at":"2028-01-01T11:59:00Z","completed_at":"2028-01-01T11:59:30Z"}`,
		"GET actions/runs/7/jobs": `{"jobs":[]}`,
	})
	deps, out, diagnostics := actionsDeps(t, server.URL, true)
	deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "xterm", "GITEA_CLI_COLOR": "never"})
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "watch", "7", "-R", "alice/project", "--interval=2s"}))
	frame := "✓ Ship it CI · run 7\nsuccess • triggered about 1 minute ago • took 30s\n"
	require.Equal(t, "\x1b[?1049h\x1b[H\x1b[JRefreshing run status every 2s. Press Ctrl+C to quit.\n\n"+frame+"\x1b[?1049l"+frame, out.String())
	require.Equal(t, "✓ Run 7 (CI) completed with success in 30s\n", diagnostics.String())

	// A dumb terminal, or a console that does not interpret escape sequences, gets the progress
	// lines instead of a redraw.
	for _, plain := range []func(*Dependencies){
		func(deps *Dependencies) { deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "dumb"}) },
		func(deps *Dependencies) {
			deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "xterm"})
			deps.Streams.OutPlain = true
		},
	} {
		deps, out, diagnostics := actionsDeps(t, server.URL, true)
		plain(&deps)
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "watch", "7", "-R", "alice/project", "--interval=2s"}))
		require.NotContains(t, out.String(), "\x1b[")
		require.Contains(t, diagnostics.String(), "run 7: success\n")
	}
}

func TestRunMutationsReportWhatHappened(t *testing.T) {
	completedRun := failedRunFixture
	for _, tc := range []struct {
		name      string
		args      []string
		responses map[string]string
		requests  []string
		out, err  string
		prompts   []string
	}{
		{
			"cancel without confirmation",
			[]string{"run", "cancel", "47"},
			map[string]string{"POST actions/runs/47/cancel": queuedRunFixture},
			[]string{"POST actions/runs/47/cancel"},
			"", "✓ Requested cancellation of run 47\n", nil,
		},
		{
			"cancel returns the run",
			[]string{"run", "cancel", "47", "--json", "id,status,workflow"},
			map[string]string{"POST actions/runs/47/cancel": queuedRunFixture, "GET actions/workflows": workflowsFixture},
			[]string{"POST actions/runs/47/cancel", "GET actions/workflows"},
			`{"id":47,"status":"queued","workflow":"CI"}`, "", nil,
		},
		{
			"cancel with fields the server may leave empty",
			[]string{"run", "cancel", "47", "--json", "id,attempt,event"},
			map[string]string{"POST actions/runs/47/cancel": `{"id":47}`, "GET actions/workflows": workflowsFixture},
			[]string{"POST actions/runs/47/cancel", "GET actions/workflows"},
			`{"id":47,"attempt":0,"event":""}`, "", nil,
		},
		{
			"rerun",
			[]string{"run", "rerun", "29"},
			map[string]string{"GET actions/runs/29": completedRun, "POST actions/runs/29/rerun": completedRun},
			[]string{"GET actions/runs/29", "POST actions/runs/29/rerun", "GET actions/runs/29"},
			"", "✓ Requested rerun of run 29 (attempt 2); watch it with: gt run watch 29 -R alice/project\n", nil,
		},
		{
			"rerun returns the run",
			[]string{"run", "rerun", "29", "--json", "id,workflow"},
			map[string]string{"GET actions/runs/29": completedRun, "POST actions/runs/29/rerun": completedRun, "GET actions/workflows": workflowsFixture},
			[]string{"GET actions/runs/29", "POST actions/runs/29/rerun", "GET actions/runs/29", "GET actions/workflows"},
			`{"id":29,"workflow":"Nightly"}`, "", nil,
		},
		{
			"rerun a job by name",
			[]string{"run", "rerun", "29", "--job", "TEST"},
			map[string]string{"GET actions/runs/29": completedRun, "GET actions/runs/29/jobs": failedJobsFixture, "POST actions/runs/29/jobs/31/rerun": `{"id":31}`},
			[]string{"GET actions/runs/29", "GET actions/runs/29/jobs", "POST actions/runs/29/jobs/31/rerun", "GET actions/runs/29"},
			"", "✓ Requested rerun of run 29 (attempt 2); watch it with: gt run watch 29 -R alice/project\n", nil,
		},
		{
			"rerun refuses an unfinished run",
			[]string{"run", "rerun", "47"},
			map[string]string{"GET actions/runs/47": queuedRunFixture},
			[]string{"GET actions/runs/47"},
			"", "", nil,
		},
		{
			"delete after confirmation",
			[]string{"run", "delete", "29"},
			map[string]string{"GET actions/workflows": workflowsFixture, "GET actions/runs/29": completedRun, "GET actions/runs/29/jobs": failedJobsFixture, "DELETE actions/runs/29": "204 "},
			[]string{"GET actions/workflows", "GET actions/runs/29", "GET actions/runs/29/jobs", "DELETE actions/runs/29"},
			"", "✓ Deleted run 29 from alice/project\n",
			[]string{"Delete run 29 (Nightly · Refactor the deployment pipeline) from alice/project?"},
		},
		{
			"delete refuses an unfinished run before asking",
			[]string{"run", "delete", "47"},
			map[string]string{"GET actions/workflows": workflowsFixture, "GET actions/runs/47": queuedRunFixture, "GET actions/runs/47/jobs": queuedJobsFixture},
			[]string{"GET actions/workflows", "GET actions/runs/47", "GET actions/runs/47/jobs"},
			"", "", nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := actionsAPI(t, tc.responses)
			deps, out, diagnostics := actionsDeps(t, server.URL, true)
			prompter := &scriptedPrompter{accept: true}
			deps.Prompter = prompter
			deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			err := Execute(context.Background(), deps, append(tc.args, "-R", "alice/project"))
			if strings.Contains(tc.name, "unfinished") {
				require.EqualError(t, err, "run 47 is still queued; cancel it first: gt run cancel 47 -R alice/project")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.requests, *requests)
			if strings.HasPrefix(tc.out, "{") {
				require.JSONEq(t, tc.out, out.String())
			} else {
				require.Equal(t, tc.out, out.String())
			}
			require.Equal(t, tc.err, diagnostics.String())
			require.Equal(t, tc.prompts, prompter.confirmations)
		})
	}
}

func TestRunJobsListsStatusAndElapsedTime(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{"GET actions/runs/29/jobs": failedJobsFixture})
	deps, out, _ := actionsDeps(t, server.URL, true)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "jobs", "#29", "-R", "alice/project"}))
	require.Equal(t, "Showing 2 of 2 jobs in run 29\n\nSTATUS  ID  NAME   ELAPSED\n✓       30  build  45s\nX       31  test   27s\n", out.String())
}

func TestRunsAwaitingApprovalAreNotReportedAsWaitingForARunner(t *testing.T) {
	jobs := []services.Job{{ID: 1, Name: "build", Status: "waiting", Labels: []string{"ubuntu-latest"}}}
	approval := services.Run{ID: 7, Status: "waiting", Jobs: jobs}
	require.Equal(t, "Waiting for approval; approve the run in the web interface", waitingForRunner(approval))
	require.Equal(t, "still waiting for approval in the web interface", stillRunning(approval))
	queued := services.Run{ID: 7, Status: "queued", Jobs: jobs}
	require.Equal(t, `Waiting for a runner for "ubuntu-latest"`, waitingForRunner(queued))
	require.Equal(t, `still queued; is a runner registered for "ubuntu-latest"?`, stillRunning(queued))
}

func TestRunSuggestionsNameTheInstanceThatWasSelectedExplicitly(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{"GET actions/runs/47": queuedRunFixture})
	host := strings.TrimPrefix(server.URL, "https://")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-R", "alice/project", "--host", server.URL}, " -R " + host + "/alice/project"},
		{[]string{"-R", host + "/alice/project", "--host", server.URL}, " -R " + host + "/alice/project"},
	} {
		deps, _, _ := actionsDeps(t, server.URL, false)
		configureAccounts(t, deps, server.URL, "alice", "alice")
		// The instance that commands use by default is another one than the server.
		deps.Getenv = envFunc(map[string]string{"TERM": "dumb", "GITEA_CLI_HOST": "https://default.test"})
		err := Execute(context.Background(), deps, append([]string{"run", "rerun", "47"}, tc.args...))
		require.EqualError(t, err, "run 47 is still queued; cancel it first: gt run cancel 47"+tc.want, tc.args)
	}
}
