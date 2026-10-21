// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaPullEditLifecycle(t *testing.T) {
	gitea, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	out.Reset()
	diagnostics.Reset()
	identityErr := Execute(context.Background(), deps, []string{"issue", "create", "-R", "integration/nonexistent", "--title", "Must not submit", "--body", "Body", "--user", "gt-deliberately-wrong-account", "--no-input"})
	require.ErrorContains(t, identityErr, "does not belong to the selected account")
	require.Equal(t, clierr.Authentication, clierr.ExitCode(identityErr))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
	var repo services.Repository
	name := fmt.Sprintf("gt-pr-edit-%d", time.Now().UnixNano())
	require.NoError(t, json.Unmarshal(execute("repo", "create", name, "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	execute("branch", "create", "feature", "-R", repo.FullName)
	deps.Streams.In = strings.NewReader("{\"branch\":\"feature\",\"content\":\"dGVzdAo=\",\"message\":\"Disposable PR change\"}")
	execute("api", "repos/"+repo.FullName+"/contents/change.txt", "--input", "-")
	var label services.Label
	require.NoError(t, json.Unmarshal(execute("label", "create", "review", "--color", "00ff00", "-R", repo.FullName, "--json"), &label))
	exportPath := filepath.Join(t.TempDir(), "labels.txt")
	execute("label", "export", "-R", repo.FullName, "--output", exportPath)
	exported, err := os.ReadFile(exportPath)
	require.NoError(t, err)
	require.Contains(t, string(exported), "#00ff00 review\n")
	jsonExportPath := exportPath + ".json"
	execute("label", "export", "-R", repo.FullName, "--output", jsonExportPath, "--json")
	jsonExport, err := os.ReadFile(jsonExportPath)
	require.NoError(t, err)
	var exportedLabels []services.Label
	require.NoError(t, json.Unmarshal(jsonExport, &exportedLabels))
	require.Contains(t, exportedLabels, label)

	var milestone services.Milestone
	require.NoError(t, json.Unmarshal(execute("milestone", "create", "--title", "Disposable", "-R", repo.FullName, "--json"), &milestone))
	var dated services.Milestone
	require.NoError(t, json.Unmarshal(execute("milestone", "edit", fmt.Sprint(milestone.ID), "-R", repo.FullName, "--deadline", "2028-01-15", "--json"), &dated))
	require.NotNil(t, dated.Deadline)
	preserved := *dated.Deadline
	for _, verb := range []string{"view", "close", "reopen"} {
		var selected services.Milestone
		require.NoError(t, json.Unmarshal(execute("milestone", verb, milestone.Title, "--by-name", "-R", repo.FullName, "--json"), &selected))
		require.Equal(t, milestone.ID, selected.ID)
		require.NotNil(t, selected.Deadline)
		require.Equal(t, preserved, *selected.Deadline)
		if verb == "close" {
			require.Equal(t, "closed", selected.State)
		} else {
			require.Equal(t, "open", selected.State)
		}
	}
	require.NoError(t, json.Unmarshal(execute("milestone", "edit", fmt.Sprint(milestone.ID), "-R", repo.FullName, "--deadline=", "--json"), &dated))
	require.Nil(t, dated.Deadline)
	var secondMilestone services.Milestone
	require.NoError(t, json.Unmarshal(execute("milestone", "create", "--title", "Bulk milestone", "-R", repo.FullName, "--json"), &secondMilestone))
	for _, verb := range []string{"close", "reopen"} {
		var changed []services.Milestone
		require.NoError(t, json.Unmarshal(execute("milestone", verb, milestone.Title, secondMilestone.Title, "--by-name", "-R", repo.FullName, "--json"), &changed))
		require.Len(t, changed, 2)
		require.Equal(t, milestone.ID, changed[0].ID)
		require.Equal(t, secondMilestone.ID, changed[1].ID)
		for _, item := range changed {
			if verb == "close" {
				require.Equal(t, "closed", item.State)
			} else {
				require.Equal(t, "open", item.State)
			}
		}
	}
	var pull services.Pull
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, "", "Editor PR body", "pr", "create", "-R", repo.FullName, "--title", "[wip] Initial", "--head", "feature", "--base", repo.DefaultBranch, "--deadline", "2028-01-15", "--assignee", strings.SplitN(repo.FullName, "/", 2)[0], "--label", "review", "--milestone", milestone.Title), &pull))
	require.Equal(t, []services.Label{label}, pull.Labels)
	require.Equal(t, []string{strings.SplitN(repo.FullName, "/", 2)[0]}, pull.Assignees)
	require.Equal(t, &services.IssueMilestone{ID: milestone.ID, Title: milestone.Title}, pull.Milestone)
	require.NotNil(t, pull.Deadline)
	require.Equal(t, "2028-01-15T23:59:59Z", *pull.Deadline)
	n := fmt.Sprint(pull.Number)
	require.Equal(t, "[]\n", string(execute("pr", "attachment", "list", n, "-R", repo.FullName, "--json")))
	verifyResourceBrowser(t, deps, pull.URL, "pr", "view", n, "--repo", repo.FullName)
	verifyBodyEditorIntegration(t, deps, "pr", repo.FullName, n, pull.Body)
	verifyIssueAttachmentLifecycle(t, deps, "pr", repo.FullName, n)
	var pullStatus services.PullStatus
	require.NoError(t, json.Unmarshal(execute("pr", "status", "-R", repo.FullName, "--json"), &pullStatus))
	require.Len(t, pullStatus.Authored, 1)
	require.Equal(t, pull.Number, pullStatus.Authored[0].Number)
	require.Equal(t, repo.FullName, pullStatus.Authored[0].Repository)
	require.Len(t, pullStatus.Assigned, 1)
	require.Equal(t, pull.Number, pullStatus.Assigned[0].Number)
	require.Equal(t, repo.FullName, pullStatus.Assigned[0].Repository)
	require.Empty(t, pullStatus.ReviewRequested)
	var filtered []services.Pull
	require.NoError(t, json.Unmarshal(execute("pr", "list", "-R", repo.FullName, "--author", strings.SplitN(repo.FullName, "/", 2)[0], "--base", repo.DefaultBranch, "--sort", "oldest", "--milestone", fmt.Sprint(milestone.ID), "--label", "review", "--json"), &filtered))
	require.Len(t, filtered, 1)
	require.Equal(t, pull.Number, filtered[0].Number)
	require.Equal(t, strings.SplitN(repo.FullName, "/", 2)[0], filtered[0].Author)
	require.NotEmpty(t, filtered[0].UpdatedAt)
	require.NoError(t, json.Unmarshal(execute("pr", "list", "-R", repo.FullName, "--base", "gt-nonexistent-target-branch", "--json"), &filtered))
	require.Empty(t, filtered)
	var head struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	require.NoError(t, json.Unmarshal(execute("api", "repos/"+repo.FullName+"/pulls/"+n), &head))
	require.NotEmpty(t, head.Head.SHA)
	execute("repo", "edit", "-R", repo.FullName, "--private=false")
	workingTree := filepath.Join(t.TempDir(), "checkout")
	runner := git.System{In: deps.Streams.In, ErrOut: diagnostics}
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", repo.FullName, workingTree, "--depth", "1", "--no-input", "--json"}))
	var cloned services.Repository
	require.NoError(t, json.Unmarshal(out.Bytes(), &cloned))
	require.Equal(t, repo.ID, cloned.ID)
	require.Equal(t, repo.FullName, cloned.FullName)
	shallow, err := runner.Output(context.Background(), workingTree, false, "rev-parse", "--is-shallow-repository")
	require.NoError(t, err)
	require.Equal(t, "true", shallow)

	var checkout git.CheckoutResult
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", n, "-R", repo.FullName, "--cwd", workingTree, "--no-input", "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &checkout))
	require.Empty(t, diagnostics.String(), "checkout fetches quietly")
	require.Equal(t, "feature", checkout.Branch, "the local branch takes the head branch name")
	require.Equal(t, head.Head.SHA, checkout.Commit)
	actual, err := runner.Output(context.Background(), workingTree, false, "rev-parse", "HEAD")
	require.NoError(t, err)
	require.Equal(t, head.Head.SHA, actual)
	execute("repo", "edit", "-R", repo.FullName, "--private=true")
	for _, state := range []string{"failure", "success"} {
		payload, err := json.Marshal(map[string]string{"state": state, "context": "gt-check", "description": "Disposable check"})
		require.NoError(t, err)
		deps.Streams.In = bytes.NewReader(payload)
		execute("api", "repos/"+repo.FullName+"/statuses/"+head.Head.SHA, "--input", "-")
	}
	var checks []services.Check
	require.NoError(t, json.Unmarshal(execute("pr", "checks", n, "-R", repo.FullName, "--json"), &checks))
	require.Len(t, checks, 1)
	require.Equal(t, "success", checks[0].State)
	require.Equal(t, "gt-check", checks[0].Name)
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--set-labels", "repo:review", "--json"), &pull))
	require.Equal(t, []services.Label{label}, pull.Labels)
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--set-labels=", "--json"), &pull))
	require.Empty(t, pull.Labels)
	require.NotNil(t, pull.Labels)
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--ready", "--body", "Updated", "--milestone", milestone.Title, "--allow-maintainer-edit=false", "--json"), &pull))
	require.Equal(t, "Initial", pull.Title)
	require.Equal(t, "Updated", pull.Body)
	execute("pr", "edit", n, "-R", repo.FullName, "--milestone=0", "--body=", "--set-assignees=", "--draft", "--deadline=")
	var raw struct {
		Milestone   *struct{ ID int64 }
		Assignees   []any
		Title, Body string
		Deadline    *string `json:"due_date"`
	}
	require.NoError(t, json.Unmarshal(execute("api", "repos/"+repo.FullName+"/pulls/"+n), &raw))
	require.Nil(t, raw.Milestone)
	require.Nil(t, raw.Deadline)
	require.Empty(t, raw.Assignees)
	require.Empty(t, raw.Body)
	require.Equal(t, "WIP: Initial", raw.Title)
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--deadline", "2028-02-01T00:30:00+02:00", "--json"), &pull))
	assertEditedDeadline(t, pull.Deadline, "2028-02-01")
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--deadline=", "--json"), &pull))
	require.Nil(t, pull.Deadline)
	var issue services.Issue
	owner, _, _ := strings.Cut(repo.FullName, "/")
	require.NoError(t, json.Unmarshal(execute("issue", "create", "-R", repo.FullName, "--title", "Deadline fixture", "--deadline", "2028-01-15", "--ref", "feature", "--assignee", owner, "--milestone", milestone.Title, "--json"), &issue))
	require.NotNil(t, issue.Deadline)
	require.Equal(t, "feature", issue.Ref)
	require.Equal(t, []string{owner}, issue.Assignees)
	require.NotNil(t, issue.Milestone)
	require.Equal(t, milestone.ID, issue.Milestone.ID)
	issueNumber := fmt.Sprint(issue.Number)
	require.NoError(t, json.Unmarshal(execute("issue", "edit", issueNumber, "-R", repo.FullName, "--ref", repo.DefaultBranch, "--json"), &issue))
	require.Equal(t, repo.DefaultBranch, issue.Ref)
	require.NoError(t, json.Unmarshal(execute("issue", "edit", issueNumber, "-R", repo.FullName, "--ref=", "--set-assignees=", "--milestone=0", "--json"), &issue))
	require.Empty(t, issue.Ref)
	require.Empty(t, issue.Assignees)
	require.Nil(t, issue.Milestone)
	require.NoError(t, json.Unmarshal(execute("issue", "edit", issueNumber, "-R", repo.FullName, "--deadline", "2028-02-01T00:30:00+02:00", "--json"), &issue))
	assertEditedDeadline(t, issue.Deadline, "2028-02-01")
	require.NoError(t, json.Unmarshal(execute("issue", "edit", issueNumber, "-R", repo.FullName, "--deadline=", "--json"), &issue))
	require.Nil(t, issue.Deadline)
	commentsFile := filepath.Join(t.TempDir(), "comments.json")
	require.NoError(t, os.WriteFile(commentsFile, []byte(`[{"path":"change.txt","body":"Disposable inline review","new_position":1}]`), 0o600))
	var review services.Review
	require.NoError(t, json.Unmarshal(execute("pr", "review", n, "-R", repo.FullName, "--comment", "--comments-file", commentsFile, "--json"), &review))
	require.NotZero(t, review.ID)
	require.NotEmpty(t, review.Author)
	require.Equal(t, "COMMENT", review.State)
	var reviews []services.Review
	require.NoError(t, json.Unmarshal(execute("pr", "review", "list", n, "-R", repo.FullName, "--json"), &reviews))
	require.Len(t, reviews, 1)
	var comments []services.ReviewComment
	require.NoError(t, json.Unmarshal(execute("pr", "review", "comments", n, "-R", repo.FullName, "--json"), &comments))
	require.Len(t, comments, 1)
	require.Equal(t, "change.txt", comments[0].Path)
	require.EqualValues(t, 1, comments[0].Line)
	commentID := fmt.Sprint(comments[0].ID)
	require.Contains(t, string(execute("pr", "review", "reply", n, commentID, "-R", repo.FullName, "--body", "Disposable reply", "--json", "body")), "Disposable reply")
	require.NoError(t, json.Unmarshal(execute("pr", "review", "comments", n, "-R", repo.FullName, "--json"), &comments))
	require.Len(t, comments, 2)
	require.Equal(t, "Disposable reply", comments[1].Body)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "resolve", commentID, "-R", repo.FullName}))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String(), "feedback is for terminals")
	require.NoError(t, json.Unmarshal(execute("pr", "review", "comments", n, "-R", repo.FullName, "--review", fmt.Sprint(review.ID), "--json"), &comments))
	require.NotEmpty(t, comments[0].Resolver)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "unresolve", commentID, "-R", repo.FullName}))
	require.Empty(t, out.String())
	require.NoError(t, json.Unmarshal(execute("pr", "review", "comments", n, "-R", repo.FullName, "--json"), &comments))
	require.Empty(t, comments[0].Resolver)
	var pending services.Review
	require.NoError(t, json.Unmarshal(execute("pr", "review", n, "-R", repo.FullName, "--pending", "--body", "Preserved draft text", "--json"), &pending))
	require.Equal(t, "PENDING", pending.State)
	var submitted services.Review
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, pending.Body, "Edited draft text", "pr", "review", "submit", n, fmt.Sprint(pending.ID), "-R", repo.FullName, "--comment"), &submitted))
	require.Equal(t, "COMMENT", submitted.State)
	require.Equal(t, "Edited draft text", submitted.Body)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "delete", n, fmt.Sprint(submitted.ID), "-R", repo.FullName, "--yes"}))
	verifyEditorSubmission(t, deps, repo.FullName, n, true)
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String(), "feedback is for terminals")
	if reviewerTokenFile := os.Getenv("GITEA_CLI_TEST_REVIEWER_TOKEN_FILE"); reviewerTokenFile != "" {
		reviewerDeps := deps
		reviewerDeps.Getenv = gitea.env(readTokenFile(t, reviewerTokenFile))
		out.Reset()
		require.NoError(t, Execute(context.Background(), reviewerDeps, []string{"api", "user"}))
		var reviewer struct{ Login string }
		require.NoError(t, json.Unmarshal(out.Bytes(), &reviewer))
		require.NotEmpty(t, reviewer.Login)
		deps.Streams.In = strings.NewReader(`{"permission":"write"}`)
		execute("api", "repos/"+repo.FullName+"/collaborators/"+reviewer.Login, "--method", "PUT", "--input", "-")
		var requested services.Pull
		require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--add-reviewer", reviewer.Login, "--json"), &requested))
		require.Contains(t, requested.Reviewers, reviewer.Login)
		require.Empty(t, requested.ReviewerTeams)
		out.Reset()
		require.NoError(t, Execute(context.Background(), reviewerDeps, []string{"pr", "status", "-R", repo.FullName, "--json"}))
		require.NoError(t, json.Unmarshal(out.Bytes(), &pullStatus))
		require.Empty(t, pullStatus.Authored)
		require.Len(t, pullStatus.ReviewRequested, 1)
		require.Equal(t, pull.Number, pullStatus.ReviewRequested[0].Number)
		require.Equal(t, repo.FullName, pullStatus.ReviewRequested[0].Repository)
		require.NoError(t, json.Unmarshal(execute("pr", "review", "cancel-request", n, "-R", repo.FullName, "--reviewer", reviewer.Login, "--json"), &requested))
		require.NotContains(t, requested.Reviewers, reviewer.Login)
		require.NoError(t, json.Unmarshal(execute("pr", "review", "request", n, "-R", repo.FullName, "--reviewer", reviewer.Login, "--json"), &requested))
		require.Contains(t, requested.Reviewers, reviewer.Login)
		require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--remove-reviewer", reviewer.Login, "--body", "Updated alongside review request", "--json"), &requested))
		require.NotContains(t, requested.Reviewers, reviewer.Login)
		require.Equal(t, "Updated alongside review request", requested.Body)
		execute("pr", "review", "request", n, "-R", repo.FullName, "--reviewer", reviewer.Login)
		out.Reset()
		require.NoError(t, Execute(context.Background(), reviewerDeps, []string{"pr", "review", n, "-R", repo.FullName, "--approve", "--json"}))
		var approval services.Review
		require.NoError(t, json.Unmarshal(out.Bytes(), &approval))
		require.Equal(t, "APPROVED", approval.State)
		out.Reset()
		require.NoError(t, Execute(context.Background(), reviewerDeps, []string{"pr", "status", "-R", repo.FullName, "--json"}))
		require.NoError(t, json.Unmarshal(out.Bytes(), &pullStatus))
		require.Empty(t, pullStatus.ReviewRequested)
		require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, "", "Disposable dismissal reason", "pr", "review", "dismiss", n, fmt.Sprint(approval.ID), "-R", repo.FullName, "--yes"), &approval))
		require.True(t, approval.Dismissed)
		require.NoError(t, json.Unmarshal(execute("pr", "review", "undismiss", n, fmt.Sprint(approval.ID), "-R", repo.FullName, "--json"), &approval))
		require.False(t, approval.Dismissed)
		testTeamReviewRequests(t, &deps, execute, reviewer.Login)
	}
	execute("pr", "close", n, "-R", repo.FullName)
	var pulls []services.Pull
	require.NoError(t, json.Unmarshal(execute("pr", "list", "--state=closed", "-R", repo.FullName, "--json"), &pulls))
	require.Len(t, pulls, 1)
	require.Equal(t, "closed", pulls[0].State)
}

