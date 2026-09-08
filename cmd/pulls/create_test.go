// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"bytes"
	"encoding/json"
	"testing"

	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCreatedPullAsJSON(t *testing.T) {
	pr := &gitea.PullRequest{
		Index:   33,
		Title:   "test title",
		HTMLURL: "https://gitea.example.com/owner/repo/pulls/33",
		State:   gitea.StateOpen,
		Base:    &gitea.PRBranchInfo{Ref: "main"},
		Head:    &gitea.PRBranchInfo{Ref: "feature"},
	}

	var buf bytes.Buffer
	require.NoError(t, writeCreatedPullAsJSON(&buf, pr))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	assert.Equal(t, float64(33), got["index"])
	assert.Equal(t, "test title", got["title"])
	assert.Equal(t, "https://gitea.example.com/owner/repo/pulls/33", got["url"])
	assert.Equal(t, "open", got["state"])
	assert.Equal(t, "main", got["base"])
	assert.Equal(t, "feature", got["head"])

	// exactly the lean field set, nothing extra
	assert.Len(t, got, 6)

	// machine-readable output must not contain terminal escape sequences
	assert.NotContains(t, buf.String(), "\x1b")
}
