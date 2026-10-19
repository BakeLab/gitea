// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestGiteaMetadataLifecycle(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)

	adminUsername := fmt.Sprintf("gt-it-admin-%d", time.Now().UnixNano())
	var managedUser services.AdminUser
	adminData := execute("admin", "user", "create", adminUsername, "--email", adminUsername+"@example.test", "--password", "local-admin-test-password", "--admin", "--restricted", "--prohibit-login", "--visibility", "limited", "--json")
	require.NotContains(t, string(adminData), "local-admin-test-password")
	require.NoError(t, json.Unmarshal(adminData, &managedUser))
	require.True(t, managedUser.Admin)
	require.True(t, managedUser.Restricted)
	require.True(t, managedUser.ProhibitLogin)
	require.Equal(t, "limited", managedUser.Visibility)
	userDeleted := false
	t.Cleanup(func() {
		if !userDeleted {
			if err := Execute(context.Background(), deps, []string{"admin", "user", "delete", adminUsername, "--yes"}); err != nil {
				t.Errorf("admin account cleanup: %v", err)
			}
		}
	})
	passwordPath := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(passwordPath, []byte("updated-local-password\n"), 0o600))
	require.NoError(t, json.Unmarshal(execute("admin", "user", "edit", adminUsername, "--admin=false", "--restricted=false", "--prohibit-login=false", "--active=false", "--full-name", "Managed Account", "--description", "Disposable", "--location", "Test server", "--website", "https://example.test", "--visibility", "private", "--max-repo-creation", "0", "--allow-git-hook=false", "--allow-import-local=false", "--allow-create-organization=false", "--password-file", passwordPath, "--must-change-password=false", "--json"), &managedUser))
	require.False(t, managedUser.Admin)
	require.False(t, managedUser.Restricted)
	require.False(t, managedUser.ProhibitLogin)
	require.False(t, managedUser.Active)
	require.Equal(t, "Managed Account", managedUser.FullName)
	require.Equal(t, "private", managedUser.Visibility)
	require.NoError(t, json.Unmarshal(execute("admin", "user", "view", adminUsername, "--json"), &managedUser))
	require.Equal(t, "Disposable", managedUser.Description)
	var managedUsers []services.AdminUser
	require.NoError(t, json.Unmarshal(execute("admin", "user", "list", "--json"), &managedUsers))
	require.Contains(t, managedUsers, managedUser)
	execute("admin", "user", "delete", adminUsername, "--yes")
	userDeleted = true
	name := fmt.Sprintf("gt-it-%d", time.Now().UnixNano())
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", name, "--auto-init", "--private", "--json"), &repo))
	require.Equal(t, name, repo.Name)
	t.Cleanup(func() {
		out.Reset()
		err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"})
		if err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	require.NoError(t, json.Unmarshal(execute("repo", "edit", "-R", repo.FullName, "--description", "edited", "--website", "https://example.test", "--default-branch", repo.DefaultBranch, "--json"), &repo))
	require.Equal(t, "edited", repo.Description)
	require.Equal(t, "https://example.test", repo.Website)
	require.NoError(t, json.Unmarshal(execute("repo", "edit", "-R", repo.FullName, "--website=", "--json"), &repo))
	require.Empty(t, repo.Website)
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName, "--json")...) }
	templateName := fmt.Sprintf("gt-template-%d", time.Now().UnixNano())
	var template, generated services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", templateName, "--as-template", "--auto-init", "--default-branch", "trunk", "--private", "--json"), &template))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", template.FullName, "--yes"}); err != nil {
			t.Errorf("template cleanup: %v", err)
		}
	})
	require.True(t, template.Template)
	generatedName := fmt.Sprintf("gt-generated-%d", time.Now().UnixNano())
	require.NoError(t, json.Unmarshal(execute("repo", "create", generatedName, "--template", template.URL, "--private", "--copy-git-hooks=false", "--json"), &generated))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", generated.FullName, "--yes"}); err != nil {
			t.Errorf("generated repository cleanup: %v", err)
		}
	})
	require.Equal(t, "trunk", generated.DefaultBranch)
	require.False(t, generated.Template)
	var foundRepos []services.Repository
	require.NoError(t, json.Unmarshal(execute("search", "repo", generatedName, "--owner", "integration", "--private", "--archived=false", "--type", "source", "--json"), &foundRepos))
	require.Contains(t, foundRepos, generated)
	execute("api", "user/starred/"+generated.FullName, "--method", "PUT")
	require.NoError(t, json.Unmarshal(execute("repo", "list", "--starred", "--json"), &foundRepos))
	require.True(t, slices.ContainsFunc(foundRepos, func(r services.Repository) bool { return r.ID == generated.ID }), "starred list misses the repository")
	execute("api", "user/starred/"+generated.FullName, "--method", "DELETE")
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "list", "--watched", "--limit", "1", "--page", "2", "--json"}))
	require.NoError(t, json.Unmarshal(out.Bytes(), &foundRepos))
	require.Len(t, foundRepos, 1)
	require.Contains(t, diagnostics.String(), "more results available; use --page 3", "a page in the middle says that more follow")
	if serverRoot := os.Getenv("GITEA_CLI_TEST_REPOSITORY_ROOT"); serverRoot != "" {
		migrationName := fmt.Sprintf("gt-migrated-%d", time.Now().UnixNano())
		var migrated services.Repository
		require.NoError(t, json.Unmarshal(execute("repo", "migrate", "--server-path", filepath.ToSlash(filepath.Join(serverRoot, repo.FullName+".git")), "--name", migrationName, "--private", "--json"), &migrated))
		t.Cleanup(func() {
			if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", migrated.FullName, "--yes"}); err != nil {
				t.Errorf("migrated repository cleanup: %v", err)
			}
		})
		require.Equal(t, repo.DefaultBranch, migrated.DefaultBranch)
		require.True(t, migrated.Private)
	}
	var branch services.Branch
	require.NoError(t, json.Unmarshal(scoped("branch", "create", "feature/initial"), &branch))
	require.Equal(t, "feature/initial", branch.Name)
	execute("branch", "rename", "feature/initial", "feature/renamed", "-R", repo.FullName)
	execute("branch", "protect", "feature/renamed", "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("branch", "view", "feature/renamed"), &branch))
	require.True(t, branch.Protected)
	require.EqualValues(t, 1, branch.RequiredApprovals)
	execute("branch", "unprotect", "feature/renamed", "-R", repo.FullName, "--yes")
	require.NoError(t, json.Unmarshal(scoped("branch", "view", "feature/renamed"), &branch))
	require.False(t, branch.Protected)
	execute("branch", "delete", "feature/renamed", "-R", repo.FullName, "--yes")

	var hook services.Webhook
	require.NoError(t, json.Unmarshal(scoped("webhook", "create", "http://127.0.0.1:9/hook?key=private-value", "--secret", "local-signing-secret", "--authorization-header", "Bearer local-header", "--active=false", "--events", "issues,release", "--branch-filter", "feature/*"), &hook))
	require.False(t, hook.Active)
	hookID := strconv.FormatInt(hook.ID, 10)
	hookData := scoped("webhook", "edit", hookID, "--active=false", "--clear-authorization")
	require.NotContains(t, string(hookData), "local-signing-secret")
	require.NotContains(t, string(hookData), "local-header")
	require.NotContains(t, string(hookData), "private-value")
	require.NoError(t, json.Unmarshal(hookData, &hook))
	require.Equal(t, "feature/*", hook.BranchFilter)
	require.Contains(t, hook.Events, "release")
	require.Contains(t, hook.Events, "issues")
	require.NoError(t, json.Unmarshal(scoped("webhook", "edit", hookID, "--events", "push", "--branch-filter=", "--clear-authorization"), &hook))
	require.Empty(t, hook.BranchFilter)
	require.Equal(t, []string{"push"}, hook.Events)
	execute("webhook", "delete", hookID, "-R", repo.FullName, "--yes")
	var hooks []services.Webhook
	require.NoError(t, json.Unmarshal(scoped("webhook", "list"), &hooks))
	require.Empty(t, hooks)
	for _, scope := range []string{"--account", "--global"} {
		require.NoError(t, json.Unmarshal(execute("webhook", "create", "http://127.0.0.1:9/hook", scope, "--active=false", "--json"), &hook))
		hookID := strconv.FormatInt(hook.ID, 10)
		require.NoError(t, json.Unmarshal(execute("webhook", "list", scope, "--json"), &hooks))
		require.Contains(t, hooks, hook)
		execute("webhook", "delete", hookID, scope, "--yes")
	}
	var label services.Label
	require.NoError(t, json.Unmarshal(scoped("label", "create", "bug", "--color", "ff0000"), &label))
	require.Positive(t, label.ID)
	id := strconv.FormatInt(label.ID, 10)
	require.NoError(t, json.Unmarshal(scoped("label", "edit", id, "--description", "updated"), &label))
	require.Equal(t, "updated", label.Description)
	var labels []services.Label
	require.NoError(t, json.Unmarshal(scoped("label", "list"), &labels))
	require.Len(t, labels, 1)
	var labeledIssue services.Issue
	require.NoError(t, json.Unmarshal(scoped("issue", "create", "--title", "Labeled issue", "--label", "repo:bug"), &labeledIssue))
	require.Positive(t, labeledIssue.Number)
	require.Equal(t, []services.Label{label}, labeledIssue.Labels)
	require.NoError(t, json.Unmarshal(scoped("issue", "edit", strconv.FormatInt(labeledIssue.Number, 10), "--set-labels=", "--title", "Labels cleared"), &labeledIssue))
	require.Empty(t, labeledIssue.Labels)
	require.NotNil(t, labeledIssue.Labels)
	execute("label", "delete", id, "-R", repo.FullName, "--yes")
	var milestone services.Milestone
	require.NoError(t, json.Unmarshal(scoped("milestone", "create", "--title", "v1", "--deadline", "2026-12-01"), &milestone))
	require.Positive(t, milestone.ID)
	ms := strconv.FormatInt(milestone.ID, 10)
	require.NoError(t, json.Unmarshal(scoped("milestone", "close", ms), &milestone))
	require.Equal(t, "closed", milestone.State)
	require.NoError(t, json.Unmarshal(scoped("milestone", "reopen", ms), &milestone))
	require.Equal(t, "open", milestone.State)
	execute("milestone", "delete", ms, "-R", repo.FullName, "--yes")
	var issue services.Issue
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, "", "Body", "issue", "create", "--repo", repo.FullName, "--title", "real API issue"), &issue))
	require.Positive(t, issue.Number)
	require.Equal(t, "real API issue", issue.Title)
	issueNumber := strconv.FormatInt(issue.Number, 10)
	require.Equal(t, "[]\n", string(scoped("issue", "attachment", "list", issueNumber)))
	verifyResourceBrowser(t, deps, repo.URL, "repo", "view", "--repo", repo.FullName)
	verifyResourceBrowser(t, deps, issue.URL, "issue", "view", issueNumber, "--repo", repo.FullName)
	verifyBodyEditorIntegration(t, deps, "issue", repo.FullName, issueNumber, issue.Body)
	verifyIssueAttachmentLifecycle(t, deps, "issue", repo.FullName, issueNumber)
	var searchItems []services.SearchItem
	for range 100 {
		require.NoError(t, json.Unmarshal(scoped("search", "issue", "real API issue", "--authored"), &searchItems))
		if len(searchItems) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Len(t, searchItems, 1)
	require.Equal(t, repo.FullName, searchItems[0].Repository)
	require.Equal(t, "issue", searchItems[0].Type)
	require.NoError(t, json.Unmarshal(execute("search", "issue", "real API issue", "--owner", "integration", "--authored", "--json"), &searchItems))
	foundIssue := false
	for _, item := range searchItems {
		if item.ID == issue.ID {
			foundIssue = true
		}
	}
	require.True(t, foundIssue)
	require.Equal(t, "[]\n", string(execute("search", "pr", name, "--json")))
	require.NoError(t, json.Unmarshal(scoped("issue", "edit", issueNumber, "--title", "edited", "--body=", "--set-assignees", strings.Split(repo.FullName, "/")[0]), &issue))
	require.Equal(t, "edited", issue.Title)
	require.Empty(t, issue.Body)
	var bulkEdited []services.Issue
	require.NoError(t, json.Unmarshal(scoped("issue", "edit", issueNumber, strconv.FormatInt(labeledIssue.Number, 10), "--body", "Bulk edited body"), &bulkEdited))
	require.Len(t, bulkEdited, 2)
	for _, edited := range bulkEdited {
		require.Equal(t, "Bulk edited body", edited.Body)
		var saved services.Issue
		require.NoError(t, json.Unmarshal(scoped("issue", "view", strconv.FormatInt(edited.Number, 10)), &saved))
		require.Equal(t, edited.Body, saved.Body)
	}
	require.NoError(t, json.Unmarshal(scoped("repo", "edit", "--actions=false"), &repo))
	require.False(t, repo.ActionsEnabled)
	var accountStatus services.AccountStatus
	require.NoError(t, json.Unmarshal(scoped("status"), &accountStatus))
	require.Equal(t, "integration", accountStatus.User)
	require.Equal(t, repo.FullName, accountStatus.Repository)
	require.Len(t, accountStatus.AssignedIssues, 1)
	require.Equal(t, issue.ID, accountStatus.AssignedIssues[0].ID)
	require.False(t, accountStatus.RunsIncluded)
	require.Empty(t, accountStatus.FailingRuns)
	scoped("issue", "edit", issueNumber, "--set-assignees=")
	var comment services.Comment
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "create", issueNumber, "--body", "First comment"), &comment))
	require.Positive(t, comment.ID)
	commentID := strconv.FormatInt(comment.ID, 10)
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "edit", commentID, "--body", "Edited comment"), &comment))
	require.Equal(t, "Edited comment", comment.Body)
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "view", commentID), &comment))
	var comments []services.Comment
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "list", issueNumber), &comments))
	require.Len(t, comments, 1)
	execute("issue", "comment", "delete", commentID, "-R", repo.FullName, "--yes")
	verifyEditorSubmission(t, deps, repo.FullName, issueNumber, false)
	require.Equal(t, "[]\n", string(scoped("issue", "comment", "list", issueNumber)))
	require.NoError(t, json.Unmarshal(scoped("milestone", "create", "--title", "membership"), &milestone))
	ms = strconv.FormatInt(milestone.ID, 10)
	scoped("milestone", "issue", "add", ms, issueNumber)
	var members []services.Issue
	require.NoError(t, json.Unmarshal(scoped("milestone", "issue", "list", ms), &members))
	require.Len(t, members, 1)
	require.Equal(t, issue.Number, members[0].Number)
	scoped("milestone", "issue", "remove", ms, issueNumber)
	require.Equal(t, "[]\n", string(scoped("milestone", "issue", "list", ms)))
	execute("milestone", "delete", ms, "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("wiki", "list")))
	notifier := fmt.Sprintf("gt-notifier-%d", time.Now().UnixNano())
	deps.Streams.In = bytes.NewBufferString(fmt.Sprintf(`{"username":%q,"password":"local-test-password!123","email":%q,"must_change_password":false}`, notifier, notifier+"@example.test"))
	execute("api", "admin/users", "--input", "-")
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"api", "admin/users/" + notifier, "--method", "DELETE"}); err != nil {
			t.Errorf("notifier cleanup: %v", err)
		}
	})
	deps.Streams.In = bytes.NewBufferString(`{"permission":"write"}`)
	execute("api", "repos/"+repo.FullName+"/collaborators/"+notifier, "--method", "PUT", "--input", "-")
	deps.Streams.In = bytes.NewBufferString(`{"body":"Notification event"}`)
	execute("api", "repos/"+repo.FullName+"/issues/"+issueNumber+"/comments?sudo="+notifier, "--input", "-")
	var notifications []services.Notification
	for range 100 {
		require.NoError(t, json.Unmarshal(scoped("notification", "list", "--type", "issue"), &notifications))
		if len(notifications) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotEmpty(t, notifications)
	var boundedNotifications []services.Notification
	require.NoError(t, json.Unmarshal(scoped("notification", "list", "--type", "issue", "--since=24h", "--before=0s"), &boundedNotifications))
	require.NotEmpty(t, boundedNotifications)
	notificationID := strconv.FormatInt(notifications[0].ID, 10)
	var notification services.Notification
	require.NoError(t, json.Unmarshal(scoped("notification", "view", notificationID), &notification))
	require.Equal(t, repo.FullName, notification.Repository)
	require.NoError(t, json.Unmarshal(scoped("notification", "pin", notificationID), &notification))
	require.True(t, notification.Pinned)
	require.NoError(t, json.Unmarshal(scoped("notification", "unpin", notificationID), &notification))
	require.False(t, notification.Pinned)
	require.NoError(t, json.Unmarshal(scoped("notification", "mark-unread", notificationID), &notification))
	require.True(t, notification.Unread)
	watched := json.NewDecoder(bytes.NewReader(scoped("notification", "watch", "--timeout", "500ms", "--interval", "100ms")))
	require.NoError(t, watched.Decode(&notification), "watch prints one object per line")
	require.Equal(t, repo.FullName, notification.Repository)
	scoped("notification", "mark-read", "--all")
	require.Equal(t, "[]\n", string(scoped("notification", "list")))
	var release services.Release
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, "", "Release notes", "release", "create", "v1", "--repo", repo.FullName, "--draft"), &release))
	require.True(t, release.Draft)
	require.Equal(t, "Release notes", release.Body)
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, release.Body, "Edited release notes", "release", "edit", "v1", "--repo", repo.FullName), &release))
	require.Equal(t, "Edited release notes", release.Body)
	require.NoError(t, json.Unmarshal(scoped("release", "view", "v1"), &release))
	require.Equal(t, "Edited release notes", release.Body)
	require.NoError(t, json.Unmarshal(scoped("release", "edit", "v1", "--title", "First release", "--prerelease"), &release))
	require.Equal(t, "First release", release.Title)
	require.True(t, release.Prerelease)
	require.NoError(t, json.Unmarshal(scoped("release", "publish", "v1"), &release))
	require.False(t, release.Draft)
	assetFile := filepath.Join(t.TempDir(), "artifact.txt")
	require.NoError(t, os.WriteFile(assetFile, []byte("release asset bytes"), 0o600))
	var asset services.ReleaseAsset
	require.NoError(t, json.Unmarshal(scoped("release", "asset", "upload", "v1", assetFile), &asset))
	require.Equal(t, int64(19), asset.Size)
	assetID := strconv.FormatInt(asset.ID, 10)
	downloaded := execute("release", "asset", "download", "v1", assetID, "-R", repo.FullName, "--output", "-")
	require.Equal(t, "release asset bytes", string(downloaded))
	require.NoError(t, json.Unmarshal(scoped("release", "asset", "edit", "v1", assetID, "--name", "renamed.txt"), &asset))
	require.Equal(t, "renamed.txt", asset.Name)
	var assets []services.ReleaseAsset
	require.NoError(t, json.Unmarshal(scoped("release", "asset", "list", "v1"), &assets))
	require.Len(t, assets, 1)
	execute("release", "asset", "delete", "v1", assetID, "-R", repo.FullName, "--yes")
	secondAssetFile := filepath.Join(t.TempDir(), "second.txt")
	require.NoError(t, os.WriteFile(secondAssetFile, []byte("second attachment"), 0o600))
	var bulkAssets []services.ReleaseAsset
	require.NoError(t, json.Unmarshal(scoped("release", "asset", "upload", "v1", assetFile, secondAssetFile), &bulkAssets))
	require.Len(t, bulkAssets, 2)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", bulkAssets[0].Name, bulkAssets[1].Name, "--by-name", "-R", repo.FullName, "--yes"}))
	require.Empty(t, diagnostics.String(), "confirmation lines are only written on a terminal")
	require.NoError(t, json.Unmarshal(scoped("release", "asset", "list", "v1"), &assets))
	require.Empty(t, assets)
	scoped("release", "create", "v2", "--asset", assetFile)
	require.NoError(t, json.Unmarshal(scoped("release", "asset", "list", "v2"), &assets))
	require.Len(t, assets, 1)
	var releases []services.Release
	require.NoError(t, json.Unmarshal(scoped("release", "edit", "v1", "v2", "--title", "Updated releases"), &releases))
	require.Len(t, releases, 2)
	execute("release", "delete", "v1", "v2", "-R", repo.FullName, "--delete-tag", "--yes")
	var tags []json.RawMessage
	require.NoError(t, json.Unmarshal(execute("api", "repos/"+repo.FullName+"/tags"), &tags))
	require.Empty(t, tags)
	require.Equal(t, "[]\n", string(scoped("release", "list")))
	var variable services.ActionVariable
	require.NoError(t, json.Unmarshal(scoped("variable", "set", "BUILD_MODE", "--body", "first"), &variable))
	require.Equal(t, "first", variable.Value)
	scoped("variable", "set", "BUILD_MODE", "--body", "second")
	require.NoError(t, json.Unmarshal(scoped("variable", "view", "BUILD_MODE"), &variable))
	require.Equal(t, "second", variable.Value)
	var variables []services.ActionVariable
	require.NoError(t, json.Unmarshal(scoped("variable", "list"), &variables))
	require.Len(t, variables, 1)
	execute("variable", "delete", "BUILD_MODE", "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("variable", "list")))
	secretOutput := scoped("secret", "set", "BUILD_TOKEN", "--body", "sensitive-local-test-value")
	require.NotContains(t, string(secretOutput), "sensitive-local-test-value")
	var secrets []services.ActionSecret
	require.NoError(t, json.Unmarshal(scoped("secret", "list"), &secrets))
	require.Len(t, secrets, 1)
	require.Equal(t, "BUILD_TOKEN", secrets[0].Name)
	execute("secret", "delete", "BUILD_TOKEN", "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("secret", "list")))
	accountName := fmt.Sprintf("CLI_IT_%d", time.Now().UnixNano())
	execute("variable", "set", accountName, "--account", "--body", "account value")
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"variable", "delete", accountName, "--account", "--yes"}); err != nil {
			t.Errorf("account variable cleanup: %v", err)
		}
	})
	require.NoError(t, json.Unmarshal(execute("variable", "view", accountName, "--account", "--json"), &variable))
	require.Equal(t, "account value", variable.Value)
	orgName := fmt.Sprintf("gt-it-org-%d", time.Now().UnixNano())
	var organization services.Organization
	require.NoError(t, json.Unmarshal(execute("org", "create", orgName, "--description", "Original", "--visibility", "private", "--json"), &organization))
	require.Equal(t, "private", organization.Visibility)
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"org", "delete", orgName, "--yes"}); err != nil {
			t.Errorf("organization cleanup: %v", err)
		}
	})
	require.NoError(t, json.Unmarshal(execute("webhook", "create", "http://127.0.0.1:9/hook", "--org", orgName, "--active=false", "--json"), &hook))
	execute("webhook", "delete", strconv.FormatInt(hook.ID, 10), "--org", orgName, "--yes")
	require.NoError(t, json.Unmarshal(execute("org", "edit", orgName, "--description=", "--visibility", "limited", "--repo-admins-can-change-team-access", "--json"), &organization))
	require.Empty(t, organization.Description)
	require.True(t, organization.RepoAdminChangeTeamAccess)
	require.NoError(t, json.Unmarshal(execute("org", "view", orgName, "--json"), &organization))
	require.Equal(t, "limited", organization.Visibility)
	var organizations []services.Organization
	require.NoError(t, json.Unmarshal(execute("org", "list", "--json"), &organizations))
	require.Contains(t, organizations, organization)
	username, _, _ := strings.Cut(repo.FullName, "/")
	execute("org", "member", "public", orgName, username)
	var membersList []services.OrganizationMember
	require.NoError(t, json.Unmarshal(execute("org", "member", "list", orgName, "--public", "--json"), &membersList))
	require.Len(t, membersList, 1)
	execute("org", "member", "private", orgName, username)
	out.Reset()
	membershipErr := Execute(context.Background(), deps, []string{"api", "orgs/" + orgName + "/public_members/" + username})
	var missingMembership *transport.APIError
	require.ErrorAs(t, membershipErr, &missingMembership)
	require.Equal(t, 404, missingMembership.Status)
	var team services.Team
	require.NoError(t, json.Unmarshal(execute("team", "create", orgName, "--name", "Developers", "--unit", "repo.code=write,repo.issues=read", "--json"), &team))
	teamID := strconv.FormatInt(team.ID, 10)
	require.Equal(t, "write", team.Units["repo.code"])
	require.NoError(t, json.Unmarshal(execute("team", "edit", teamID, "--description", "Edited team", "--all-repositories", "--json"), &team))
	require.Equal(t, "write", team.Units["repo.code"])
	require.True(t, team.AllRepositories)
	require.NoError(t, json.Unmarshal(execute("team", "edit", teamID, "--all-repositories=false", "--json"), &team))
	require.False(t, team.AllRepositories)
	execute("team", "member", "add", teamID, username)
	var teamMembers []services.OrganizationMember
	require.NoError(t, json.Unmarshal(execute("team", "member", "list", teamID, "--json"), &teamMembers))
	require.Len(t, teamMembers, 1)
	execute("team", "member", "remove", teamID, username, "--yes")
	require.Equal(t, "[]\n", string(execute("team", "member", "list", teamID, "--json")))
	var orgRepo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", "team-repo", "--org", orgName, "--json"), &orgRepo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", orgRepo.FullName, "--yes"}); err != nil {
			t.Errorf("organization repository cleanup: %v", err)
		}
	})
	execute("team", "repo", "add", teamID, orgRepo.FullName)
	var teamRepos []services.Repository
	require.NoError(t, json.Unmarshal(execute("team", "repo", "list", teamID, "--json"), &teamRepos))
	require.Len(t, teamRepos, 1)
	execute("team", "repo", "remove", teamID, orgRepo.FullName, "--yes")
	require.Equal(t, "[]\n", string(execute("team", "repo", "list", teamID, "--json")))
	execute("team", "delete", teamID, "--yes")
	execute("variable", "set", "ORG_MODE", "--org", orgName, "--body", "org value")
	require.NoError(t, json.Unmarshal(execute("variable", "view", "ORG_MODE", "--org", orgName, "--json"), &variable))
	require.Equal(t, "org value", variable.Value)
	execute("variable", "delete", "ORG_MODE", "--org", orgName, "--yes")
	execute("secret", "set", "ORG_TOKEN", "--org", orgName, "--body", "org-local-secret")
	require.NoError(t, json.Unmarshal(execute("secret", "list", "--org", orgName, "--json"), &secrets))
	require.Len(t, secrets, 1)
	execute("secret", "delete", "ORG_TOKEN", "--org", orgName, "--yes")
	execute("secret", "set", accountName, "--account", "--body", "account-local-secret")
	execute("secret", "delete", accountName, "--account", "--yes")
	var wiki services.WikiPage
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, "", "# Unicode ü content", "wiki", "create", "--repo", repo.FullName, "--title", "Home", "--message", "Create page"), &wiki))
	require.Equal(t, "# Unicode ü content", wiki.Body)
	require.NoError(t, json.Unmarshal(scoped("wiki", "view", "Home"), &wiki))
	require.Equal(t, "Home", wiki.Title)
	require.NoError(t, json.Unmarshal(executeWithEditorIntegration(t, deps, wiki.Body, "Updated content", "wiki", "edit", "Home", "--repo", repo.FullName, "--message", "Edit page"), &wiki))
	require.Equal(t, "Updated content", wiki.Body)
	var pages []services.WikiPage
	require.NoError(t, json.Unmarshal(scoped("wiki", "list"), &pages))
	require.Len(t, pages, 1)
	var revisions []services.WikiRevision
	require.NoError(t, json.Unmarshal(scoped("wiki", "revisions", "Home"), &revisions))
	require.Len(t, revisions, 2)
	execute("wiki", "delete", "Home", "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("wiki", "list")))
	var record services.TrackedTime
	require.NoError(t, json.Unmarshal(scoped("time", "create", issueNumber, "1h25m"), &record))
	require.Equal(t, int64(5100), record.Seconds)
	timeID := strconv.FormatInt(record.ID, 10)
	var records []services.TrackedTime
	require.NoError(t, json.Unmarshal(scoped("time", "list", "--issue", issueNumber), &records))
	require.Len(t, records, 1)
	execute("time", "delete", issueNumber, timeID, "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("time", "list", "--issue", issueNumber)))
	scoped("time", "create", issueNumber, "30s")
	execute("time", "reset", issueNumber, "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("time", "list", "--issue", issueNumber)))
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "integration.pub")
	// The title defaults to the key comment.
	require.NoError(t, os.WriteFile(keyFile, append(bytes.TrimSpace(ssh.MarshalAuthorizedKey(publicKey)), " integration\n"...), 0o600))
	var key services.SSHKey
	require.NoError(t, json.Unmarshal(execute("ssh-key", "create", keyFile, "--json"), &key))
	require.Positive(t, key.ID)
	keyID := strconv.FormatInt(key.ID, 10)
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"ssh-key", "delete", keyID, "--yes"}); err != nil {
			t.Errorf("SSH key cleanup: %v", err)
		}
	})
	require.NoError(t, json.Unmarshal(execute("ssh-key", "view", keyID, "--json"), &key))
	require.Equal(t, "integration", key.Title)
	var keys []services.SSHKey
	require.NoError(t, json.Unmarshal(execute("ssh-key", "list", "--json"), &keys))
	require.Contains(t, keys, key)
}
