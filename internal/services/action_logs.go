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
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Job identifies one workflow job without exposing server-specific metadata.
type Job struct {
	RunAttempt int64  `json:"run_attempt"`
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// Jobs lists every page of jobs in the selected run.
func (s Actions) Jobs(ctx context.Context, run int64) ([]Job, error) {
	return listAll(func(page int) ([]Job, error) { return s.jobsPage(ctx, run, 100, page) })
}

// JobsPage lists a page of jobs in the selected run.
func (s Actions) JobsPage(ctx context.Context, run int64, limit, page int) ([]Job, error) {
	return s.jobsPage(ctx, run, limit, page)
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

func (s Actions) jobsPage(ctx context.Context, run int64, limit, page int) ([]Job, error) {
	response, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ActionsWorkflowJobsResponse, *gitea.Response, error) {
		return c.Actions.ListRepoJobsByRun(ctx, owner, repo, run, gitea.ListRepoActionsJobsOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(response.Jobs, jobFrom), nil
}

func jobFrom(job *gitea.ActionsWorkflowJob) Job {
	return Job{RunAttempt: job.RunAttempt, ID: job.ID, Name: job.Name, Status: job.Status, Conclusion: job.Conclusion}
}

// JobLogs streams a job's plain-text logs within the selected instance.
func (s Actions) JobLogs(ctx context.Context, id int64) (io.ReadCloser, error) {
	// TODO(sdk): GetRepoRunJobLogs buffers the whole log; a reader variant would allow streaming.
	response, err := s.Client.Do(ctx, http.MethodGet, fmt.Sprintf("%sjobs/%d/logs", s.endpoint(), id), nil, nil)
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
			if api, ok := errors.AsType[*transport.APIError](err); ok && api.Status == http.StatusNotFound && offset == 0 && (current.Status == "queued" || current.Status == "waiting" || current.Status == "pending") {
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

// RunLogsArchive streams the server's ZIP archive without extracting its entries.
func (s Actions) RunLogsArchive(ctx context.Context, run int64) (io.ReadCloser, error) {
	return repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (io.ReadCloser, *gitea.Response, error) {
		return c.Actions.GetRepoRunLogsReader(ctx, owner, repo, run)
	})
}
