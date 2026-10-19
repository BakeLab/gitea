// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDiffCommentsMapsAddedAndRemovedLines(t *testing.T) {
	diff := `diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -1,4 +1,5 @@
 package sample
-func old() {}
# Prefer a clearer name
+func renamed() {}
 context
# Keep context notes on the left
`
	comments, err := parseDiffComments(strings.NewReader(diff))
	require.NoError(t, err)
	require.Len(t, comments, 2)
	require.Equal(t, "sample.go", comments[0].Path)
	require.Equal(t, "Prefer a clearer name", comments[0].Body)
	require.EqualValues(t, 2, comments[0].OldLine)
	require.Zero(t, comments[0].NewLine)
	require.Equal(t, "sample.go", comments[1].Path)
	require.Equal(t, "Keep context notes on the left", comments[1].Body)
	require.EqualValues(t, 3, comments[1].OldLine)
	require.Zero(t, comments[1].NewLine)
}

func TestParseDiffCommentsOnADeletedFileNameTheFile(t *testing.T) {
	diff := `diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package gone
# Why remove this?
-func f() {}
`
	comments, err := parseDiffComments(strings.NewReader(diff))
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, "gone.go", comments[0].Path)
	require.EqualValues(t, 1, comments[0].OldLine)
}

func TestSaveDiffReviewIncludesHelpHeader(t *testing.T) {
	path, err := saveDiffReview(t.TempDir(), 7, "owner/repo", strings.NewReader("diff --git a/x b/x\n"))
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "PR #7 on owner/repo")
	require.Contains(t, string(data), "diff --git a/x b/x")
}