func testTeamReviewRequests(t *testing.T, deps *Dependencies, execute func(...string) []byte, reviewer string) {
	t.Helper()
	orgName := fmt.Sprintf("gt-review-org-%d", time.Now().UnixNano())
	execute("org", "create", orgName, "--visibility", "private")
	t.Cleanup(func() {
		if err := Execute(context.Background(), *deps, []string{"org", "delete", orgName, "--yes"}); err != nil {
			t.Errorf("review organization cleanup: %v", err)
		}
	})
	var team services.Team
	require.NoError(t, json.Unmarshal(execute("team", "create", orgName, "--name", "Reviewers", "--unit", "repo.code=read,repo.pulls=write", "--json"), &team))
	teamID := fmt.Sprint(team.ID)
	execute("team", "member", "add", teamID, reviewer)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", "review-target", "--org", orgName, "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), *deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("team review repository cleanup: %v", err)
		}
	})
	execute("team", "repo", "add", teamID, repo.FullName)
	execute("branch", "create", "feature", "-R", repo.FullName)
	deps.Streams.In = strings.NewReader(`{"branch":"feature","content":"dGVzdAo=","message":"Disposable team review change"}`)
	execute("api", "repos/"+repo.FullName+"/contents/change.txt", "--input", "-")
	var pull services.Pull
	require.NoError(t, json.Unmarshal(execute("pr", "create", "-R", repo.FullName, "--title", "Team review fixture", "--head", "feature", "--base", repo.DefaultBranch, "--json"), &pull))
	n := fmt.Sprint(pull.Number)
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--add-reviewer-team", team.Name, "--json"), &pull))
	require.Equal(t, []string{team.Name}, pull.ReviewerTeams)
	require.Empty(t, pull.Reviewers)
	require.NoError(t, json.Unmarshal(execute("pr", "review", "cancel-request", n, "-R", repo.FullName, "--team", team.Name, "--json"), &pull))
	require.Empty(t, pull.ReviewerTeams)
	require.NoError(t, json.Unmarshal(execute("pr", "review", "request", n, "-R", repo.FullName, "--team", team.Name, "--json"), &pull))
	require.Equal(t, []string{team.Name}, pull.ReviewerTeams)
	require.NoError(t, json.Unmarshal(execute("pr", "edit", n, "-R", repo.FullName, "--remove-reviewer-team", team.Name, "--json"), &pull))
	require.Empty(t, pull.ReviewerTeams)
}

func assertEditedDeadline(t *testing.T, value *string, day string) {
	t.Helper()
	require.NotNil(t, value)
	date, err := time.Parse(time.RFC3339, *value)
	require.NoError(t, err)
	require.Equal(t, day, date.Format("2006-01-02"))
	require.Equal(t, 59, date.Minute())
	require.Equal(t, 59, date.Second())
}
