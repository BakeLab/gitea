// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Job identifies one workflow job without exposing server-specific metadata. StartedAt and
// CompletedAt are nil until the job starts and completes.
type Job struct {
	RunAttempt  int64    `json:"run_attempt"`
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Conclusion  string   `json:"conclusion"`
	URL         string   `json:"url"`
	Labels      []string `json:"labels"`
	CreatedAt   string   `json:"created_at"`
	StartedAt   *string  `json:"started_at"`
	CompletedAt *string  `json:"completed_at"`
	Steps       []Step   `json:"steps"`
}

// Step is one step of a job. StartedAt and CompletedAt are nil until the step starts and completes.
type Step struct {
	Number      int64   `json:"number"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	Conclusion  string  `json:"conclusion"`
	StartedAt   *string `json:"started_at"`
	CompletedAt *string `json:"completed_at"`
}

// Completed reports whether the job finished, whatever its conclusion.
func (j Job) Completed() bool { return completedActionStatus(j.Status) }

// Jobs lists every job in the selected run.
func (s Actions) Jobs(ctx context.Context, run int64) ([]Job, error) {
	jobs := []Job{}
	for page := 1; page <= maxListPages; page++ {
		window, err := s.JobsPage(ctx, run, 100, page)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, window.Items...)
		if !window.More || len(window.Items) == 0 {
			return jobs, nil
		}
	}
	return nil, fmt.Errorf("listing exceeded %d pages", maxListPages)
}

// JobsPage lists a page of jobs in the selected run.
func (s Actions) JobsPage(ctx context.Context, run int64, limit, page int) (Page[Job], error) {
	window, err := listPage(limit, page, func(limit, page int) ([]*gitea.ActionsWorkflowJob, http.Header, error) {
		response, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsWorkflowJobsResponse, *gitea.Response, error) {
			owner, repo, err := splitRepository(s.Repository)
			if err != nil {
				return nil, nil, err
			}
			return c.Actions.ListRepoJobsByRun(ctx, owner, repo, run, gitea.ListRepoActionsJobsOptions{ListOptions: listOptions(limit, page)})
		})
		if err != nil {
			return nil, headerOf(resp), err
		}
		return response.Jobs, headerOf(resp), nil
	})
	if err != nil {
		return Page[Job]{}, err
	}
	return Page[Job]{Items: convertAll(window.Items, jobFrom), More: window.More, Total: window.Total, Counted: window.Counted}, nil
}

// WaitForFirstJob polls a freshly dispatched run until the server has created its first job.
func (s Actions) WaitForFirstJob(ctx context.Context, run int64, interval time.Duration) (Job, error) {
	if interval <= 0 {
		return Job{}, errors.New("job polling interval must be positive")
	}
	for {
		jobs, err := s.Jobs(ctx, run)
		if err != nil {
			return Job{}, err
		}
		if len(jobs) > 0 {
			return jobs[0], nil
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return Job{}, err
		}
	}
}

func jobFrom(job *gitea.ActionsWorkflowJob) Job {
	return Job{
		RunAttempt: job.RunAttempt, ID: job.ID, Name: job.Name, Status: job.Status, Conclusion: job.Conclusion,
		URL: job.HTMLURL, Labels: append([]string{}, job.Labels...), CreatedAt: timestamp(job.CreatedAt),
		StartedAt: actionTime(job.StartedAt), CompletedAt: actionTime(job.CompletedAt), Steps: convertAll(job.Steps, stepFrom),
	}
}

func stepFrom(step *gitea.ActionsWorkflowStep) Step {
	return Step{
		Number: step.Number, Name: step.Name, Status: step.Status, Conclusion: step.Conclusion,
		StartedAt: actionTime(step.StartedAt), CompletedAt: actionTime(step.CompletedAt),
	}
}

// actionTime is runTime for SDK times.
func actionTime(t time.Time) *string {
	if t.Unix() <= 0 {
		return nil
	}
	value := timestamp(t)
	return &value
}

// Started reports whether the job has started, so that it has logs.
func (j Job) Started() bool { return j.StartedAt != nil }

// ErrJobNotStarted reports a job without logs because no runner has started it.
var ErrJobNotStarted = errors.New("job not started")

// JobLogs streams a job's plain-text logs within the selected instance. A job that has not
// started fails with ErrJobNotStarted.
func (s Actions) JobLogs(ctx context.Context, id int64) (io.ReadCloser, error) {
	// TODO(sdk): GetRepoRunJobLogs buffers the whole log; a reader variant would allow streaming.
	response, err := s.Client.Do(ctx, http.MethodGet, fmt.Sprintf("%sjobs/%d/logs", s.endpoint(), id), nil, nil)
	if api, ok := errors.AsType[*transport.APIError](err); ok && api.Status == http.StatusNotFound && strings.Contains(api.Message, "not started") {
		return nil, fmt.Errorf("job %d: %w", id, ErrJobNotStarted)
	}
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// FollowJobLogs emits only newly appended bytes and stops when the job completes.
// A shorter snapshot fails rather than silently mixing logs from different attempts.
func (s Actions) FollowJobLogs(ctx context.Context, run, id int64, interval time.Duration, emit func([]byte) error) error {
	if interval <= 0 {
		return fmt.Errorf("log polling interval must be positive")
	}
	var offset int64
	var attempt int64
	var previousHash []byte
	for {
		jobs, err := s.Jobs(ctx, run)
		if err != nil {
			return err
		}
		var current *Job
		for i := range jobs {
			if jobs[i].ID == id {
				current = &jobs[i]
				break
			}
		}
		if current == nil {
			return fmt.Errorf("job %d no longer belongs to run %d", id, run)
		}
		if attempt != 0 && current.RunAttempt != attempt {
			return fmt.Errorf("job attempt changed while following logs; restart with the current attempt")
		}
		attempt = current.RunAttempt
		logs, err := s.JobLogs(ctx, id)
		if err != nil {
			api, ok := errors.AsType[*transport.APIError](err)
			if offset == 0 && !completedActionStatus(current.Status) && (errors.Is(err, ErrJobNotStarted) || ok && api.Status == http.StatusNotFound) {
				if err := sleepCtx(ctx, interval); err != nil {
					return err
				}
				continue
			}
			return err
		}
		err = func() error {
			defer logs.Close()
			snapshotHash := sha256.New()
			if _, err := io.CopyN(snapshotHash, logs, offset); err != nil {
				return fmt.Errorf("job log snapshot became shorter; select the current run attempt: %w", err)
			}
			if offset > 0 && !bytes.Equal(previousHash, snapshotHash.Sum(nil)) {
				return errors.New("job log contents changed while following; restart with the current attempt")
			}
			buffer := make([]byte, 32*1024)
			for {
				n, err := logs.Read(buffer)
				if n > 0 {
					if emitErr := emit(buffer[:n]); emitErr != nil {
						return emitErr
					}
					_, _ = snapshotHash.Write(buffer[:n])
					offset += int64(n)
				}
				if err == io.EOF {
					previousHash = snapshotHash.Sum(nil)
					return nil
				}
				if err != nil {
					return err
				}
			}
		}()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if completedActionStatus(current.Status) {
			return nil
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
	}
}

// RunLogsArchive streams the server's ZIP archive without extracting its entries. A run without
// logs, such as one that never started, is reported as not found.
func (s Actions) RunLogsArchive(ctx context.Context, run int64) (io.ReadCloser, error) {
	body, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (io.ReadCloser, *gitea.Response, error) {
		return c.Actions.GetRepoRunLogsReader(ctx, owner, repo, run)
	})
	return body, Classify(err, "logs of run", strconv.FormatInt(run, 10), s.Repository)
}
