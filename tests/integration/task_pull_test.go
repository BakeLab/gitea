// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/cmd"
	"github.com/stretchr/testify/require"
)

func TestEditPull_ModifiesAssignees(t *testing.T) {
	// This test verifies that EditPull correctly modifies assignees of an pull request via set, add, and remove.
	// It sets up a test repository and organization with known users, then calls EditPull and checks the results.
	login := createIntegrationLogin(t)
	client := login.Client()
	orgName := fmt.Sprintf("pull-org-%d", time.Now().UnixNano()%1_000_000)
	orgRepoName := fmt.Sprintf("pull-repo-%d", time.Now().UnixNano()%1_000_000)
	ctx := context.Background()

	// Clean up any existing test data that might interfere with the test.
	_, _ = client.Repositories.DeleteRepo(ctx, orgName, orgRepoName)
	_, _ = client.Organizations.DeleteOrg(ctx, orgName)
	_, _ = client.Admin.DeleteUser(ctx, "user1")
	_, _ = client.Admin.DeleteUser(ctx, "user2")

	_, _, err := client.Admin.CreateOrg(ctx, integrationUsername, gitea.CreateOrgOption{Name: orgName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Organizations.DeleteOrg(ctx, orgName); delErr != nil {
			t.Logf("failed to delete integration test org %q: %v", orgName, delErr)
		}
	})

	orgRepo, _, err := client.Repositories.CreateOrgRepo(ctx, orgName, gitea.CreateRepoOption{Name: orgRepoName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Repositories.DeleteRepo(ctx, orgName, orgRepoName); delErr != nil {
			t.Logf("failed to delete integration test repo %q: %v", orgRepoName, delErr)
		}
	})

	user1, _, err := client.Admin.CreateUser(ctx, gitea.CreateUserOption{Username: "user1", Password: "user1!1234", Email: "user1@test.com"})
	require.NoError(t, err)
	user2, _, err := client.Admin.CreateUser(ctx, gitea.CreateUserOption{Username: "user2", Password: "user2!1234", Email: "user2@test.com"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = client.Admin.DeleteUser(ctx, "user1")
		_, _ = client.Admin.DeleteUser(ctx, "user2")
	})

	permission := gitea.AccessModeOwner
	team, _, err := client.Organizations.CreateTeam(ctx, orgName, gitea.CreateTeamOption{Name: "writers", Permission: permission})
	require.NoError(t, err)
	_, err = client.Organizations.AddTeamMember(ctx, team.ID, "user1")
	require.NoError(t, err)
	_, err = client.Organizations.AddTeamMember(ctx, team.ID, "user2")
	require.NoError(t, err)
	_, err = client.Organizations.AddTeamRepository(ctx, team.ID, orgName, orgRepoName)
	require.NoError(t, err)
	assigneeValid, _, err := client.Repositories.CheckRepoIssueAssignee(ctx, orgName, orgRepoName, user1.UserName)
	require.NoError(t, err)
	require.True(t, assigneeValid)
	assigneeValid, _, err = client.Repositories.CheckRepoIssueAssignee(ctx, orgName, orgRepoName, user2.UserName)
	require.NoError(t, err)
	require.True(t, assigneeValid)

	tmpDir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		require.NoError(t, cmd.Run())
	}

	runGit("init")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "user.name", "test")
	httpsURL := fmt.Sprintf("%s/%s.git", login.URL, orgRepo.FullName)
	httpsURL = strings.Replace(httpsURL, "://", fmt.Sprintf("://%s:%s@", login.Name, login.Token), 1)

	runGit("remote", "add", "origin", httpsURL)

	runGit("checkout", "-b", "main")
	runGit("commit", "--allow-empty", "-m", "Initial commit")
	runGit("push", "-u", "origin", "HEAD:main")

	runGit("checkout", "-b", "branch-with-assignees")
	runGit("commit", "--allow-empty", "-m", "Initial commit")
	runGit("push", "-u", "origin", "HEAD:branch-with-assignees")

	waitForBranches(t, orgRepo.FullName, "branch-with-assignees")
	getNames := func(pr *gitea.PullRequest) []string {
		names := make([]string, len(pr.Assignees))
		for i, u := range pr.Assignees {
			names[i] = u.UserName
		}
		return names
	}
	checkAssignees := func(expected []string) {
		updatedPr, _, err := client.PullRequests.GetPullRequest(ctx, orgName, orgRepoName, 1)
		require.NoError(t, err)
		require.ElementsMatch(t, getNames(updatedPr), expected)
	}

	app := cmd.App()
	err = app.Run(ctx, []string{"tea", "pr", "create", "--repo", orgRepo.FullName, "--base", "main", "--head", "branch-with-assignees", "--a", integrationUsername})
	require.NoError(t, err)
	checkAssignees([]string{integrationUsername})
	curUser, _, err := client.Users.GetMyUserInfo(ctx)
	require.NoError(t, err)

	// test set overwrites add and remove
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--set-assignees", "user1", "--add-assignees", "user2", "--remove-assignees", integrationUsername, "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{user1.UserName})
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--set-assignees", "user2," + integrationUsername, "--add-assignees", "user1", "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{user2.UserName, curUser.UserName})
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--set-assignees", "user1", "--remove-assignees", integrationUsername, "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{user1.UserName})

	// test remove one assignee
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--remove-assignees", "user1", "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{})

	// test add multiple assignees and overwrites remove
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--add-assignees", "user1," + integrationUsername, "--remove-assignees", integrationUsername, "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{curUser.UserName, user1.UserName})

	// test add one assignee
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--add-assignees", "user2", "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{curUser.UserName, user1.UserName, user2.UserName})

	// test remove multiple assignees
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--remove-assignees", "user1,user2," + integrationUsername, "--repo", orgRepo.FullName})
	require.NoError(t, err)
	checkAssignees([]string{})

	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--set-assignees", "user_not_exists"})
	require.Error(t, err)
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--add-assignees", "user_not_exists"})
	require.Error(t, err)
	err = app.Run(ctx, []string{"tea", "pr", "edit", "1", "--remove-assignees", "user_not_exists"})
	require.Error(t, err)
}
