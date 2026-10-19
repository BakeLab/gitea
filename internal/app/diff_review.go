// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"fmt"
	"io"
	"strings"

	unidiff "gitea.com/noerw/unidiff-comments"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
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
#
# Lines starting with '###' are ignored: below the lines they are on, they show the review
# comments the pull request already has.

`

// saveDiffReview writes a temporary annotated-diff file for interactive review, with the existing
// inline comments below the lines they are on.
func saveDiffReview(dir string, n int64, repo string, diff io.Reader, existing []services.ReviewComment) (string, error) {
	data, err := io.ReadAll(diff)
	if err != nil {
		return "", err
	}
	return terminal.SaveDraft(dir, fmt.Sprintf("pull-%d-review-*.diff", n), fmt.Sprintf(diffReviewHelp, n, repo)+annotateDiff(string(data), existing))
}

// diffLine places a review comment: a file's path, the side of the diff and the line on it.
type diffLine struct {
	path string
	old  bool
	line int64
}

// annotateDiff adds each comment as ### lines below the diff line it is on: the new line of an
// added or unchanged line, or the old line of a removed one.
func annotateDiff(diff string, comments []services.ReviewComment) string {
	if len(comments) == 0 {
		return diff
	}
	placed := map[diffLine][]services.ReviewComment{}
	for _, comment := range comments {
		at := diffLine{comment.Path, comment.Line == 0, cmp.Or(comment.Line, comment.OldLine)}
		placed[at] = append(placed[at], comment)
	}
	var b strings.Builder
	var path string
	var oldLine, newLine int64
	// File headers come before the first hunk; inside hunks, "--- x" is a removed "-- x".
	header := true
	for line := range strings.Lines(diff) {
		b.WriteString(line)
		var at diffLine
		switch {
		case strings.HasPrefix(line, "diff "):
			header = true
			continue
		case header && strings.HasPrefix(line, "+++ "):
			path = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")), "b/")
			continue
		case header && strings.HasPrefix(line, "--- "):
			if source := strings.TrimSpace(strings.TrimPrefix(line, "--- ")); source != "/dev/null" {
				path = strings.TrimPrefix(source, "a/")
			}
			continue
		case strings.HasPrefix(line, "@@ "):
			// @@ -old[,count] +new[,count] @@
			_, _ = fmt.Sscanf(line, "@@ -%d", &oldLine)
			if _, after, ok := strings.Cut(line, " +"); ok {
				_, _ = fmt.Sscanf(after, "%d", &newLine)
			}
			oldLine, newLine, header = oldLine-1, newLine-1, false
			continue
		case strings.HasPrefix(line, "+"):
			newLine++
			at = diffLine{path, false, newLine}
		case strings.HasPrefix(line, "-"):
			oldLine++
			at = diffLine{path, true, oldLine}
		case strings.HasPrefix(line, " "):
			oldLine, newLine = oldLine+1, newLine+1
			at = diffLine{path, false, newLine}
		default:
			continue
		}
		if !strings.HasSuffix(line, "\n") {
			b.WriteString("\n")
		}
		for _, comment := range placed[at] {
			for i, text := range strings.Split(strings.TrimSpace(comment.Body), "\n") {
				if i == 0 {
					text = comment.Author + ": " + text
				}
				b.WriteString("### " + strings.TrimRight(text, "\r") + "\n")
			}
		}
	}
	return b.String()
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
			// The destination of a deleted file is /dev/null; its comments belong to the source.
			path := strings.TrimPrefix(c.Anchor.Path, "b/")
			if c.Anchor.Path == "/dev/null" {
				path = strings.TrimPrefix(c.Anchor.SrcPath, "a/")
			}
			comment := terminal.ReviewCommentDraft{Body: c.Text, Path: path}
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
