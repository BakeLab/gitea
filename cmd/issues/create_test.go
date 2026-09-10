// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"bytes"
	"encoding/json"
	"testing"

	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCreatedIssueAsJSON(t *testing.T) {
	issue := &gitea.Issue{
		Index:   42,
		Title:   "test title",
		HTMLURL: "https://gitea.example.com/owner/repo/issues/42",
		State:   gitea.StateOpen,
	}

	var buf bytes.Buffer
	require.NoError(t, writeCreatedIssueAsJSON(&buf, issue))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	assert.Equal(t, float64(42), got["index"])
	assert.Equal(t, "test title", got["title"])
	assert.Equal(t, "https://gitea.example.com/owner/repo/issues/42", got["url"])
	assert.Equal(t, "open", got["state"])

	// exactly the lean field set, nothing extra
	assert.Len(t, got, 4)

	// machine-readable output must not contain terminal escape sequences
	assert.NotContains(t, buf.String(), "\x1b")
}
