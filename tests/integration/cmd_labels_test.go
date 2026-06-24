// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func setupOrgRepoWithLabels(t *testing.T) (org *gitea.Organization, orgRepo *gitea.Repository, orgLabel, repoLabel *gitea.Label) {
	t.Helper()

	login := createIntegrationLogin(t)
	client := login.Client()
	orgName := fmt.Sprintf("labels-org-%d", time.Now().UnixNano()%1_000_000)
	orgRepoName := fmt.Sprintf("labels-repo-%d", time.Now().UnixNano()%1_000_000)
	ctx := context.Background()

	// Clean up any existing test data that might interfere with the test.
	_, _ = client.Repositories.DeleteRepo(ctx, orgName, orgRepoName)
	_, _ = client.Organizations.DeleteOrg(ctx, orgName)

	org, _, err := client.Organizations.CreateOrg(ctx, gitea.CreateOrgOption{Name: orgName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Organizations.DeleteOrg(ctx, orgName); delErr != nil {
			t.Logf("failed to delete integration test org %q: %v", orgName, delErr)
		}
	})

	orgRepo, _, err = client.Repositories.CreateOrgRepo(ctx, orgName, gitea.CreateRepoOption{Name: orgRepoName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Repositories.DeleteRepo(ctx, orgName, orgRepoName); delErr != nil {
			t.Logf("failed to delete integration test repo %q: %v", orgRepoName, delErr)
		}
	})

	orgLabelName := fmt.Sprintf("org-label-%d", time.Now().UnixNano()%1_000_000)
	repoLabelName := fmt.Sprintf("repo-label-%d", time.Now().UnixNano()%1_000_000)

	orgLabel, _, err = client.Organizations.CreateOrgLabel(ctx, orgName, gitea.CreateOrgLabelOption{Name: orgLabelName, Color: "ff0000"})
	require.NoError(t, err)
	repoLabel, _, err = client.Repositories.CreateLabel(ctx, orgName, orgRepoName, gitea.CreateLabelOption{Name: repoLabelName, Color: "00ff00"})
	require.NoError(t, err)
	return org, orgRepo, orgLabel, repoLabel
}

func setupRepoWithLabels(t *testing.T) (repo *gitea.Repository, repoLabel *gitea.Label) {
	t.Helper()

	login := createIntegrationLogin(t)
	client := login.Client()
	repoName := fmt.Sprintf("labels-repo-%d", time.Now().UnixNano()%1_000_000)
	ctx := context.Background()

	// Clean up any existing test data that might interfere with the test.
	_, _ = client.Repositories.DeleteRepo(ctx, login.User, repoName)

	repo, _, err := client.Repositories.CreateRepo(ctx, gitea.CreateRepoOption{Name: repoName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Repositories.DeleteRepo(ctx, login.User, repoName); delErr != nil {
			t.Logf("failed to delete integration test repo %q: %v", repoName, delErr)
		}
	})

	repoLabelName := fmt.Sprintf("repo-label-%d", time.Now().UnixNano()%1_000_000)
	repoLabel, _, err = client.Repositories.CreateLabel(ctx, login.User, repoName, gitea.CreateLabelOption{Name: repoLabelName, Color: "00ff00"})
	require.NoError(t, err)
	return repo, repoLabel
}

func TestRunLabelsList_OrgFlagReturnsOnlyOrgLabels(t *testing.T) {
	// This test verifies that when listing labels with only the --org flag set,
	// only organization labels are included in the results, and repository labels are excluded.
	_, orgRepo, orgLabel, _ := setupOrgRepoWithLabels(t)

	labelIDs := runTeaCommand(t, "label", "ls", "--org", orgRepo.Owner.UserName, "--output", "json")
	var rows []map[string]string
	require.NoError(t, json.Unmarshal([]byte(labelIDs), &rows))

	require.Len(t, rows, 1)
	indexInt, err := strconv.ParseInt(rows[0]["index"], 10, 64)
	require.NoError(t, err)
	require.Equal(t, orgLabel.ID, indexInt)
	require.Equal(t, orgLabel.Name, rows[0]["name"])
}

func TestRunLabelsList_RepoReturnsNoOrgLabels(t *testing.T) {
	// This test verifies that when listing labels for a repository with no organization,
	// organization labels are not included in the results.
	repo, repoLabel := setupRepoWithLabels(t)

	labelIDs := runTeaCommand(t, "label", "ls", "--repo", repo.FullName, "--output", "json")
	var rows []map[string]string
	require.NoError(t, json.Unmarshal([]byte(labelIDs), &rows))

	require.Len(t, rows, 1)
	indexInt, err := strconv.ParseInt(rows[0]["index"], 10, 64)
	require.NoError(t, err)
	require.Equal(t, repoLabel.ID, indexInt)
	require.Equal(t, repoLabel.Name, rows[0]["name"])
}

func TestRunLabelsList_UseRepoFlagOverOrgFlag(t *testing.T) {
	// This test verifies that when both --org and --repo flags are set,
	// the command prioritizes the --repo flag and lists labels for the repository, ignoring the --org flag.
	repo, repoLabel := setupRepoWithLabels(t)

	labelIDs := runTeaCommand(t, "label", "ls", "--repo", repo.FullName, "--org", "some-other-org", "--output", "json")
	var rows []map[string]string
	require.NoError(t, json.Unmarshal([]byte(labelIDs), &rows))

	require.Len(t, rows, 1)
	indexInt, err := strconv.ParseInt(rows[0]["index"], 10, 64)
	require.NoError(t, err)
	require.Equal(t, repoLabel.ID, indexInt)
	require.Equal(t, repoLabel.Name, rows[0]["name"])
}

func TestRunLabelsList_OrgRepoReturnsRepoAndOrgLabels(t *testing.T) {
	// This test verifies that when listing labels for a repository that belongs to an organization,
	// both repository and organization labels are included in the results.
	_, orgRepo, orgLabel, repoLabel := setupOrgRepoWithLabels(t)

	labelIDs := runTeaCommand(t, "label", "ls", "--repo", orgRepo.FullName, "--output", "json")
	var rows []map[string]string
	require.NoError(t, json.Unmarshal([]byte(labelIDs), &rows))

	require.Len(t, rows, 2)
	require.ElementsMatch(t, []string{rows[0]["name"], rows[1]["name"]}, []string{orgLabel.Name, repoLabel.Name})
	require.ElementsMatch(
		t,
		[]string{rows[0]["index"], rows[1]["index"]},
		[]string{strconv.FormatInt(orgLabel.ID, 10), strconv.FormatInt(repoLabel.ID, 10)},
	)
}

func TestRunLabelsList_OrgRepoWithExcludeReturnsOnlyRepoLabels(t *testing.T) {
	// This test verifies that when listing labels for a repository that belongs to an organization with the --exclude flag,
	// only repository labels are included in the results, and organization labels are excluded.
	_, orgRepo, _, repoLabel := setupOrgRepoWithLabels(t)

	labelIDs := runTeaCommand(t, "label", "ls", "--repo", orgRepo.FullName, "--exclude-org", "--output", "json")
	var rows []map[string]string
	require.NoError(t, json.Unmarshal([]byte(labelIDs), &rows))

	require.Len(t, rows, 1)
	indexInt, err := strconv.ParseInt(rows[0]["index"], 10, 64)
	require.NoError(t, err)
	require.Equal(t, repoLabel.ID, indexInt)
	require.Equal(t, repoLabel.Name, rows[0]["name"])
}
