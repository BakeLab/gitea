// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"time"

	"code.gitea.io/sdk/gitea"
)

// formatDurationMinutes formats duration in a human-readable way
func formatDurationMinutes(started, completed time.Time) string {
	if started.IsZero() {
		return ""
	}

	end := completed
	if end.IsZero() {
		end = time.Now()
	}

	duration := end.Sub(started)
	if duration < time.Minute {
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	}
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, minutes)
}

// getWorkflowDisplayName returns the display title or falls back to path
func getWorkflowDisplayName(run *gitea.ActionWorkflowRun) string {
	if run.DisplayTitle != "" {
		return run.DisplayTitle
	}
	return run.Path
}

// ActionRunsList prints a list of workflow runs
func ActionRunsList(runs []*gitea.ActionWorkflowRun, output string) error {
	t := table{
		headers: []string{
			"ID",
			"Status",
			"Workflow",
			"Branch",
			"Event",
			"Started",
			"Duration",
		},
	}

	machineReadable := isMachineReadable(output)

	for _, run := range runs {
		workflowName := getWorkflowDisplayName(run)
		duration := formatDurationMinutes(run.StartedAt, run.CompletedAt)

		t.addRow(
			fmt.Sprintf("%d", run.ID),
			run.Status,
			workflowName,
			run.HeadBranch,
			run.Event,
			FormatTime(run.StartedAt, machineReadable),
			duration,
		)
	}

	if len(runs) == 0 {
		fmt.Printf("No workflow runs found\n")
		return nil
	}

	t.sort(0, true)
	return t.print(output)
}

// ActionRunDetails prints detailed information about a workflow run
func ActionRunDetails(run *gitea.ActionWorkflowRun) {
	workflowName := getWorkflowDisplayName(run)

	fmt.Printf("Run ID: %d\n", run.ID)
	fmt.Printf("Run Number: %d\n", run.RunNumber)
	fmt.Printf("Status: %s\n", run.Status)
	if run.Conclusion != "" {
		fmt.Printf("Conclusion: %s\n", run.Conclusion)
	}
	fmt.Printf("Workflow: %s\n", workflowName)
	fmt.Printf("Path: %s\n", run.Path)
	fmt.Printf("Branch: %s\n", run.HeadBranch)
	fmt.Printf("Event: %s\n", run.Event)
	fmt.Printf("Head SHA: %s\n", run.HeadSha)
	fmt.Printf("Started: %s\n", FormatTime(run.StartedAt, false))
	if !run.CompletedAt.IsZero() {
		fmt.Printf("Completed: %s\n", FormatTime(run.CompletedAt, false))
		duration := formatDurationMinutes(run.StartedAt, run.CompletedAt)
		fmt.Printf("Duration: %s\n", duration)
	}
	if run.RunAttempt > 1 {
		fmt.Printf("Attempt: %d\n", run.RunAttempt)
	}
	if run.Actor != nil {
		fmt.Printf("Triggered by: %s\n", run.Actor.UserName)
	}
	if run.HTMLURL != "" {
		fmt.Printf("URL: %s\n", run.HTMLURL)
	}
}

// ActionWorkflowJobsList prints a list of workflow jobs
func ActionWorkflowJobsList(jobs []*gitea.ActionWorkflowJob, output string) error {
	t := table{
		headers: []string{
			"ID",
			"Name",
			"Status",
			"Runner",
			"Started",
			"Duration",
		},
	}

	machineReadable := isMachineReadable(output)

	for _, job := range jobs {
		duration := formatDurationMinutes(job.StartedAt, job.CompletedAt)
		runner := job.RunnerName
		if runner == "" {
			runner = "-"
		}

		t.addRow(
			fmt.Sprintf("%d", job.ID),
			job.Name,
			job.Status,
			runner,
			FormatTime(job.StartedAt, machineReadable),
			duration,
		)
	}

	if len(jobs) == 0 {
		fmt.Printf("No jobs found\n")
		return nil
	}

	t.sort(0, true)
	return t.print(output)
}

// WorkflowsList prints a list of workflow files with active status
func WorkflowsList(workflows []*gitea.ContentsResponse, activeStatus map[string]bool, output string) error {
	t := table{
		headers: []string{
			"Active",
			"Name",
			"Path",
		},
	}

	machineReadable := isMachineReadable(output)

	for _, workflow := range workflows {
		// Check if this workflow file is active (has runs)
		isActive := activeStatus[workflow.Name]
		activeIndicator := formatBoolean(isActive, !machineReadable)

		t.addRow(
			activeIndicator,
			workflow.Name,
			workflow.Path,
		)
	}

	if len(workflows) == 0 {
		fmt.Printf("No workflows found\n")
		return nil
	}

	t.sort(1, true) // Sort by name column
	return t.print(output)
}
