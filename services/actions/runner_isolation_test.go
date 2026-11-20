// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerIsolationNetworkAndMounts(t *testing.T) {
	base := map[string]any{
		"runtime": "sandbox-vm", "network": "gateway", "fabric": true,
		"allowed_hosts": []any{"*", "other.internal"}, "required_internal_services": []any{"other.internal:443"},
		"vfs_mounts": map[string]any{"models": "/pool/private"}, "vfs_writable": []any{"models"}, "block_exports": []any{"disk"},
		"expose_ports": []any{22}, "bootstrap_secrets": []any{"DEPLOY"}, "ssh_authorized_keys": []any{"key"},
	}
	isolated, err := applyRunnerIsolation(base, &actions_model.RunnerIsolation{AllowedHosts: []string{"github.com"}})
	require.NoError(t, err)
	assert.Equal(t, []any{"github.com", "try.gitea.io"}, isolated["allowed_hosts"])
	assert.Equal(t, []any{"try.gitea.io:443"}, isolated["required_internal_services"])
	assert.Equal(t, false, isolated["fabric"])
	assert.NotContains(t, isolated, "vfs_mounts")
	assert.NotContains(t, isolated, "vfs_writable")
	assert.NotContains(t, isolated, "block_exports")
	assert.NotContains(t, isolated, "expose_ports")
	assert.NotContains(t, isolated, "bootstrap_secrets")
	assert.NotContains(t, isolated, "ssh_authorized_keys")
	assert.Equal(t, true, base["fabric"])
	configured := map[string]any{"models": "/pool/public"}
	require.NoError(t, validateBBDispatchMounts(configured, map[string]any{"models": "/pool/public"}))
	for _, value := range []any{nil, "bad", map[string]any{"models": "/pool/private"}, map[string]any{"other": "/pool/public"}} {
		require.Error(t, validateBBDispatchMounts(configured, value))
	}
}

