// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Workflow is the stable command representation of an Actions workflow.
type Workflow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

func workflowFrom(workflow *gitea.ActionsWorkflow) Workflow {
	return Workflow{ID: workflow.ID, Name: workflow.Name, Path: workflow.Path, State: workflow.State}
}

// Run is the stable command representation of an Actions run.
type Run struct {
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	URL         string `json:"url"`
	Branch      string `json:"branch"`
	Event       string `json:"event,omitempty"`
	Actor       string `json:"actor,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Attempt     int64  `json:"attempt,omitempty"`
}
type runResponse struct {
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	StartedAt    string `json:"started_at"`
	CompletedAt  string `json:"completed_at"`
	ID           int64  `json:"id"`
	DisplayTitle string `json:"display_title"`
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

func (r runResponse) result() Run {
	return Run{
		ID: r.ID, Title: r.DisplayTitle, Status: r.Status, Conclusion: r.Conclusion, URL: r.HTMLURL, Branch: r.HeadBranch,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, StartedAt: r.StartedAt, CompletedAt: r.CompletedAt,
		Event: r.Event, Actor: r.Actor.Login, Commit: r.HeadSHA, Attempt: r.RunAttempt,
	}
}

// DispatchResult identifies a workflow run created by dispatch when the server returns details.
type DispatchResult struct {
	ID  int64  `json:"id"`
	URL string `json:"url,omitempty"`
}

// Actions implements repository workflow and run operations.
type Actions struct {
	Client     *transport.Client
	Repository string
}

func (s Actions) endpoint() string { return "repos/" + s.Repository + "/actions/" }

// Workflows lists the repository workflow definitions.
func (s Actions) Workflows(ctx context.Context) ([]Workflow, error) {
	response, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ActionsWorkflowResponse, *gitea.Response, error) {
		return c.Actions.ListRepoWorkflows(ctx, owner, repo)
	})
	if err != nil {
		return nil, err
	}
	return convertAll(response.Workflows, workflowFrom), nil
}

// Workflow retrieves one workflow definition.
func (s Actions) Workflow(ctx context.Context, id string) (Workflow, error) {
	return repoConvert(ctx, s.Client, s.Repository, workflowFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ActionsWorkflow, *gitea.Response, error) {
		return c.Actions.GetRepoWorkflow(ctx, owner, repo, id)
	})
}

// Dispatch triggers a workflow and reports the run it created.
func (s Actions) Dispatch(ctx context.Context, id, ref string, inputs map[string]string) (DispatchResult, error) {
	details, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.RunDetails, *gitea.Response, error) {
		return c.Actions.DispatchRepoWorkflow(ctx, owner, repo, id, gitea.CreateActionsWorkflowDispatchOption{Ref: ref, Inputs: inputs}, true)
	})
	if err != nil {
		return DispatchResult{}, err
	}
	return DispatchResult{ID: details.WorkflowRunID, URL: details.HTMLURL}, nil
}

// SetWorkflowEnabled updates whether a workflow can run.
func (s Actions) SetWorkflowEnabled(ctx context.Context, id string, enabled bool) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		if enabled {
			return c.Actions.EnableRepoWorkflow(ctx, owner, repo, id)
		}
		return c.Actions.DisableRepoWorkflow(ctx, owner, repo, id)
	})
}

// RunListOptions supplies server-side run filters and pagination.
type RunListOptions struct {
	Since, Until                           *time.Time
	Limit, Page                            int
	Branch, Event, Actor, Commit, Workflow string
	Statuses                               []string
}

// Validate rejects unsupported status filters before performing API requests.
func (o RunListOptions) Validate() error {
	if o.Limit < 1 || o.Limit > 100 || o.Page < 1 || o.Page > int(^uint(0)>>1)/o.Limit {
		return errors.New("invalid pagination")
	}
	for _, status := range o.Statuses {
		switch status {
		case "pending", "waiting", "requested", "action_required", "queued", "in_progress", "completed", "failure", "success", "skipped", "neutral", "cancelled", "timed_out": //nolint:misspell // Gitea API spelling.
		default:
			return fmt.Errorf("unsupported run status %q; see tea run list --help", status)
		}
	}
	return nil
}

const maxRunScanPages = 50

// Runs lists a page of runs.
func (s Actions) Runs(ctx context.Context, opts RunListOptions) ([]Run, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if opts.Since != nil || opts.Until != nil {
		first, last := (opts.Page-1)*opts.Limit, opts.Page*opts.Limit
		result := []Run{}
		matched := 0
		for page := 1; page <= maxRunScanPages; page++ {
			scan := opts
			scan.Limit = 100
			scan.Page = page
			items, err := s.runsPage(ctx, scan)
			if err != nil {
				return nil, err
			}
			if len(items) == 0 {
				return result, nil
			}
			for _, item := range items {
				if item.StartedAt == "" {
					continue
				}
				started, err := time.Parse(time.RFC3339Nano, item.StartedAt)
				if err != nil {
					return nil, errors.New("server returned an invalid run start timestamp")
				}
				if opts.Since != nil && started.Before(*opts.Since) || opts.Until != nil && started.After(*opts.Until) {
					continue
				}
				if matched >= first {
					result = append(result, item)
				}
				matched++
				if matched >= last {
					return result, nil
				}
			}
		}
		return nil, fmt.Errorf("time filters scanned %d runs without exhausting the history; narrow the search with --branch, --workflow or --status", maxRunScanPages*100)
	}
	return s.runsPage(ctx, opts)
}

func (s Actions) runsPage(ctx context.Context, opts RunListOptions) ([]Run, error) {
	var response struct {
		Runs []runResponse `json:"workflow_runs"`
	}
	query := url.Values{"limit": {strconv.Itoa(opts.Limit)}, "page": {strconv.Itoa(opts.Page)}}
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
	// TODO(sdk): ListRepoActionsRunsOptions takes one status and ActionsWorkflowRun lacks created_at/updated_at.
	if err := getJSON(ctx, s.Client, endpoint+"?"+query.Encode(), &response); err != nil {
		return nil, err
	}
	result := make([]Run, 0, len(response.Runs))
	for _, item := range response.Runs {
		result = append(result, item.result())
	}
	return result, nil
}

// Run retrieves one Actions run.
func (s Actions) Run(ctx context.Context, id int64) (Run, error) {
	var response runResponse
	// TODO(sdk): ActionsWorkflowRun lacks created_at/updated_at.
	err := getJSON(ctx, s.Client, fmt.Sprintf("%sruns/%d", s.endpoint(), id), &response)
	return response.result(), err
}

// Rerun schedules another attempt, optionally restricted to failed jobs.
func (s Actions) Rerun(ctx context.Context, id int64, failedOnly bool) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		if failedOnly {
			return c.Actions.RerunRepoRunFailedJobs(ctx, owner, repo, id)
		}
		_, resp, err := c.Actions.RerunRepoRun(ctx, owner, repo, id)
		return resp, err
	})
}

// DeleteRun removes the selected run.
func (s Actions) DeleteRun(ctx context.Context, id int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Actions.DeleteRepoRun(ctx, owner, repo, id)
	})
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

// Watch polls with cancellation and reports only changed run state.
func (s Actions) Watch(ctx context.Context, id int64, interval time.Duration, changed func(Run) error) (Run, error) {
	if interval <= 0 {
		return Run{}, errors.New("watch interval must be positive")
	}
	last := Run{}
	for {
		current, err := s.Run(ctx, id)
		if err != nil {
			return Run{}, err
		}
		if current != last {
			if changed != nil {
				if err := changed(current); err != nil {
					return Run{}, err
				}
			}
			last = current
		}
		if completedActionStatus(current.Status) {
			return current, nil
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return Run{}, err
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
	return response.result(), err
}
