// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	teacmd "gitea.dev/tea/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiCommandLifecycle(t *testing.T) {
	login := createIntegrationLogin(t)
	client := login.Client()
	repoName := fmt.Sprintf("tea-wiki-integration-%d", time.Now().UnixNano())

	_, _, err := client.Repositories.CreateRepo(context.Background(), gitea.CreateRepoOption{Name: repoName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Repositories.DeleteRepo(context.Background(), login.User, repoName); delErr != nil {
			t.Logf("failed to delete integration test repo %q: %v", repoName, delErr)
		}
	})

	repoSlug := fmt.Sprintf("%s/%s", login.User, repoName)

	createOutput := runTeaCommand(t,
		"wiki", "create",
		"--login", login.Name,
		"--repo", repoSlug,
		"--title", "Home",
		"--content", "# Home",
		"--message", "create home",
		"--output", "json",
	)
	assert.Contains(t, createOutput, "\"title\": \"Home\"")

	page, _, err := client.Wiki.GetPage(context.Background(), login.User, repoName, "Home")
	require.NoError(t, err)
	assert.Equal(t, "Home", page.Title)
	assert.Contains(t, decodeBase64String(t, page.ContentBase64), "# Home")

	listOutput := runTeaCommand(t,
		"wiki", "list",
		"--login", login.Name,
		"--repo", repoSlug,
		"--output", "json",
	)
	assert.Contains(t, listOutput, "\"title\": \"Home\"")

	emptyRepoName := fmt.Sprintf("tea-wiki-empty-%d", time.Now().UnixNano())
	_, _, err = client.Repositories.CreateRepo(context.Background(), gitea.CreateRepoOption{Name: emptyRepoName})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Repositories.DeleteRepo(context.Background(), login.User, emptyRepoName); delErr != nil {
			t.Logf("failed to delete empty wiki repo %q: %v", emptyRepoName, delErr)
		}
	})
	emptyRepoSlug := fmt.Sprintf("%s/%s", login.User, emptyRepoName)
	emptyListOutput := runTeaCommand(t,
		"wiki", "list",
		"--login", login.Name,
		"--repo", emptyRepoSlug,
	)
	assert.Contains(t, emptyListOutput, "No wiki pages found")

	viewOutput := runTeaCommand(t,
		"wiki", "view", "Home",
		"--login", login.Name,
		"--repo", repoSlug,
		"--output", "json",
	)
	assert.Contains(t, viewOutput, "\"title\": \"Home\"")
	assert.Contains(t, viewOutput, "\"content\": \"# Home\"")

	runTeaCommand(t,
		"wiki", "edit", "Home",
		"--login", login.Name,
		"--repo", repoSlug,
		"--content", "# Updated",
		"--message", "edit home",
		"--output", "json",
	)

	page, _, err = client.Wiki.GetPage(context.Background(), login.User, repoName, "Home")
	require.NoError(t, err)
	assert.Equal(t, "Home", page.Title)
	assert.Contains(t, decodeBase64String(t, page.ContentBase64), "# Updated")

	viewRenderedOutput := runTeaCommand(t,
		"wiki", "view", "Home",
		"--login", login.Name,
		"--repo", repoSlug,
	)
	assert.Contains(t, viewRenderedOutput, "# Updated")
	assert.NotContains(t, viewRenderedOutput, "Last updated")

	revisionsOutput := runTeaCommand(t,
		"wiki", "revisions", "Home",
		"--login", login.Name,
		"--repo", repoSlug,
		"--output", "json",
	)
	assert.Contains(t, revisionsOutput, "create home")
	assert.Contains(t, revisionsOutput, "edit home")

	runTeaCommand(t,
		"wiki", "delete", "Home",
		"--login", login.Name,
		"--repo", repoSlug,
		"--confirm",
	)

	_, _, err = client.Wiki.GetPage(context.Background(), login.User, repoName, "Home")
	require.Error(t, err)
	assert.True(t, strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "404"))
}

func runTeaCommand(t *testing.T, args ...string) string {
	t.Helper()

	app := teacmd.App()
	errBuf := &bytes.Buffer{}
	app.Writer = io.Discard
	app.ErrWriter = errBuf

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan string, 1)
	go func() {
		var outBuf bytes.Buffer
		_, _ = io.Copy(&outBuf, reader)
		done <- outBuf.String()
	}()

	runErr := app.Run(context.Background(), append([]string{"tea"}, args...))
	require.NoError(t, writer.Close())
	output := <-done
	require.NoError(t, reader.Close())

	require.NoError(t, runErr, "tea %s failed with stdout: %s\nstderr: %s", strings.Join(args, " "), output, errBuf.String())

	return output
}

func decodeBase64String(t *testing.T, encoded string) string {
	t.Helper()

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)

	return string(decoded)
}
