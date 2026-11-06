// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

// registerIntegrationRunner registers a runner that never runs, the way act_runner does it: with a
// registration token over the connect protocol that runners speak, here as JSON.
func registerIntegrationRunner(t *testing.T, instance, token, name string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "token": token, "version": "v0.2.11", "labels": []string{"ubuntu-latest:host", "docker"}})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, instance+"/api/actions/runner.v1.RunnerService/Register", bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode, "the runner registers with the token that gt printed")
}

func TestGiteaRunnerLifecycleInEveryScope(t *testing.T) {
	gitea, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-runner-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	org := fmt.Sprintf("gt-runner-org-%d", time.Now().UnixNano())
	execute("org", "create", org)
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"org", "delete", org, "--yes"}); err != nil {
			t.Errorf("organization cleanup: %v", err)
		}
	})

	for _, scope := range []struct {
		name  string
		flags []string
	}{
		{"repository", []string{"-R", repo.FullName}},
		{"organization", []string{"--org", org}},
		{"account", []string{"--account"}},
		{"instance", []string{"--global"}},
	} {
		t.Run(scope.name, func(t *testing.T) {
			run := func(args ...string) []byte {
				return execute(append(append([]string{"runner"}, args...), scope.flags...)...)
			}
			runnerName := fmt.Sprintf("gt-itest-%s-%d", scope.name, time.Now().UnixNano())

			var registration struct{ Token string }
			require.NoError(t, json.Unmarshal(run("token", "--json"), &registration))
			require.NotEmpty(t, registration.Token)
			require.Equal(t, registration.Token+"\n", string(run("token")), "the token alone, for $(gt runner token)")
			registerIntegrationRunner(t, gitea.url, registration.Token, runnerName)

			var runners []services.Runner
			require.NoError(t, json.Unmarshal(run("list", "--json"), &runners))
			var runner services.Runner
			for _, item := range runners {
				if item.Name == runnerName {
					runner = item
				}
			}
			require.Equal(t, runnerName, runner.Name, "the registered runner is listed")
			require.Positive(t, runner.ID)
			require.False(t, runner.Disabled)
			require.ElementsMatch(t, []string{"ubuntu-latest:host", "docker"}, runner.Labels)
			id := strconv.FormatInt(runner.ID, 10)
			t.Cleanup(func() {
				_ = Execute(context.Background(), deps, append([]string{"runner", "delete", id, "--yes"}, scope.flags...))
			})

			var viewed services.Runner
			require.NoError(t, json.Unmarshal(run("view", id, "--json"), &viewed))
			require.Equal(t, runner, viewed)
			require.Contains(t, string(run("view", id)), runnerName)

			require.NoError(t, json.Unmarshal(run("disable", id, "--json"), &viewed))
			require.True(t, viewed.Disabled)
			require.Contains(t, diagnosedRunner(t, &deps, diagnostics, append([]string{"runner", "disable", id}, scope.flags...)), "is already disabled")
			require.NoError(t, json.Unmarshal(run("list", "--disabled", "--json", "id"), &runners))
			require.True(t, slices.ContainsFunc(runners, func(item services.Runner) bool { return item.ID == runner.ID }), "a disabled runner is listed as disabled")
			require.NoError(t, json.Unmarshal(run("list", "--enabled", "--json", "id"), &runners))
			for _, item := range runners {
				require.NotEqual(t, runner.ID, item.ID, "a disabled runner is not among the enabled ones")
			}

			require.NoError(t, json.Unmarshal(run("enable", id, "--json"), &viewed))
			require.False(t, viewed.Disabled)

			require.Error(t, Execute(context.Background(), deps, append([]string{"runner", "delete", id, "--no-input"}, scope.flags...)), "without --yes and a terminal there is nobody to ask")
			run("delete", id, "--yes")
			require.NoError(t, json.Unmarshal(run("list", "--json"), &runners))
			for _, item := range runners {
				require.NotEqual(t, runner.ID, item.ID, "the runner is gone")
			}
			err := Execute(context.Background(), deps, append([]string{"runner", "view", id, "--no-input"}, scope.flags...))
			require.ErrorContains(t, err, "runner "+id)
		})
	}
}

// diagnosedRunner runs gt, requires success and returns what it wrote to stderr.
func diagnosedRunner(t *testing.T, deps *Dependencies, diagnostics *bytes.Buffer, args []string) string {
	t.Helper()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), *deps, append(args, "--no-input")))
	return diagnostics.String()
}
