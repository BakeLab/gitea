// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"reflect"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
)

var errRunnerIsolation = errors.New("runner isolation policy does not match this BB VM; rerun after configuring an isolated profile")

var errBBDispatchSandbox = errors.New("invalid dispatch sandbox configuration")

func failIsolatedTask(ctx context.Context, task *actions_model.ActionTask) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if err := actions_model.StopTask(ctx, task.ID, actions_model.StatusFailure); err != nil {
			return err
		}
		return actions_model.UpsertActionRunJobSummary(ctx, task.RepoID, task.Job.RunID, task.Job.RunAttemptID, task.JobID, 0, actions_model.JobSummaryContentTypeMarkdown, []byte(errRunnerIsolation.Error()))
	}); err != nil {
		return err
	}
	job, err := actions_model.GetRunJobByRepoAndID(ctx, task.RepoID, task.JobID)
	if err != nil {
		return err
	}
	notifyAndEmitFinishedJobs(ctx, []*actions_model.ActionRunJob{job})
	return nil
}

func isolatedJobSandbox(ctx context.Context, job *actions_model.ActionRunJob, sandbox map[string]any) (map[string]any, error) {
	isolation, err := actions_model.RunnerIsolationForJob(ctx, job)
	if err != nil || isolation == nil {
		return sandbox, err
	}
	return applyRunnerIsolation(sandbox, isolation)
}

func applyRunnerIsolation(sandbox map[string]any, isolation *actions_model.RunnerIsolation) (map[string]any, error) {
	sandbox = bbMergeSandbox(sandbox, nil)
	sandbox["runtime"], sandbox["network"], sandbox["fabric"] = "sandbox-vm", "gateway", false
	for _, field := range []string{"vfs_mounts", "vfs_writable", "block_exports", "expose_ports", "bootstrap_secrets", "ssh_authorized_keys"} {
		delete(sandbox, field)
	}
	hosts := make([]any, 0, len(isolation.AllowedHosts))
	for _, host := range isolation.AllowedHosts {
		hosts = append(hosts, host)
	}
	sandbox["allowed_hosts"] = hosts
	delete(sandbox, "required_internal_services")
	origin, err := bbHTTPS(setting.AppURL)
	if err != nil {
		return nil, err
	}
	return bbGiteaSandbox(sandbox, origin)
}

func validateIsolatedTask(ctx context.Context, task *actions_model.ActionTask) error {
	isolation, err := actions_model.RunnerIsolationForJob(ctx, task.Job)
	if err != nil || isolation == nil {
		return err
	}
	binding, err := actions_model.GetBBTaskForRunner(ctx, task.RunnerID)
	if err != nil {
		return err
	}
	if binding == nil || binding.Completed || binding.JobID != task.JobID {
		return errRunnerIsolation
	}
	var request struct {
		Sandbox map[string]any `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(binding.RequestJSON), &request); err != nil {
		return errRunnerIsolation
	}
	expected, err := applyRunnerIsolation(request.Sandbox, isolation)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, request.Sandbox) {
		return errRunnerIsolation
	}
	return nil
}

func validateBBDispatchMounts(configured, requested any) error {
	allowed, _ := configured.(map[string]any)
	mounts, ok := requested.(map[string]any)
	if !ok {
		return errors.New("validateBBDispatchMounts: mounts must be an object")
	}
	for name, value := range mounts {
		path, ok := value.(string)
		if !ok || path == "" || allowed[name] != path {
			return errors.New("validateBBDispatchMounts: dispatch mounts must exactly match administrator-configured profile mounts")
		}
	}
	return nil
}
