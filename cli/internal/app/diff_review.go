// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

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
#
# Keep the lines of the diff as they are: the review is refused if one is changed or removed.
# A blank line you add is left out, or kept inside the comment it is in; use '#' alone for an
# empty comment line.

`

// saveDiffReview writes a temporary annotated-diff file for interactive review, with the existing
// inline comments below the lines they are on, and returns its path and the text it wrote.
func saveDiffReview(dir string, n int64, repo string, diff io.Reader, existing []services.ReviewComment) (path, annotated string, err error) {
	data, err := io.ReadAll(diff)
	if err != nil {
		return "", "", err
	}
	annotated = fmt.Sprintf(diffReviewHelp, n, repo) + annotateDiff(string(data), existing)
	path, err = terminal.SaveDraft(dir, fmt.Sprintf("pull-%d-review-*.diff", n), annotated)
	return path, annotated, err
}

// diffLine places a review comment: a file's path, the side of the diff and the line on it.
type diffLine struct {
	path string
	old  bool
	line int64
}

// annotateDiff adds each comment as ### lines below the diff line it is on: the new line of an
// added line, the old line of a removed one, or either line of an unchanged one, since Gitea
// anchors a comment on an unchanged line on the old side when it was written there.
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
	walkDiff(diff, func(line string, at []diffLine) {
		b.WriteString(line)
		if len(at) == 0 {
			return
		}
		if !strings.HasSuffix(line, "\n") {
			b.WriteString("\n")
		}
		for _, position := range at {
			for _, comment := range placed[position] {
				for i, text := range strings.Split(strings.TrimSpace(comment.Body), "\n") {
					if i == 0 {
						text = comment.Author + ": " + text
					}
					b.WriteString("### " + strings.TrimRight(text, "\r") + "\n")
				}
			}
		}
	})
	return b.String()
}

// diffLines returns the lines of a diff that a review comment can be on.
func diffLines(diff string) map[diffLine]bool {
	lines := map[diffLine]bool{}
	walkDiff(diff, func(_ string, at []diffLine) {
		for _, position := range at {
			lines[position] = true
		}
	})
	return lines
}

// walkDiff calls visit with each line of a unified diff and the places a review comment on it
// would have: the new line of an added line, the old line of a removed one, and both lines of an
// unchanged one. Lines outside hunks have none. An empty line inside a hunk is an unchanged one,
// as git apply reads it.
func walkDiff(diff string, visit func(line string, at []diffLine)) {
	var path string
	var oldLine, newLine, oldLeft, newLeft int64
	// File headers come before the first hunk; inside hunks, "--- x" is a removed "-- x".
	header := true
	for line := range strings.Lines(diff) {
		var at []diffLine
		kind := ""
		switch {
		case (oldLeft > 0 || newLeft > 0) && strings.TrimRight(line, "\r\n") == "":
			kind = " "
		case !header:
			kind = line[:1]
		}
		switch {
		case strings.HasPrefix(line, "diff "):
			header, oldLeft, newLeft = true, 0, 0
		case header && strings.HasPrefix(line, "+++ "):
			// A deleted file's comments are on its source, like parseDiffComments places them.
			if target := diffHeaderPath(line, "b/"); target != "/dev/null" {
				path = target
			}
		case header && strings.HasPrefix(line, "--- "):
			if source := diffHeaderPath(line, "a/"); source != "/dev/null" {
				path = source
			}
		case strings.HasPrefix(line, "@@ "):
			// @@ -old[,count] +new[,count] @@
			var oldRange, newRange string
			_, _ = fmt.Sscanf(line, "@@ -%s +%s", &oldRange, &newRange)
			oldLine, oldLeft = hunkRange(oldRange)
			newLine, newLeft = hunkRange(newRange)
			oldLine, newLine, header = oldLine-1, newLine-1, false
		case kind == "+":
			newLine, newLeft = newLine+1, newLeft-1
			at = []diffLine{{path, false, newLine}}
		case kind == "-":
			oldLine, oldLeft = oldLine+1, oldLeft-1
			at = []diffLine{{path, true, oldLine}}
		case kind == " ":
			oldLine, newLine, oldLeft, newLeft = oldLine+1, newLine+1, oldLeft-1, newLeft-1
			at = []diffLine{{path, false, newLine}, {path, true, oldLine}}
		}
		oldLeft, newLeft = max(oldLeft, 0), max(newLeft, 0)
		visit(line, at)
	}
}

// hunkRange reads one side of a hunk header, such as "12,3", or "12" for a single line, as its
// first line and line count.
func hunkRange(text string) (start, count int64) {
	first, rest, ok := strings.Cut(text, ",")
	start, _ = strconv.ParseInt(first, 10, 64)
	count = 1
	if ok {
		count, _ = strconv.ParseInt(rest, 10, 64)
	}
	return start, count
}

// diffHeaderPath returns the path of a "--- " or "+++ " file header without its a/ or b/ prefix.
// Git quotes a path with unusual characters like a Go string and ends the header of a path with a
// space in a tab; any other leading or trailing space belongs to the path.
func diffHeaderPath(line, prefix string) string {
	name := strings.TrimSuffix(strings.TrimRight(line[len("--- "):], "\r\n"), "\t")
	return strings.TrimPrefix(unquoteDiffPath(name), prefix)
}

// keepDiffLines ends the errors about an annotated diff whose lines were changed.
const keepDiffLines = "keep the lines of the diff as they are and start every comment line with #"

// trimLine returns line without its trailing space and line ending, which editors strip or change.
func trimLine(line string) string {
	return strings.TrimRightFunc(line, unicode.IsSpace)
}

// parseDiffComments extracts inline review comments from edited, the annotated diff original that
// gt wrote once the user has edited it. The lines starting with "#" below a line of a hunk, up to
// the next line of the diff, are a comment on that line, on its old side when the line is
// unchanged. Lines starting with "###" are ignored, and so are the comments above the diff, like
// its help.
//
// Every other line must be the next line of original, ignoring trailing space, which editors strip.
// A blank line that is not one of the diff is one the user added: it belongs to the comment it is
// in, or is left out. Only the empty unchanged lines at the end of original may be missing, which
// editors that strip trailing empty lines remove. Any other change is refused rather than risk
// putting a comment on another line than the one it was written below.
func parseDiffComments(original string, edited io.Reader) ([]terminal.ReviewCommentDraft, error) {
	data, err := io.ReadAll(edited)
	if err != nil {
		return nil, fmt.Errorf("couldn't load diff: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		// An emptied file holds no comment.
		return nil, nil
	}
	// The lines of the diff, which the edited text must keep in order; the help above the diff and
	// the existing comments in it may go.
	type diffText struct {
		text string
		at   []diffLine
	}
	var want []diffText
	walkDiff(original, func(line string, at []diffLine) {
		text := trimLine(line)
		if strings.HasPrefix(text, "#") || (len(want) == 0 && text == "") {
			return
		}
		want = append(want, diffText{text, at})
	})
	type editedLine struct {
		number int
		text   string
	}
	var lines []editedLine
	for i, line := range slices.Collect(strings.Lines(string(data))) {
		if !strings.HasPrefix(line, "###") {
			lines = append(lines, editedLine{i + 1, line})
		}
	}
	isComment := func(line editedLine) bool { return strings.HasPrefix(line.text, "#") }
	isBlank := func(line editedLine) bool { return !isComment(line) && trimLine(line.text) == "" }

	var comments []terminal.ReviewCommentDraft
	var anchor *diffLine
	var body []string
	indent, started := 0, false
	finish := func() {
		if text := strings.TrimSpace(strings.Join(body, "\n")); text != "" && anchor != nil {
			comment := terminal.ReviewCommentDraft{Path: anchor.path, Body: text, NewLine: anchor.line}
			if anchor.old {
				comment.OldLine, comment.NewLine = anchor.line, 0
			}
			comments = append(comments, comment)
		}
		body = nil
	}
	addComment := func(line editedLine) error {
		text := strings.TrimRight(line.text[1:], "\r\n")
		margin := len(text) - len(strings.TrimLeft(text, " \t"))
		if body == nil {
			indent = margin
		}
		if started && anchor == nil && strings.TrimSpace(text) != "" {
			return fmt.Errorf("the comment on line %d of the annotated diff is not below a line of a hunk", line.number)
		}
		body = append(body, text[min(margin, indent):])
		return nil
	}
	match := func(line diffText) {
		// "\ No newline at end of file" leaves a comment below it on the line above it.
		if strings.HasPrefix(line.text, `\`) {
			return
		}
		finish()
		anchor = nil
		if len(line.at) > 0 {
			anchor = &line.at[len(line.at)-1]
		}
	}
	next := 0
	for i := 0; ; {
		// The comments and blank lines up to the next line of the diff, some of which may be the
		// empty unchanged lines of the diff there.
		end, blanks := i, 0
		for ; end < len(lines) && (isComment(lines[end]) || isBlank(lines[end])); end++ {
			if isBlank(lines[end]) {
				blanks++
			}
		}
		empty := 0
		for next+empty < len(want) && want[next+empty].text == "" {
			empty++
		}
		// Editors that strip trailing empty lines may remove those at the end of the diff.
		last := end == len(lines) && next+empty == len(want)
		if blanks < empty && !last {
			if end == len(lines) {
				return nil, errors.New("the annotated diff lacks an empty unchanged line of the diff at its end; " + keepDiffLines)
			}
			return nil, fmt.Errorf("the annotated diff lacks an empty unchanged line of the diff above its line %d; %s", lines[end].number, keepDiffLines)
		}
		// With more blank lines than the diff has there, which ones the user added is unknown:
		// refuse a comment whose line depends on it.
		seen := 0
		for _, line := range lines[i:end] {
			if isBlank(line) {
				seen++
				continue
			}
			low, high := max(0, empty-(blanks-seen)), min(empty, seen)
			if last {
				low = 0
			}
			if low != high && strings.TrimSpace(line.text[1:]) != "" {
				return nil, fmt.Errorf("the comment on line %d of the annotated diff may be below an empty line of the diff or below a blank line you added; remove the blank lines around it", line.number)
			}
		}
		// Every choice places the comments alike, so take the first blank lines for those of the diff.
		seen = 0
		for _, line := range lines[i:end] {
			switch {
			case !isBlank(line):
				if err := addComment(line); err != nil {
					return nil, err
				}
			case seen < empty:
				match(want[next+seen])
				seen++
			case body != nil:
				body = append(body, "")
			}
		}
		next += empty
		if end == len(lines) {
			if next < len(want) {
				return nil, fmt.Errorf("the annotated diff lacks the line %q of the diff at its end; %s", want[next].text, keepDiffLines)
			}
			break
		}
		line := lines[end]
		switch {
		case next == len(want):
			return nil, fmt.Errorf("line %d of the annotated diff, %q, is not a line of the diff; %s", line.number, trimLine(line.text), keepDiffLines)
		case trimLine(line.text) != want[next].text:
			return nil, fmt.Errorf("line %d of the annotated diff, %q, is not the next line of the diff, %q; %s", line.number, trimLine(line.text), want[next].text, keepDiffLines)
		}
		match(want[next])
		started = true
		next, i = next+1, end+1
	}
	finish()
	return comments, nil
}
