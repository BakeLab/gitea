// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/log"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// bbExecutorUnsupportedLabels explicitly identifies a runs-on label set that
// this deployment cannot execute. Unconfigured label sets remain queued.
type bbExecutorUnsupportedLabels struct {
	Labels []string `json:"labels"`
	Reason string   `json:"reason"`
}

func canonicalRunnerLabels(labels []string) []string {
	canonical := make([]string, 0, len(labels))
	seen := make(map[string]bool, len(labels))
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		canonical = append(canonical, label)
	}
	sort.Strings(canonical)
	return canonical
}

func runnerLabelSetKey(labels []string) string {
	return strings.Join(canonicalRunnerLabels(labels), "\x00")
}

func (config bbExecutorConfig) validateUnsupportedLabels() error {
	seen := make(map[string]bool, len(config.UnsupportedLabels))
	for _, unsupported := range config.UnsupportedLabels {
		reason := strings.TrimSpace(unsupported.Reason)
		if len(canonicalRunnerLabels(unsupported.Labels)) == 0 || reason == "" || len(reason) > 2048 {
			return errors.New("BB executor unsupported_labels entries require labels and a reason")
		}
		labels := make(map[string]bool, len(unsupported.Labels))
		for _, label := range unsupported.Labels {
			label = strings.ToLower(strings.TrimSpace(label))
			if label == "" || labels[label] {
				return errors.New("BB executor unsupported_labels entries require unique, non-empty labels")
			}
			labels[label] = true
		}
		key := runnerLabelSetKey(unsupported.Labels)
		if seen[key] {
			return errors.New("BB executor unsupported_labels contains duplicate label sets")
		}
		seen[key] = true
	}
	return nil
}

func (config bbExecutorConfig) unsupportedLabelReason(labels []string) (string, bool) {
	key := runnerLabelSetKey(labels)
	if key == "" {
		return "", false
	}
	for _, unsupported := range config.UnsupportedLabels {
		if runnerLabelSetKey(unsupported.Labels) == key {
			return strings.TrimSpace(unsupported.Reason), true
		}
	}
	return "", false
}

func bbProfileRunnerLabels(profile bbExecutorProfile) []string {
	labels := make([]string, 0, len(profile.Labels))
	for _, label := range profile.Labels {
		name, _, _ := strings.Cut(label, ":")
		labels = append(labels, name)
	}
	return labels
}

func (config bbExecutorConfig) matchingProfiles(job *actions_model.ActionRunJob) []int {
	var matching []int
	for profileIndex, profile := range config.Profiles {
		runner := &actions_model.ActionRunner{AgentLabels: bbProfileRunnerLabels(profile)}
		if runner.CanMatchLabels(job.RunsOn) {
			matching = append(matching, profileIndex)
		}
	}
	return matching
}

func (config bbExecutorConfig) skipConfiguredUnsupportedJobs(ctx context.Context) error {
	if len(config.UnsupportedLabels) == 0 {
		return nil
	}
	var candidates []*actions_model.ActionRunJob
	for page := 1; ; page++ {
		jobs, total, err := actions_model.FindJobQueueJobs(ctx, actions_model.JobQueueOptions{Status: actions_model.StatusWaiting}, page, 100)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if len(config.matchingProfiles(job)) == 0 {
				if _, unsupported := config.unsupportedLabelReason(job.RunsOn); unsupported {
					candidates = append(candidates, job)
				}
			}
		}
		if int64(page*100) >= total {
			break
		}
	}

	onlineRunners := make(map[int64][]*actions_model.ActionRunner)
	loadedRunners := make(map[int64]bool)
	failedRunners := make(map[int64]bool)
	for _, job := range candidates {
		reason, _ := config.unsupportedLabelReason(job.RunsOn)
		if !loadedRunners[job.RepoID] && !failedRunners[job.RepoID] {
			runners, err := db.Find[actions_model.ActionRunner](ctx, actions_model.FindRunnerOptions{
				RepoID:        job.RepoID,
				IsOnline:      optional.Some(true),
				IsDisabled:    optional.Some(false),
				WithAvailable: true,
				ListOptions:   db.ListOptions{ListAll: true},
			})
			if err != nil {
				failedRunners[job.RepoID] = true
				log.Warn("BB executor cannot inspect online runners for unsupported job %d; leaving it queued: %v", job.ID, err)
			} else {
				onlineRunners[job.RepoID] = runners
				loadedRunners[job.RepoID] = true
			}
		}
		if !loadedRunners[job.RepoID] {
			continue
		}
		if slices.ContainsFunc(onlineRunners[job.RepoID], func(runner *actions_model.ActionRunner) bool {
			return runner.CanMatchLabels(job.RunsOn)
		}) {
			continue
		}
		skipped, err := markConfiguredUnsupportedJobSkipped(ctx, job, reason)
		if err != nil {
			return err
		}
		if skipped {
			log.Info("BB executor skipped job %d with unsupported labels %v: %s", job.ID, job.RunsOn, reason)
		}
	}
	return nil
}

func markConfiguredUnsupportedJobSkipped(ctx context.Context, job *actions_model.ActionRunJob, reason string) (bool, error) {
	updated := *job
	updated.Status = actions_model.StatusSkipped
	updated.Stopped = timeutil.TimeStampNow()
	changed := false
	err := db.WithTx(ctx, func(ctx context.Context) error {
		count, err := actions_model.UpdateRunJob(ctx, &updated, builder.Eq{"status": actions_model.StatusWaiting, "task_id": 0}, "status", "stopped")
		if err != nil || count != 1 {
			return err
		}
		escapedReason := strings.ReplaceAll(reason, "`", "\\`")
		content := []byte(fmt.Sprintf("This job was skipped by the Gitea BB dispatcher because its runner labels are configured as unsupported in this deployment.\n\n**Labels:** `%s`\n\n**Reason:** %s\n", strings.Join(canonicalRunnerLabels(job.RunsOn), ", "), escapedReason))
		if err := actions_model.UpsertActionRunJobSummary(ctx, job.RepoID, job.RunID, job.RunAttemptID, job.ID, 0, actions_model.JobSummaryContentTypeMarkdown, content); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if changed {
		*job = updated
		notifyAndEmitFinishedJobs(ctx, []*actions_model.ActionRunJob{job})
	}
	return changed, nil
}
