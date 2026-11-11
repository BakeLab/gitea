// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/modules/log"
)

type bbExecutorExcludedWorkflow struct {
	Repository string
	Workflow   string
	Reason     string
}

// These workflows depend on GitHub-only integrations and must not be started
// by the Gitea dispatcher. The exclusions are applied only to Gitea jobs.
var bbExecutorExcludedWorkflows = []bbExecutorExcludedWorkflow{
	{
		Repository: "BakeLab/intel-xpu-backend-for-triton",
		Workflow:   "codeql.yml",
		Reason:     "CodeQL analysis is provided by GitHub Actions for this repository.",
	},
	{
		Repository: "BakeLab/rustfs",
		Workflow:   "functional-chain-health.yml",
		Reason:     "This workflow uses GitHub-only credentials and publishes results to GitHub.",
	},
}

func excludedWorkflowReason(job *actions_model.ActionRunJob) (string, bool) {
	if job == nil || job.Run == nil || job.Repo == nil {
		return "", false
	}
	for _, excluded := range bbExecutorExcludedWorkflows {
		if strings.EqualFold(job.Repo.FullName(), excluded.Repository) && job.Run.WorkflowID == excluded.Workflow {
			return excluded.Reason, true
		}
	}
	return "", false
}

func (config bbExecutorConfig) skipExcludedWorkflows(ctx context.Context) error {
	var afterID int64
	for {
		jobs, err := actions_model.FindWaitingJobsAfterID(ctx, afterID, 100)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			break
		}
		if err := actions_model.ActionJobList(jobs).LoadAttributes(ctx, true); err != nil {
			return err
		}
		for _, job := range jobs {
			reason, excluded := excludedWorkflowReason(job)
			if !excluded {
				continue
			}
			repository := strings.ReplaceAll(job.Repo.FullName(), "`", "\\`")
			workflow := strings.ReplaceAll(job.Run.WorkflowID, "`", "\\`")
			reason = strings.ReplaceAll(reason, "`", "\\`")
			content := []byte(fmt.Sprintf("This job was skipped by the Gitea BB dispatcher because its workflow is excluded from Gitea execution.\n\n**Repository:** `%s`\n\n**Workflow:** `%s`\n\n**Reason:** %s\n", repository, workflow, reason))
			skipped, err := markConfiguredJobSkipped(ctx, job, content)
			if err != nil {
				return err
			}
			if skipped {
				log.Info("BB executor skipped job %d for Gitea-excluded workflow %s/%s", job.ID, job.Repo.FullName(), job.Run.WorkflowID)
			}
		}
		afterID = jobs[len(jobs)-1].ID
	}
	return nil
}
