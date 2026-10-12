// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaActionsQueuedRunLifecycle(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-actions-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	require.NoError(t, json.Unmarshal(execute("repo", "edit", "-R", repo.FullName, "--actions", "--json"), &repo))
	require.True(t, repo.ActionsEnabled)
	workflow := "name: gitea-cli fixture\non:\n  push:\n  workflow_dispatch:\njobs:\n  build:\n    runs-on: gt-disposable-no-runner\n    steps:\n      - run: echo gt fixture\n"
	payload, err := json.Marshal(map[string]string{"branch": repo.DefaultBranch, "content": base64.StdEncoding.EncodeToString([]byte(workflow)), "message": "Disposable workflow fixture"})
	require.NoError(t, err)
	deps.Streams.In = bytes.NewReader(payload)
	execute("api", "repos/"+repo.FullName+"/contents/.gitea/workflows/gt.yml", "--input", "-")
	var runs []services.Run
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		require.NoError(t, json.Unmarshal(execute("run", "list", "-R", repo.FullName, "--json"), &runs))
		if len(runs) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, runs, "workflow did not produce a disposable run")
	var status services.AccountStatus
	require.NoError(t, json.Unmarshal(execute("status", "-R", repo.FullName, "--json"), &status))
	require.True(t, status.RunsIncluded)
	require.Equal(t, repo.FullName, status.Repository)
	require.Empty(t, status.FailingRuns, "queued runs must not be reported as failures")
	var matching []services.Run
	require.NoError(t, json.Unmarshal(execute("run", "list", "-R", repo.FullName, "--branch", repo.DefaultBranch, "--event", "push", "--json"), &matching))
	require.NotEmpty(t, matching)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, json.Unmarshal(execute("run", "list", "-R", repo.FullName, "--workflow", "gt.yml", "--json"), &matching))
	require.NotEmpty(t, matching)

	require.NoError(t, json.Unmarshal(execute("run", "list", "-R", repo.FullName, "--branch", "does-not-exist", "--json"), &matching))
	require.Empty(t, matching)
	id := fmt.Sprint(runs[0].ID)
	var jobs []services.Job
	require.NoError(t, json.Unmarshal(execute("run", "jobs", id, "-R", repo.FullName, "--json"), &jobs))
	require.NotEmpty(t, jobs)
	require.Equal(t, "build", jobs[0].Name)
	var artifacts []services.Artifact
	require.NoError(t, json.Unmarshal(execute("run", "artifact", "list", id, "-R", repo.FullName, "--json"), &artifacts))
	require.Empty(t, artifacts)
	require.NotNil(t, artifacts)
	out.Reset()
	diagnostics.Reset()
	err = Execute(context.Background(), deps, []string{"run", "logs", id, "-R", repo.FullName, "--job", fmt.Sprint(jobs[0].ID), "--follow", "--interval=10ms", "--timeout=100ms"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Empty(t, out.String())
	var canceled services.Run
	require.NoError(t, json.Unmarshal(execute("run", "cancel", id, "-R", repo.FullName, "--yes", "--json"), &canceled))
	require.Equal(t, runs[0].ID, canceled.ID)
}