func TestRunnerIsolationEnforcedAtClaimAndDelivery(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo.LoadOwner(ctx))
	policies := []actions_model.RunnerPolicy{{Repositories: []string{repo.FullName()}, Events: []string{"push"}, AllowedHosts: []string{"github.com"}}}
	require.NoError(t, actions_model.SetRunnerPolicies(policies))
	t.Cleanup(func() { require.NoError(t, actions_model.SetRunnerPolicies(nil)) })
	run := &actions_model.ActionRun{RepoID: 1, OwnerID: 2, WorkflowID: "renamed.yml", Event: "push", TriggerEvent: "push", TriggerUserID: 2, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{
		RunID: run.ID, RepoID: 1, OwnerID: 2, JobID: "renamed", Status: actions_model.StatusWaiting,
		RunsOn: []string{"ubuntu-latest"}, TokenPermissions: new(repo_model.MakeWriteAllActionsTokenPermissions()),
		WorkflowPayload: []byte("on: push\njobs:\n  renamed:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"),
	}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{Name: "ordinary", AgentLabels: []string{"ubuntu-latest"}, RepoID: 1}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task, _, err := actions_model.CreateTaskForRunner(ctx, runner)
	require.NoError(t, err)
	assert.Nil(t, task, "ordinary runner cannot bypass isolation using matching labels")
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
	binding := &actions_model.ActionBBTask{JobID: job.ID, RunnerID: runner.ID, RequestJSON: `{"sandbox":{"runtime":"sandbox-vm","network":"gateway","allowed_hosts":["*"]}}`}
	require.NoError(t, db.Insert(ctx, binding))
	task, _, err = actions_model.CreateTaskForRunner(ctx, runner)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.NoError(t, task.LoadAttributes(ctx))
	require.ErrorIs(t, validateIsolatedTask(ctx, task), errRunnerIsolation, "old unrestricted VM cannot receive a newly protected job")
	for _, scope := range []actions_model.BelongingScope{actions_model.BelongingScopeOwner(2), actions_model.BelongingScopeRepo(1)} {
		_, err := secret_model.InsertEncryptedSecret(ctx, scope, "BUSINESS", "must-not-deliver", "")
		require.NoError(t, err)
	}
	secrets, err := secret_model.GetSecretsOfTask(ctx, task)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"GITHUB_TOKEN": task.Token, "GITEA_TOKEN": task.Token}, secrets)
	permissions, err := actions_model.ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	assert.LessOrEqual(t, permissions.UnitAccessModes[unit.TypeCode], perm.AccessModeRead)
	assert.LessOrEqual(t, permissions.UnitAccessModes[unit.TypePackages], perm.AccessModeRead)
	assert.Equal(t, perm.AccessModeNone, permissions.UnitAccessModes[unit.TypeIssues])
	assert.False(t, permissions.IDToken)
	apiPermission, err := access_model.GetActionsUserRepoPermission(ctx, repo, user_model.NewActionsUser(), task.ID)
	require.NoError(t, err)
	assert.True(t, apiPermission.CanRead(unit.TypeCode))
	assert.False(t, apiPermission.CanWrite(unit.TypeCode))
	assert.False(t, apiPermission.CanWrite(unit.TypePackages))
	allowedOIDC, err := taskAllowsOIDCToken(ctx, task)
	require.NoError(t, err)
	assert.False(t, allowedOIDC)
	sandbox, err := isolatedJobSandbox(ctx, task.Job, map[string]any{"runtime": "sandbox-vm", "network": "gateway", "allowed_hosts": []any{"*"}})
	require.NoError(t, err)
	request, err := json.Marshal(map[string]any{"sandbox": sandbox})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(binding.ID).Cols("request_json").Update(&actions_model.ActionBBTask{RequestJSON: string(request)})
	require.NoError(t, err)
	require.NoError(t, validateIsolatedTask(ctx, task))
	// A renamed reusable callee retains its caller's explicitly selected policy.
	policies[0].Workflows, policies[0].Jobs = []string{"renamed.yml"}, []string{"renamed"}
	require.NoError(t, actions_model.SetRunnerPolicies(policies))
	child := &actions_model.ActionRunJob{RepoID: 1, RunID: run.ID, JobID: "callee", ParentJobID: job.ID}
	require.NoError(t, db.Insert(ctx, child))
	isolation, err := actions_model.RunnerIsolationForJob(ctx, child)
	require.NoError(t, err)
	require.NotNil(t, isolation)
	assert.Equal(t, []string{"github.com"}, isolation.AllowedHosts)
	// Webhook sync events and pull_request_target cannot escape a pull_request policy.
	policies[0].Events, policies[0].Workflows, policies[0].Jobs = []string{"pull_request"}, nil, nil
	require.NoError(t, actions_model.SetRunnerPolicies(policies))
	task.Job.Run.Event, task.Job.Run.TriggerEvent = "pull_request_sync", "pull_request_target"
	isolation, err = actions_model.RunnerIsolationForJob(ctx, task.Job)
	require.NoError(t, err)
	require.NotNil(t, isolation)
}

func TestBBDispatchRejectsLegacyExportsAndUnconfiguredMounts(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	run := &actions_model.ActionRun{RepoID: 1, Event: "workflow_dispatch"}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{
		RepoID: 1, RunID: run.ID, JobID: "eval",
		WorkflowPayload: []byte("on:\n  workflow_dispatch:\n    inputs:\n      config_json:\n        type: string\njobs:\n  eval:\n    runs-on: linux\n    steps:\n      - run: true\n"),
	}
	require.NoError(t, db.Insert(ctx, job))
	config := bbExecutorConfig{}
	profile := bbExecutorProfile{SandboxInput: "config_json.sandbox", Sandbox: map[string]any{
		"runtime": "sandbox-vm", "network": "gateway", "fabric": false,
		"vfs_mounts": map[string]any{"models": "/pool/public"},
	}}
	for _, extra := range []map[string]any{
		{"vfs_exports": map[string]any{"old": "/pool/public"}},
		{"vfs_mounts": map[string]any{"models": "/pool/private"}},
		{"allowed_hosts": []any{"*"}},
	} {
		extra["cpu"], extra["disk_mb"] = map[string]any{"vcpus": 2}, 4096
		cfg, err := json.Marshal(map[string]any{"sandbox": extra})
		require.NoError(t, err)
		payload, err := json.Marshal(map[string]any{"inputs": map[string]any{"config_json": string(cfg)}})
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).ID(run.ID).Cols("event_payload").Update(&actions_model.ActionRun{EventPayload: string(payload)})
		require.NoError(t, err)
		_, err = config.sandboxForJob(ctx, job, profile)
		require.ErrorIs(t, err, errBBDispatchSandbox)
	}
}
