// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaTimeStopwatch(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)

	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-it-watch-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	var issue services.Issue
	require.NoError(t, json.Unmarshal(execute("issue", "create", "-R", repo.FullName, "--title", "Track me", "--json"), &issue))
	number := strconv.FormatInt(issue.Number, 10)
	fails := func(args ...string) error {
		t.Helper()
		return Execute(context.Background(), deps, append(args, "-R", repo.FullName, "--no-input"))
	}
	status := func() []services.Stopwatch {
		t.Helper()
		var watches []services.Stopwatch
		require.NoError(t, json.Unmarshal(execute("time", "status", "--json"), &watches))
		return watches
	}

	require.ErrorContains(t, fails("time", "stop", number), "no stopwatch is running")
	require.ErrorContains(t, fails("time", "cancel", number, "--yes"), "no stopwatch is running")
	require.ErrorContains(t, fails("time", "stop", "99999"), "issue #99999")
	require.ErrorContains(t, fails("time", "cancel", "99999", "--yes"), "issue #99999")

	execute("time", "start", number, "-R", repo.FullName, "--dry-run")
	require.Empty(t, status(), "a dry run starts nothing")

	execute("time", "start", number, "-R", repo.FullName)
	require.ErrorContains(t, fails("time", "start", number), "already running")
	watches := status()
	require.Len(t, watches, 1)
	require.Equal(t, repo.FullName, watches[0].Repository)
	require.Equal(t, issue.Number, watches[0].IssueNumber)

	time.Sleep(1100 * time.Millisecond) // Gitea logs whole seconds only, and nothing under one
	execute("time", "stop", number, "-R", repo.FullName)
	require.Empty(t, status())
	var records []services.TrackedTime
	require.NoError(t, json.Unmarshal(execute("time", "list", "--issue", number, "-R", repo.FullName, "--json"), &records))
	require.Len(t, records, 1, "stopping logs the elapsed time")
	require.Positive(t, records[0].Seconds)

	execute("time", "start", number, "-R", repo.FullName)
	execute("time", "cancel", number, "-R", repo.FullName, "--yes")
	require.Empty(t, status())
	require.NoError(t, json.Unmarshal(execute("time", "list", "--issue", number, "-R", repo.FullName, "--json"), &records))
	require.Len(t, records, 1, "canceling logs nothing")

	var other services.Issue
	require.NoError(t, json.Unmarshal(execute("issue", "create", "-R", repo.FullName, "--title", "Track me next", "--json"), &other))
	execute("time", "start", number, "-R", repo.FullName)
	time.Sleep(1100 * time.Millisecond)
	diagnostics.Reset()
	require.NoError(t, fails("time", "start", strconv.FormatInt(other.Number, 10)))
	require.Contains(t, diagnostics.String(), "Stopped the stopwatch on "+repo.FullName+"#"+number)
	watches = status()
	require.Len(t, watches, 1, "one stopwatch per user")
	require.Equal(t, other.Number, watches[0].IssueNumber)
	require.NoError(t, json.Unmarshal(execute("time", "list", "--issue", number, "-R", repo.FullName, "--json"), &records))
	require.Len(t, records, 2, "starting another logs the one it stops")
	execute("time", "cancel", strconv.FormatInt(other.Number, 10), "-R", repo.FullName, "--yes")
}
