// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync/atomic"
)

// RunnerPolicy is configured by the server administrator, never by workflow YAML.
type RunnerPolicy struct {
	Repositories []string `json:"repositories"`
	Events       []string `json:"events,omitempty"`
	Workflows    []string `json:"workflows,omitempty"`
	Jobs         []string `json:"jobs,omitempty"`
	AllowedHosts []string `json:"allowed_hosts"`
}

type RunnerIsolation struct {
	AllowedHosts []string
}

var runnerPolicies atomic.Pointer[[]RunnerPolicy]

func SetRunnerPolicies(policies []RunnerPolicy) error {
	cloned := slices.Clone(policies)
	for i, policy := range cloned {
		if len(policy.Repositories) == 0 || policy.AllowedHosts == nil || (len(policy.Jobs) != 0 && len(policy.Workflows) == 0) {
			return errors.New("runner policies require repositories, allowed_hosts and workflow scopes for job selectors")
		}
		for _, repo := range policy.Repositories {
			if strings.Count(repo, "/") != 1 || strings.HasPrefix(repo, "/") || strings.HasSuffix(repo, "/") || strings.ContainsAny(repo, "*?[]\\ \t\r\n") {
				return errors.New("runner policy repositories must be exact owner/repository names")
			}
		}
		for _, values := range [][]string{policy.Events, policy.Workflows, policy.Jobs} {
			for _, value := range values {
				if value == "" || strings.ContainsAny(value, "*?[]\\/ \t\r\n") {
					return errors.New("runner policy selectors must be nonempty exact identifiers")
				}
			}
		}
		for _, host := range policy.AllowedHosts {
			if host == "" || strings.ToLower(host) != host || !strings.Contains(host, ".") || net.ParseIP(host) != nil || strings.ContainsFunc(host, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.')
			}) {
				return errors.New("runner policy allowed_hosts must contain exact lowercase DNS names, not wildcards or IP addresses")
			}
		}
		cloned[i] = RunnerPolicy{slices.Clone(policy.Repositories), slices.Clone(policy.Events), slices.Clone(policy.Workflows), slices.Clone(policy.Jobs), slices.Clone(policy.AllowedHosts)}
	}
	runnerPolicies.Store(&cloned)
	return nil
}

func (p RunnerPolicy) matches(repository, event, workflow, job string) bool {
	return slices.ContainsFunc(p.Repositories, func(repo string) bool { return strings.EqualFold(repo, repository) }) &&
		(len(p.Events) == 0 || slices.Contains(p.Events, event)) &&
		(len(p.Workflows) == 0 || slices.Contains(p.Workflows, workflow)) &&
		(len(p.Jobs) == 0 || slices.Contains(p.Jobs, job))
}

func mergeRunnerIsolation(current *RunnerIsolation, allowed []string) *RunnerIsolation {
	if current == nil {
		return &RunnerIsolation{AllowedHosts: slices.Clone(allowed)}
	}
	current.AllowedHosts = slices.DeleteFunc(current.AllowedHosts, func(host string) bool { return !slices.Contains(allowed, host) })
	return current
}

func RunnerIsolationForJob(ctx context.Context, job *ActionRunJob) (*RunnerIsolation, error) {
	policies := runnerPolicies.Load()
	if policies == nil || len(*policies) == 0 {
		return nil, nil //nolint:nilnil // No policy preserves existing runner behavior.
	}
	var isolation *RunnerIsolation
	seen := map[int64]bool{}
	for {
		if seen[job.ID] || len(seen) >= 32 {
			return nil, errors.New("RunnerIsolationForJob: cyclic reusable workflow ancestry")
		}
		seen[job.ID] = true
		if err := job.LoadAttributes(ctx); err != nil {
			return nil, err
		}
		for _, policy := range *policies {
			if policy.matches(job.Run.Repo.FullName(), job.Run.Event.Event(), job.Run.WorkflowID, job.JobID) {
				isolation = mergeRunnerIsolation(isolation, policy.AllowedHosts)
			}
		}
		if job.ParentJobID == 0 {
			return isolation, nil
		}
		parent, err := GetRunJobByRepoAndID(ctx, job.RepoID, job.ParentJobID)
		if err != nil {
			return nil, fmt.Errorf("RunnerIsolationForJob: load caller: %w", err)
		}
		job = parent
	}
}

func runnerIsolationAllowsRunner(ctx context.Context, job *ActionRunJob, runnerID int64) (bool, error) {
	isolation, err := RunnerIsolationForJob(ctx, job)
	if err != nil || isolation == nil {
		return isolation == nil, err
	}
	binding, err := GetBBTaskForRunner(ctx, runnerID)
	return binding != nil && binding.JobID == job.ID && !binding.Completed, err
}
