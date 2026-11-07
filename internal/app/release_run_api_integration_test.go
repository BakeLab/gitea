// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

func TestGiteaReleaseListFiltersAndOrder(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-release-list-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName)...) }
	// Gitea lists releases made in one second in no fixed order, so each is made in a second of its own.
	scoped("release", "create", "v1", "--title", "One", "--notes", "n")
	time.Sleep(1100 * time.Millisecond)
	scoped("release", "create", "v2", "--title", "Two", "--notes", "n", "--prerelease")
	time.Sleep(1100 * time.Millisecond)
	scoped("release", "create", "v3", "--title", "Three", "--notes", "n", "--draft")
	time.Sleep(1100 * time.Millisecond)
	scoped("release", "create", "v4", "--title", "Four", "--notes", "n")
	list := func(args ...string) string {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"release", "list", "-R", repo.FullName, "--json", "tag", "--jq", `[.[].tag] | join(",")`}, args...)))
		return strings.TrimSpace(out.String())
	}

	// Gitea orders releases by the date of their tag's commit, so one made later can come first: the
	// plain listing is checked by what it holds, the sorted one by its order.
	held := func(args ...string) string {
		tags := strings.Split(list(args...), ",")
		slices.Sort(tags)
		return strings.Join(tags, ",")
	}
	require.Equal(t, "v1,v2,v3,v4", held(), "drafts and pre-releases among them")
	require.Equal(t, "v1,v2,v4", held("--exclude-drafts"))
	require.Equal(t, "v1,v3,v4", held("--exclude-pre-releases"))
	require.Equal(t, "v1,v4", held("--exclude-drafts", "--exclude-pre-releases"))
	require.Equal(t, "v1,v2,v3,v4", list("--order", "asc"), "the oldest first, by when they were published or drafted")
	require.Equal(t, "v1,v4", list("--order", "asc", "--exclude-drafts", "--exclude-pre-releases"))
	require.Equal(t, "v4,v3,v2,v1", list("--order", "desc"))
	require.Equal(t, "v1,v2", list("--order", "asc", "--limit", "2"), "the limit cuts the oldest first")
	require.Equal(t, "v3,v4", list("--order", "asc", "--limit", "2", "--page", "2"))
	require.Equal(t, "", list("--order", "asc", "--limit", "2", "--page", "3"), "a page past the end is empty")

	require.ErrorContains(t, Execute(context.Background(), deps, []string{"release", "list", "-R", repo.FullName, "--order", "up"}), "order")
	text := string(scoped("release", "list", "--order", "asc", "--exclude-drafts"))
	require.NotContains(t, text, "Three", "a draft is left out of the table too")
	require.Less(t, strings.Index(text, "One"), strings.Index(text, "Four"))
}

func TestGiteaRunViewOneJobAndLogFailed(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-run-view-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	require.NoError(t, json.Unmarshal(execute("repo", "edit", "-R", repo.FullName, "--actions", "--json"), &repo))
	workflow := "name: gitea-cli fixture\non:\n  push:\n  workflow_dispatch:\njobs:\n  build:\n    runs-on: gt-disposable-no-runner\n    steps:\n      - run: echo build\n  test:\n    runs-on: gt-disposable-no-runner\n    steps:\n      - run: echo test\n"
	payload, err := json.Marshal(map[string]string{"branch": repo.DefaultBranch, "content": base64.StdEncoding.EncodeToString([]byte(workflow)), "message": "Disposable workflow fixture"})
	require.NoError(t, err)
	deps.Streams.In = bytes.NewReader(payload)
	execute("api", "repos/"+repo.FullName+"/contents/.gitea/workflows/gt.yml", "--input", "-")
	var runs []services.Run
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		require.NoError(t, json.Unmarshal(execute("run", "list", "-R", repo.FullName, "--json"), &runs))
		if len(runs) > 0 {
			break
		}
	}
	require.NotEmpty(t, runs, "the workflow did not produce a run")
	id := strconv.FormatInt(runs[0].ID, 10)
	run := func(args ...string) (string, string, error) {
		out.Reset()
		diagnostics.Reset()
		err := Execute(context.Background(), deps, append([]string{"run", "view", id, "-R", repo.FullName, "--no-input"}, args...))
		return out.String(), diagnostics.String(), err
	}

	var all, one services.Run
	require.NoError(t, json.Unmarshal(execute("run", "view", id, "-R", repo.FullName, "--json", "id,jobs"), &all))
	require.Len(t, all.Jobs, 2)
	got, _, err := run("--json", "id,jobs", "--job", "test")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(got), &one))
	require.Len(t, one.Jobs, 1, "one job of the two")
	require.Equal(t, "test", one.Jobs[0].Name)
	got, _, err = run("--json", "jobs", "--job", strconv.FormatInt(all.Jobs[0].ID, 10))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(got), &one))
	require.Equal(t, all.Jobs[0].ID, one.Jobs[0].ID, "a job by ID")
	text, _, err := run("--job", "build")
	require.NoError(t, err)
	require.Contains(t, text, "build")
	require.NotContains(t, text, "\ttest\t", "the other job is not listed")
	_, _, err = run("--job", "deploy")
	require.ErrorContains(t, err, `job "deploy" not found in run `+id)

	_, _, err = run("--log-failed")
	require.ErrorContains(t, err, "has no failed or canceled jobs", "nothing failed in a run that is waiting for a runner")

	execute("run", "cancel", id, "-R", repo.FullName, "--yes")
	_, _, err = run("--log-failed")
	require.ErrorContains(t, err, "none of the selected jobs has started", "the canceled jobs are selected, as by run logs --failed, and they never ran")
	_, _, err = run("--log", "--job", "build")
	require.ErrorContains(t, err, "none of the selected jobs has started")
	require.ErrorContains(t, func() error { _, _, err := run("--log-failed", "--job", "build"); return err }(), "--log-failed cannot be combined with --job")
}

func TestGiteaAPISlurpsThePages(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-slurp-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	for i := range 5 {
		execute("issue", "create", "-R", repo.FullName, "--title", fmt.Sprintf("Issue %d", i), "--body", "b")
	}
	endpoint := "repos/" + repo.FullName + "/issues?type=issues&state=open&limit=2"

	var slurped [][]struct{ Title string }
	require.NoError(t, json.Unmarshal(execute("api", endpoint, "--paginate", "--slurp"), &slurped), "one JSON document")
	require.Len(t, slurped, 3, "five issues in pages of two, as an array of three arrays")
	require.Len(t, slurped[0], 2)
	require.Len(t, slurped[1], 2)
	require.Len(t, slurped[2], 1)
	require.Equal(t, "Issue 4", slurped[0][0].Title)

	lines := strings.Split(strings.TrimSpace(string(execute("api", endpoint, "--paginate"))), "\n")
	require.Len(t, lines, 3, "without --slurp every page keeps its own line")

	require.ErrorContains(t, Execute(context.Background(), deps, []string{"api", endpoint, "--slurp"}), "--slurp requires --paginate")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"api", endpoint, "--paginate", "--slurp", "--jq", "."}), "--slurp cannot be combined with --jq")
}
