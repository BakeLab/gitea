// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

// TestGiteaDryRunChangesNothing runs writes of many kinds with --dry-run on a repository that has
// something for each to change, and checks on the instance that none did.
func TestGiteaDryRunChangesNothing(t *testing.T) {
	repo := newIntegrationRepo(t, "gt-dry-run")
	issue := repo.issue(t, "Open issue")
	pull := repo.pull(t, "feature", "Open pull request")
	repo.execute("branch", "create", "old", "-R", repo.FullName)
	repo.execute("label", "create", "bug", "--color", "ff0000", "-R", repo.FullName)
	repo.execute("release", "create", "v1", "--title", "One", "--notes", "n", "-R", repo.FullName)
	repo.execute("milestone", "create", "M1", "-R", repo.FullName)

	count := func(path string) string { return repo.api(path, "length") }
	issues, labels, releases, milestones := count("issues?state=all&type=issues"), count("labels"), count("releases"), count("milestones")

	for _, tc := range []struct {
		name   string
		args   []string
		method string
		check  func(t *testing.T)
	}{
		{"close an issue", []string{"issue", "close", issue}, "PATCH", func(t *testing.T) { require.Equal(t, "open", repo.api("issues/"+issue, ".state")) }},
		{"create an issue", []string{"issue", "create", "--title", "New", "--body", "b"}, "POST", func(t *testing.T) { require.Equal(t, issues, count("issues?state=all&type=issues")) }},
		{"comment on an issue", []string{"issue", "comment", "create", issue, "--body", "hello"}, "POST", func(t *testing.T) { require.Equal(t, "0", count("issues/"+issue+"/comments")) }},
		{"edit an issue", []string{"issue", "edit", issue, "--title", "Renamed"}, "PATCH", func(t *testing.T) { require.Equal(t, "Open issue", repo.api("issues/"+issue, ".title")) }},
		{"lock an issue", []string{"issue", "lock", issue}, "PUT", func(t *testing.T) { require.Equal(t, "false", repo.api("issues/"+issue, ".is_locked")) }},
		{"pin an issue", []string{"issue", "pin", issue}, "POST", func(t *testing.T) { require.Equal(t, "0", count("issues/pinned")) }},
		{"delete an issue", []string{"issue", "delete", issue}, "DELETE", func(t *testing.T) { require.Equal(t, "Open issue", repo.api("issues/"+issue, ".title")) }},
		{"add a dependency", []string{"issue", "edit", issue, "--add-blocks", pull}, "POST", func(t *testing.T) { require.Equal(t, "0", count("issues/"+issue+"/blocks")) }},
		{"create a label", []string{"label", "create", "feature", "--color", "00ff00"}, "POST", func(t *testing.T) { require.Equal(t, labels, count("labels")) }},
		{"delete a label", []string{"label", "delete", "bug"}, "DELETE", func(t *testing.T) { require.Equal(t, labels, count("labels")) }},
		{"delete a branch", []string{"branch", "delete", "old"}, "DELETE", func(t *testing.T) { require.Equal(t, "old", repo.api("branches/old", ".name")) }},
		{"protect a branch", []string{"branch", "protect", "old"}, "POST", func(t *testing.T) { require.Equal(t, "0", count("branch_protections")) }},
		{"merge a pull request", []string{"pr", "merge", pull, "--squash"}, "POST", func(t *testing.T) { require.Equal(t, "false", repo.api("pulls/"+pull, ".merged")) }},
		{"close a pull request", []string{"pr", "close", pull}, "PATCH", func(t *testing.T) { require.Equal(t, "open", repo.api("pulls/"+pull, ".state")) }},
		{"lock a pull request", []string{"pr", "lock", pull}, "PUT", func(t *testing.T) { require.Equal(t, "false", repo.api("issues/"+pull, ".is_locked")) }},
		{"delete a pull request", []string{"pr", "delete", pull}, "DELETE", func(t *testing.T) { require.Equal(t, "Open pull request", repo.api("pulls/"+pull, ".title")) }},
		{"create a release", []string{"release", "create", "v2", "--title", "Two"}, "POST", func(t *testing.T) { require.Equal(t, releases, count("releases")) }},
		{"delete a release", []string{"release", "delete", "v1"}, "DELETE", func(t *testing.T) { require.Equal(t, releases, count("releases")) }},
		{"create a milestone", []string{"milestone", "create", "M2"}, "POST", func(t *testing.T) { require.Equal(t, milestones, count("milestones")) }},
		{"close a milestone", []string{"milestone", "close", "M1"}, "PATCH", func(t *testing.T) { require.Equal(t, "open", repo.api("milestones", ".[0].state")) }},
		{"edit the repository", []string{"repo", "edit", "--description", "changed"}, "PATCH", func(t *testing.T) { require.Equal(t, "", repo.api("", ".description")) }},
		{"archive the repository", []string{"repo", "archive", repo.FullName}, "PATCH", func(t *testing.T) { require.Equal(t, "false", repo.api("", ".archived")) }},
		{"set a secret", []string{"secret", "set", "DEPLOY", "--body", "s3cret-value"}, "PUT", func(t *testing.T) { require.Equal(t, "0", count("actions/secrets")+"") }},
		{"create a webhook", []string{"webhook", "create", "https://example.com/hook", "--events", "push", "--secret", "signing-value"}, "POST", func(t *testing.T) { require.Equal(t, "0", count("hooks")) }},
		{"create a wiki page", []string{"wiki", "create", "Home", "--body", "hello"}, "POST", func(t *testing.T) { require.ErrorContains(t, repo.fails(t, "wiki", "view", "Home"), "not found") }},
		{"delete the repository", []string{"repo", "delete", repo.FullName}, "DELETE", func(t *testing.T) { require.Equal(t, repo.FullName, repo.api("", ".full_name")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{}, tc.args...)
			if args[0] != "repo" || args[1] == "edit" { // repo archive and delete name the repository themselves
				args = append(args, "-R", repo.FullName)
			}
			out := string(repo.execute(append(args, "--dry-run")...))
			require.True(t, strings.HasPrefix(out, "Dry run: would send "+tc.method+" "), out)
			require.Contains(t, out, "/api/v1/repos/"+repo.FullName, "the request names the real endpoint")
			require.NotContains(t, out, "s3cret-value", "a secret is not printed")
			require.NotContains(t, out, "signing-value", "a secret is not printed")
			tc.check(t)
		})
	}

	t.Run("the report as JSON", func(t *testing.T) {
		var report struct {
			DryRun   bool                      `json:"dry_run"`
			Requests []transport.DryRunRequest `json:"requests"`
		}
		require.NoError(t, json.Unmarshal(repo.execute("issue", "close", issue, "-R", repo.FullName, "--dry-run", "--json"), &report))
		require.True(t, report.DryRun)
		require.Len(t, report.Requests, 1)
		require.Equal(t, "PATCH", report.Requests[0].Method)
		var body struct{ State string }
		require.NoError(t, json.Unmarshal(report.Requests[0].Body, &body))
		require.Equal(t, "closed", body.State, "the body shows what would be sent")
		require.Equal(t, "PATCH", strings.TrimSpace(string(repo.execute("issue", "close", issue, "-R", repo.FullName, "--dry-run", "--jq", ".requests[0].method"))), "--jq filters the report")
		require.Equal(t, "PATCH\n", string(repo.execute("issue", "close", issue, "-R", repo.FullName, "--dry-run", "--template", `{{range .requests}}{{.method}}{{"\n"}}{{end}}`)))
	})

	t.Run("a secret in the report", func(t *testing.T) {
		out := string(repo.execute("secret", "set", "DEPLOY", "--body", "s3cret-value", "-R", repo.FullName, "--dry-run", "--json"))
		require.Contains(t, out, "[REDACTED]")
		require.NotContains(t, out, "s3cret-value")
	})

	t.Run("what changes the clone or the browser is refused", func(t *testing.T) {
		require.ErrorContains(t, repo.fails(t, "issue", "create", "--web", "--dry-run"), "--dry-run cannot be combined with --web")
		require.ErrorContains(t, Execute(context.Background(), *repo.deps, []string{"repo", "create", "x", "--clone", "--dry-run", "--no-input"}), "--dry-run cannot be combined with --source or --clone")
	})

	t.Run("without --dry-run the same command acts", func(t *testing.T) {
		repo.execute("issue", "close", issue, "-R", repo.FullName)
		require.Equal(t, "closed", repo.api("issues/"+issue, ".state"))
	})
}
