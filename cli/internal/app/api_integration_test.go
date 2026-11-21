// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

func TestGiteaRawAPIFieldsAndPagination(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", fmt.Sprintf("gt-api-%d", time.Now().UnixNano()), "--private", "--json"}))
	var repo services.Repository
	require.NoError(t, json.Unmarshal(out.Bytes(), &repo))
	t.Cleanup(func() {
		require.NoError(t, Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}))
	})
	for index := 1; index <= 2; index++ {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/{owner}/{repo}/issues", "-R", repo.FullName, "-f", fmt.Sprintf("title=Issue %d", index), "-F", "closed=false"}))
		var issue services.Issue
		require.NoError(t, json.Unmarshal(out.Bytes(), &issue))
		require.Equal(t, int64(index), issue.Number)
	}
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/{owner}/{repo}/issues", "-R", repo.FullName, "-X", "GET", "-F", "limit=1", "-f", "state=all", "--paginate", "--include"}))
	decoder := json.NewDecoder(strings.NewReader(out.String()))
	pages, total := 0, 0
	for {
		var issues []services.Issue
		err := decoder.Decode(&issues)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		pages++
		total += len(issues)
	}
	require.Equal(t, 2, total)
	require.Equal(t, 2, pages)
	require.Contains(t, diagnostics.String(), "Link:")
	require.Contains(t, diagnostics.String(), "200 OK")
	for _, tc := range []struct {
		until string
		count int
	}{{"2099-01-01", 2}, {"2020-01-01", 0}} {
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", repo.FullName, "--author", strings.SplitN(repo.FullName, "/", 2)[0], "--until", tc.until, "--json"}))
		var issues []services.Issue
		require.NoError(t, json.Unmarshal(out.Bytes(), &issues))
		require.Len(t, issues, tc.count)
		for _, issue := range issues {
			require.Equal(t, strings.SplitN(repo.FullName, "/", 2)[0], issue.Author)
		}
		require.Empty(t, diagnostics.String())
	}
}
