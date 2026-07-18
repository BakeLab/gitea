// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func TestResolveLabelNames_ReturnsRepoAndOrgLabels(t *testing.T) {
	// This test verifies that ResolveLabelNames correctly returns both repository and organization labels.
	// It sets up a test repository and organization with known labels, then calls ResolveLabelNames and checks the results.
	login := createIntegrationLogin(t)
	client := login.Client()
	orgName := fmt.Sprintf("labels-org-%d", time.Now().UnixNano()%1_000_000)
	orgRepoName := fmt.Sprintf("labels-repo-%d", time.Now().UnixNano()%1_000_000)
	ctx := context.Background()

	// Clean up any existing test data that might interfere with the test.
	_, _ = client.Repositories.DeleteRepo(ctx, orgName, orgRepoName)
	_, _ = client.Organizations.DeleteOrg(ctx, orgName)

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

	orgLabelName := fmt.Sprintf("org-label-%d", time.Now().UnixNano()%1_000_000)
	repoLabelName := fmt.Sprintf("repo-label-%d", time.Now().UnixNano()%1_000_000)

	orgLabel, _, err := client.Organizations.CreateOrgLabel(ctx, orgName, gitea.CreateOrgLabelOption{Name: orgLabelName, Color: "ff0000"})
	require.NoError(t, err)
	repoLabel, _, err := client.Repositories.CreateLabel(ctx, orgName, orgRepoName, gitea.CreateLabelOption{Name: repoLabelName, Color: "00ff00"})
	require.NoError(t, err)

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

	runGit("checkout", "-b", "branch-with-labels")
	runGit("commit", "--allow-empty", "-m", "Initial commit")
	runGit("push", "-u", "origin", "HEAD:branch-with-labels")

	waitForBranches(t, orgRepo.FullName, "branch-with-labels")
	_ = runTeaCommand(
		t, "pr", "create", "--repo", orgRepo.FullName,
		"--login", login.Name, "--base", "main", "--head", "branch-with-labels",
		"--labels", orgLabelName+","+repoLabelName,
	)

	pr, _, err := client.PullRequests.GetPullRequest(ctx, orgName, orgRepoName, 1)
	require.NoError(t, err)
	labels := pr.Labels
	require.Len(t, labels, 2)
	require.ElementsMatch(t, labels, []*gitea.Label{orgLabel, repoLabel})
}

func waitForBranches(t *testing.T, repoFullName string, branchName string) {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/repos/%s/branches", os.Getenv("GITEA_TEA_TEST_URL"), repoFullName)

	// retry for ~6 seconds
	for range 30 {
		resp, err := http.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var branches []struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(body, &branches) == nil {
				have := map[string]bool{}
				for _, b := range branches {
					have[b.Name] = true
				}
				if have["main"] && have[branchName] {
					return
				}
			}

		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("waitForBranches: branches never appeared in API: %s", url)
}
