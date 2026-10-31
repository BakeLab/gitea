// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaIssueStateAndAttachments(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-issue-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName, "--json")...) }

	var issue services.Issue
	require.NoError(t, json.Unmarshal(scoped("issue", "create", "--title", "State changes", "--body", "Body"), &issue))
	n := strconv.FormatInt(issue.Number, 10)
	execute("issue", "close", n, "-c", "Closing note", "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("issue", "view", n), &issue))
	require.Equal(t, "closed", issue.State)
	execute("issue", "reopen", n, "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("issue", "view", n), &issue))
	require.Equal(t, "open", issue.State)
	var events []services.TimelineEvent
	require.NoError(t, json.Unmarshal(scoped("issue", "timeline", n), &events))
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	require.Equal(t, []string{"comment", "close", "reopen"}, types, "the closing comment precedes the state change")
	require.Equal(t, "Closing note", *events[0].Body)

	file := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(file, []byte("issue attachment bytes"), 0o600))
	var attachment services.IssueAttachment
	require.NoError(t, json.Unmarshal(scoped("issue", "attachment", "upload", n, file, "--name", "upload.txt"), &attachment))
	require.Equal(t, "upload.txt", attachment.Name)
	require.EqualValues(t, len("issue attachment bytes"), attachment.Size)
	id := strconv.FormatInt(attachment.ID, 10)
	require.NoError(t, json.Unmarshal(scoped("issue", "attachment", "edit", n, id, "--name", "renamed.txt"), &attachment))
	require.Equal(t, "renamed.txt", attachment.Name)
	require.NoError(t, json.Unmarshal(scoped("issue", "attachment", "view", n, id), &attachment))
	require.Equal(t, "renamed.txt", attachment.Name)
	dir := t.TempDir()
	execute("issue", "attachment", "download", n, id, "--dir", dir, "-R", repo.FullName)
	downloaded, err := os.ReadFile(filepath.Join(dir, "renamed.txt"))
	require.NoError(t, err)
	require.Equal(t, "issue attachment bytes", string(downloaded))
	require.Equal(t, "issue attachment bytes", string(execute("issue", "attachment", "download", n, id, "--output", "-", "-R", repo.FullName)))
	execute("issue", "attachment", "delete", n, id, "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("issue", "attachment", "list", n)))
	out.Reset()
	diagnostics.Reset()
	err = Execute(context.Background(), deps, []string{"issue", "attachment", "view", n, id, "-R", repo.FullName})
	require.ErrorContains(t, err, "not found")

	var comment services.Comment
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "create", n, "--body", "With a file"), &comment))
	commentID := strconv.FormatInt(comment.ID, 10)
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "attachment", "upload", commentID, file), &attachment))
	require.Equal(t, "notes.txt", attachment.Name)
	id = strconv.FormatInt(attachment.ID, 10)
	var attachments []services.IssueAttachment
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "attachment", "list", commentID), &attachments))
	require.True(t, slices.ContainsFunc(attachments, func(a services.IssueAttachment) bool { return a.ID == attachment.ID }))
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "attachment", "edit", commentID, id, "--name", "comment.txt"), &attachment))
	require.Equal(t, "comment.txt", attachment.Name)
	require.NoError(t, json.Unmarshal(scoped("issue", "comment", "attachment", "view", commentID, id), &attachment))
	require.Equal(t, "comment.txt", attachment.Name)
	require.Equal(t, "issue attachment bytes", string(execute("issue", "comment", "attachment", "download", commentID, id, "--output", "-", "-R", repo.FullName)))
	execute("issue", "comment", "attachment", "delete", commentID, id, "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("issue", "comment", "attachment", "list", commentID)))
}
