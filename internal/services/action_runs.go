// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	gitea "gitea.dev/sdk"
)

// Run is the stable command representation of an Actions run. StartedAt and CompletedAt are nil
// until the run starts and completes. Workflow is the workflow's name and WorkflowPath its file
// name; Jobs is only filled where commands show a single run, and JSON leaves it out elsewhere.
type Run struct {
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	StartedAt    *string `json:"started_at"`
	CompletedAt  *string `json:"completed_at"`
	ID           int64   `json:"id"`
	RunNumber    int64   `json:"run_number"`
	Title        string  `json:"title"`
	Workflow     string  `json:"workflow"`
	WorkflowPath string  `json:"workflow_path"`
	Status       string  `json:"status"`
	Conclusion   string  `json:"conclusion"`
	URL          string  `json:"url"`
	Branch       string  `json:"branch"`
	Event        string  `json:"event"`
	Actor        string  `json:"actor"`
	Commit       string  `json:"commit"`
	Attempt      int64   `json:"attempt"`
	Jobs         []Job   `json:"jobs,omitzero"`
}

// Completed reports whether the run finished, whatever its conclusion.
func (r Run) Completed() bool { return completedActionStatus(r.Status) }

type runResponse struct {
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	StartedAt    string `json:"started_at"`
	CompletedAt  string `json:"completed_at"`
	ID           int64  `json:"id"`
	RunNumber    int64  `json:"run_number"`
	DisplayTitle string `json:"display_title"`
	Path         string `json:"path"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HTMLURL      string `json:"html_url"`
	HeadBranch   string `json:"head_branch"`
	Event        string `json:"event"`
	HeadSHA      string `json:"head_sha"`
	RunAttempt   int64  `json:"run_attempt"`
	Actor        struct {
		Login string `json:"login"`
	} `json:"actor"`
}

// result converts the response; until the run is named after its workflow, Workflow is the
// workflow's file name without extension.
func (r runResponse) result() Run {
	file, _, _ := strings.Cut(r.Path, "@")
	return Run{
		ID: r.ID, RunNumber: r.RunNumber, Title: r.DisplayTitle, Status: r.Status, Conclusion: r.Conclusion, URL: r.HTMLURL,
		Branch: r.HeadBranch, Workflow: strings.TrimSuffix(file, path.Ext(file)), WorkflowPath: file,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, StartedAt: runTime(r.StartedAt), CompletedAt: runTime(r.CompletedAt),
		Event: r.Event, Actor: r.Actor.Login, Commit: r.HeadSHA, Attempt: r.RunAttempt,
	}
}

// runTime returns an Actions timestamp, or nil for the zero or Unix epoch time Gitea reports
// for a run or job that has not started or completed.
func runTime(raw string) *string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if raw == "" || (err == nil && t.Unix() <= 0) {
		return nil
	}
	return &raw
}

// RunListOptions supplies server-side run filters and pagination. Workflow is a workflow file
// name.
type RunListOptions struct {
	Since, Until                           *time.Time
	Limit, Page                            int
	Branch, Event, Actor, Commit, Workflow string
	Statuses                               []string
}

// RunStatuses are the values Gitea accepts as run status filters.
var RunStatuses = []string{"queued", "in_progress", "waiting", "requested", "pending", "action_required", "completed", "success", "failure", "cancelled", "skipped", "timed_out", "neutral"} //nolint:misspell // Gitea API spelling.

// ErrRunScanExhausted reports time filters that matched too few of the runs a scan may read.
var ErrRunScanExhausted = fmt.Errorf("time filters scanned %d runs without exhausting the history", maxRunScanPages*100)

// Validate rejects unsupported status filters before performing API requests.
func (o RunListOptions) Validate() error {
	if o.Limit < 1 || o.Limit > 1000 || o.Page < 1 || o.Page > int(^uint(0)>>1)/o.Limit {
		return errors.New("invalid pagination")
	}
	for _, status := range o.Statuses {
		if !slices.Contains(RunStatuses, status) {
			return fmt.Errorf("unsupported run status %q", status)
		}
	}
	return nil
}

const maxRunScanPages = 50

// Runs lists a page of runs, newest first.
func (s Actions) Runs(ctx context.Context, opts RunListOptions) ([]Run, error) {
	page, err := s.runs(ctx, opts)
	return page.Items, err
}

// RunsPage lists a page of runs like Runs, named after their workflows, with the number of runs
// the filters match when the server counts them.
func (s Actions) RunsPage(ctx context.Context, opts RunListOptions) (Page[Run], error) {
	page, err := s.runs(ctx, opts)
	if err != nil {
		return page, err
	}
	return page, s.nameRuns(ctx, page.Items)
}

func (s Actions) runs(ctx context.Context, opts RunListOptions) (Page[Run], error) {
	if err := opts.Validate(); err != nil {
		return Page[Run]{}, err
	}
	if opts.Since == nil && opts.Until == nil {
		return s.runsPage(ctx, opts)
	}
	first, last := (opts.Page-1)*opts.Limit, opts.Page*opts.Limit
	result := []Run{}
	matched := 0
	for page := 1; page <= maxRunScanPages; page++ {
		scan := opts
		scan.Limit = 100
		scan.Page = page
		window, err := s.runsPage(ctx, scan)
		if err != nil {
			return Page[Run]{}, err
		}
		for _, item := range window.Items {
			created, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
			if err != nil {
				return Page[Run]{}, errors.New("server returned an invalid run creation timestamp")
			}
			// Runs are listed newest first, so the rest were created earlier still.
			if opts.Since != nil && created.Before(*opts.Since) {
				return Page[Run]{Items: result}, nil
			}
			if opts.Until != nil && created.After(*opts.Until) {
				continue
			}
			if matched >= last {
				return Page[Run]{Items: result, More: true}, nil
			}
			if matched >= first {
				result = append(result, item)
			}
			matched++
		}
		if len(window.Items) == 0 || !window.More {
			return Page[Run]{Items: result}, nil
		}
	}
	return Page[Run]{}, ErrRunScanExhausted
}

func (s Actions) runsPage(ctx context.Context, opts RunListOptions) (Page[Run], error) {
	query := url.Values{}
	for key, value := range map[string]string{"branch": opts.Branch, "event": opts.Event, "actor": opts.Actor, "head_sha": opts.Commit} {
		if value != "" {
			query.Set(key, value)
		}
	}
	for _, status := range opts.Statuses {
		query.Add("status", status)
	}
	endpoint := s.endpoint() + "runs"
	if opts.Workflow != "" {
		endpoint = s.endpoint() + "workflows/" + url.PathEscape(opts.Workflow) + "/runs"
	}
	runs, err := instancePage(ctx, s.Client, opts.Limit, opts.Page, func(limit, page int) ([]Run, http.Header, error) {
		query.Set("limit", strconv.Itoa(limit))
		query.Set("page", strconv.Itoa(page))
		var response struct {
			Runs []runResponse `json:"workflow_runs"`
		}
		// TODO(sdk): ListRepoActionsRunsOptions takes one status and ActionsWorkflowRun lacks created_at/updated_at.
		headers, err := rawJSON(ctx, s.Client, http.MethodGet, endpoint+"?"+query.Encode(), nil, &response)
		runs := make([]Run, len(response.Runs))
		for i, item := range response.Runs {
			runs[i] = item.result()
		}
		return runs, headers, err
	})
	if opts.Workflow != "" && IsNotFound(err) {
		return Page[Run]{}, NotFound("workflow", strconv.Quote(opts.Workflow), s.Repository)
	}
	return runs, Classify(err, "repository", s.Repository, "")
}

// nameRuns names runs after their workflows.
func (s Actions) nameRuns(ctx context.Context, runs []Run) error {
	if len(runs) == 0 {
		return nil
	}
	names, err := s.workflowNames(ctx)
	for i := range runs {
		if name := names[runs[i].WorkflowPath]; name != "" {
			runs[i].Workflow = name
		}
	}
	return err
}

func (s Actions) workflowNames(ctx context.Context) (map[string]string, error) {
	workflows, err := s.Workflows(ctx)
	names := make(map[string]string, len(workflows))
	for _, workflow := range workflows {
		names[workflow.ID] = workflow.Name
	}
	return names, err
}

// Run retrieves one Actions run.
func (s Actions) Run(ctx context.Context, id int64) (Run, error) {
	var response runResponse
	// TODO(sdk): ActionsWorkflowRun lacks created_at/updated_at.
	err := getJSON(ctx, s.Client, fmt.Sprintf("%sruns/%d", s.endpoint(), id), &response)
	return response.result(), repositoryItemError(ctx, s.Client, s.Repository, err, "run", strconv.FormatInt(id, 10))
}

// NameRun names a run that a mutation returned after its workflow; it stays as is when the
// workflows cannot be listed.
func (s Actions) NameRun(ctx context.Context, run Run) Run {
	runs := []Run{run}
	_ = s.nameRuns(ctx, runs)
	return runs[0]
}

// RunDetails retrieves one run with its jobs, named after its workflow.
func (s Actions) RunDetails(ctx context.Context, id int64) (Run, error) {
	names, err := s.workflowNames(ctx)
	if err != nil {
		return Run{}, err
	}
	return s.runDetails(ctx, id, names)
}

func (s Actions) runDetails(ctx context.Context, id int64, names map[string]string) (Run, error) {
	run, err := s.Run(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if run.Jobs, err = s.Jobs(ctx, id); err != nil {
		return Run{}, err
	}
	if name := names[run.WorkflowPath]; name != "" {
		run.Workflow = name
	}
	return run, nil
}

// Rerun schedules another attempt of a run, restricted to its failed jobs with failedOnly, or of
// one job when job is not 0.
func (s Actions) Rerun(ctx context.Context, id, job int64, failedOnly bool) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		switch {
		case job != 0:
			_, resp, err := c.Actions.RerunRepoRunJob(ctx, owner, repo, id, job)
			return resp, err
		case failedOnly:
			return c.Actions.RerunRepoRunFailedJobs(ctx, owner, repo, id)
		}
		_, resp, err := c.Actions.RerunRepoRun(ctx, owner, repo, id)
		return resp, err
	})
	return repositoryItemError(ctx, s.Client, s.Repository, err, "run", strconv.FormatInt(id, 10))
}

// DeleteRun removes the selected run.
func (s Actions) DeleteRun(ctx context.Context, id int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Actions.DeleteRepoRun(ctx, owner, repo, id)
	})
	return repositoryItemError(ctx, s.Client, s.Repository, err, "run", strconv.FormatInt(id, 10))
}

func completedActionStatus(status string) bool {
	switch status {
	case "completed", "success", "failure", "canceled", "cancelled", "skipped": //nolint:misspell // Gitea API spelling.
		return true
	}
	return false
}

// SuccessfulRun recognizes a successful terminal run result.
func SuccessfulRun(run Run) bool {
	return completedActionStatus(run.Status) && (run.Conclusion == "success" || run.Status == "success")
}

// Watch polls a run with its jobs every interval until it completes, reporting each poll to
// update, and returns the completed run. When it fails, such as when ctx expires, it returns the
// run as last polled.
func (s Actions) Watch(ctx context.Context, id int64, interval time.Duration, update func(Run) error) (Run, error) {
	if interval <= 0 {
		return Run{}, errors.New("watch interval must be positive")
	}
	names, err := s.workflowNames(ctx)
	if err != nil {
		return Run{}, err
	}
	var last Run
	var failures outage
	for {
		current, err := s.runDetails(ctx, id, names)
		if failures.retry(ctx, err) {
			if err := sleepCtx(ctx, interval); err != nil {
				return last, err
			}
			continue
		}
		if err != nil {
			return last, err
		}
		last = current
		if update != nil {
			if err := update(current); err != nil {
				return last, err
			}
		}
		if current.Completed() {
			return current, nil
		}
		failures.passed()
		if err := sleepCtx(ctx, interval); err != nil {
			return last, err
		}
	}
}

// CancelRun requests cancellation without deleting the run or its history.
// Force cancellation is an explicit operation for unresponsive runners.
func (s Actions) CancelRun(ctx context.Context, id int64, force bool) (Run, error) {
	action := "cancel"
	if force {
		action = "force-cancel"
	}
	var response runResponse
	// TODO(sdk): ActionsWorkflowRun lacks created_at/updated_at.
	err := sendJSON(ctx, s.Client, http.MethodPost, fmt.Sprintf("%sruns/%d/%s", s.endpoint(), id, action), nil, &response)
	if err != nil {
		return Run{}, repositoryItemError(ctx, s.Client, s.Repository, err, "run", strconv.FormatInt(id, 10))
	}
	return response.result(), nil
}
