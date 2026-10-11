// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	unidiff "gitea.com/noerw/unidiff-comments"
	"gitea.dev/tea/internal/terminal"
)

const diffReviewHelp = `# This is the current diff of PR #%d on %s.
# To add code comments, just insert a line inside the diff with your comment,
# prefixed with '# '. For example:
#
# - foo: string,
# - bar: string,
# + foo: int,
# # This is a code comment
# + bar: int,

`

// saveDiffReview writes a temporary annotated-diff file for interactive review.
func saveDiffReview(dir string, n int64, repo string, diff io.Reader) (string, error) {
	file, err := os.CreateTemp(dir, fmt.Sprintf("pull-%d-review-*.diff", n))
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := fmt.Fprintf(file, diffReviewHelp, n, repo); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if _, err := io.Copy(file, diff); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// parseDiffComments extracts inline review comments from an edited unified diff.
func parseDiffComments(r io.Reader) ([]terminal.ReviewCommentDraft, error) {
	changeset, err := unidiff.ReadChangeset(r)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse patch: %w", err)
	}
	comments := make([]terminal.ReviewCommentDraft, 0)
	for _, file := range changeset.Diffs {
		for _, c := range file.LineComments {
			comment := terminal.ReviewCommentDraft{Body: c.Text, Path: c.Anchor.Path}
			comment.Path = strings.TrimPrefix(comment.Path, "a/")
			comment.Path = strings.TrimPrefix(comment.Path, "b/")
			switch c.Anchor.LineType {
			case "ADDED":
				comment.NewLine = c.Anchor.Line
			case "REMOVED", "CONTEXT":
				comment.OldLine = c.Anchor.Line
			}
			comments = append(comments, comment)
		}
	}
	return comments, nil
}
