// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

// oddPathsDiff changes files whose names Git quotes, or whose headers it ends with a tab because
// they hold a space.
const oddPathsDiff = "diff --git a/dir a/b.txt b/dir a/b.txt\n" +
	"new file mode 100644\n--- /dev/null\n+++ b/dir a/b.txt\t\n@@ -0,0 +1 @@\n+x\n" +
	"diff --git a/ lead.txt b/ lead.txt\n" +
	"new file mode 100644\n--- /dev/null\n+++ b/ lead.txt\t\n@@ -0,0 +1 @@\n+x\n" +
	"diff --git a/trail.txt  b/trail.txt \n" +
	"new file mode 100644\n--- /dev/null\n+++ b/trail.txt \t\n@@ -0,0 +1 @@\n+x\n" +
	`diff --git "a/q\"uote.txt" "b/q\"uote.txt"` + "\n" +
	"new file mode 100644\n--- /dev/null\n" + `+++ "b/q\"uote.txt"` + "\n@@ -0,0 +1 @@\n+x\n" +
	`diff --git "a/back\\slash.txt" "b/back\\slash.txt"` + "\n" +
	"deleted file mode 100644\n" + `--- "a/back\\slash.txt"` + "\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n" +
	`diff --git "a/tab\there.txt" "b/tab\there.txt"` + "\n" +
	`--- "a/tab\there.txt"` + "\n" + `+++ "b/tab\there.txt"` + "\n@@ -1,2 +1,2 @@\n x\n-y\n+z\n" +
	`diff --git "a/new\nline.txt" "b/new\nline.txt"` + "\n" +
	"new file mode 100644\n--- /dev/null\n" + `+++ "b/new\nline.txt"` + "\n@@ -0,0 +1 @@\n+x\n\\ No newline at end of file\n"

// parseEdited reads the comments of edited, the annotated diff original once edited.
func parseEdited(original, edited string) ([]terminal.ReviewCommentDraft, error) {
	return parseDiffComments(original, strings.NewReader(edited))
}

// withoutComments returns annotated without the comments a review added to it: the text gt wrote.
func withoutComments(annotated string) string {
	var b strings.Builder
	for line := range strings.Lines(annotated) {
		if !strings.HasPrefix(line, "#") || strings.HasPrefix(line, "###") {
			b.WriteString(line)
		}
	}
	return b.String()
}

func TestParseDiffCommentsKeepsExactPaths(t *testing.T) {
	annotated := strings.NewReplacer(
		"+++ b/dir a/b.txt\t\n@@ -0,0 +1 @@\n+x\n", "+++ b/dir a/b.txt\t\n@@ -0,0 +1 @@\n+x\n# dir\n",
		"+++ b/ lead.txt\t\n@@ -0,0 +1 @@\n+x\n", "+++ b/ lead.txt\t\n@@ -0,0 +1 @@\n+x\n# lead\n",
		"+++ b/trail.txt \t\n@@ -0,0 +1 @@\n+x\n", "+++ b/trail.txt \t\n@@ -0,0 +1 @@\n+x\n# trail\n",
		`uote.txt"`+"\n@@ -0,0 +1 @@\n+x\n", `uote.txt"`+"\n@@ -0,0 +1 @@\n+x\n# quote\n",
		"@@ -1 +0,0 @@\n-x\n", "@@ -1 +0,0 @@\n-x\n# back\n",
		" x\n-y\n+z\n", " x\n# tab\n-y\n+z\n",
		"\\ No newline at end of file\n", "\\ No newline at end of file\n# line\n",
	).Replace(oddPathsDiff)
	help := fmt.Sprintf(diffReviewHelp, 1, "o/r")
	comments, err := parseEdited(help+oddPathsDiff, help+annotated)
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{
		{Path: "dir a/b.txt", Body: "dir", NewLine: 1},
		{Path: " lead.txt", Body: "lead", NewLine: 1},
		{Path: "trail.txt ", Body: "trail", NewLine: 1},
		{Path: `q"uote.txt`, Body: "quote", NewLine: 1},
		{Path: `back\slash.txt`, Body: "back", OldLine: 1},
		{Path: "tab\there.txt", Body: "tab", OldLine: 1},
		{Path: "new\nline.txt", Body: "line", NewLine: 1},
	}, comments)
	inline := make([]services.InlineComment, 0, len(comments))
	for _, comment := range comments {
		inline = append(inline, services.InlineComment(comment))
	}
	require.NoError(t, checkDiffComments(oddPathsDiff, 1, inline))
	// The help may go.
	comments, err = parseEdited(help+oddPathsDiff, annotated)
	require.NoError(t, err)
	require.Len(t, comments, 7)
}

func TestParseDiffCommentsKeepsMultilineIndentation(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n x\n+y\n#  First\n### alice: earlier\n#    indented\n#\n# last\n"
	comments, err := parseEdited(withoutComments(diff), diff)
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "x", Body: "First\n  indented\n\nlast", NewLine: 2}}, comments)
}

func TestParseDiffCommentsRefusesACommentOutsideAHunk(t *testing.T) {
	diff := "diff --git a/x b/x\n--- a/x\n+++ b/x\n# lost\n@@ -1 +1,2 @@\n x\n+y\n"
	help := fmt.Sprintf(diffReviewHelp, 1, "o/r")
	_, err := parseEdited(help+withoutComments(diff), help+diff)
	require.ErrorContains(t, err, "the comment on line 21 of the annotated diff is not below a line of a hunk")
}

func TestParseDiffCommentsMapsAddedAndRemovedLines(t *testing.T) {
	diff := `diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -1,3 +1,3 @@
 package sample
-func old() {}
# Prefer a clearer name
+func renamed() {}
 context
# Keep context notes on the left
`
	comments, err := parseEdited(withoutComments(diff), diff)
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{
		{Path: "sample.go", Body: "Prefer a clearer name", OldLine: 2},
		{Path: "sample.go", Body: "Keep context notes on the left", OldLine: 3},
	}, comments)
}

func TestParseDiffCommentsReadsAnEmptyLineInAHunkAsUnchanged(t *testing.T) {
	const original = "diff --git a/blank.txt b/blank.txt\n--- a/blank.txt\n+++ b/blank.txt\n@@ -1,7 +1,7 @@\n a\n \n b\n \n-c\n+c changed\n \n d\n"
	diff := original + "# on d\n"
	// An editor that strips trailing whitespace leaves the empty unchanged lines empty.
	stripped := strings.ReplaceAll(diff, "\n \n", "\n\n")
	require.NotEqual(t, diff, stripped)
	for _, annotated := range []string{diff, stripped} {
		comments, err := parseEdited(original, annotated)
		require.NoError(t, err)
		require.Equal(t, []terminal.ReviewCommentDraft{{Path: "blank.txt", Body: "on d", OldLine: 7}}, comments)
	}
	// A comment on an empty unchanged line.
	comments, err := parseEdited(original, strings.Replace(stripped, " b\n\n", " b\n\n# on 4\n", 1))
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "blank.txt", Body: "on 4", OldLine: 4}, {Path: "blank.txt", Body: "on d", OldLine: 7}}, comments)
}

func TestParseDiffCommentsRefusesAChangedDiff(t *testing.T) {
	const diff = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n-b\n+c\n d\n# on d\ndiff --git a/y b/y\n--- a/y\n+++ b/y\n@@ -1 +1 @@\n-e\n+f\n"
	original := withoutComments(diff)
	_, err := parseEdited(original, diff)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, annotated, err string
	}{
		{"a removed line", strings.Replace(diff, " a\n", "", 1), `line 5 of the annotated diff, "-b", is not the next line of the diff, " a"`},
		{"an added line", strings.Replace(diff, " a\n", " a\n z\n", 1), `line 6 of the annotated diff, " z", is not the next line of the diff, "-b"`},
		{"a comment without #", strings.Replace(diff, "# on d", "on d", 1), `line 9 of the annotated diff, "on d", is not the next line of the diff, "diff --git a/y b/y"`},
		{"a line added at the end", diff + "trailing\n", `line 16 of the annotated diff, "trailing", is not a line of the diff`},
		{"a cut hunk", strings.TrimSuffix(diff, "+f\n"), `the annotated diff lacks the line "+f" of the diff at its end`},
		{"an emptied line", strings.Replace(diff, " a\n", "\n", 1), `line 6 of the annotated diff, "-b", is not the next line of the diff, " a"`},
	} {
		_, err := parseEdited(original, tc.annotated)
		require.ErrorContains(t, err, tc.err, tc.name)
		require.ErrorContains(t, err, "keep the lines of the diff as they are and start every comment line with #", tc.name)
	}
	comments, err := parseEdited(original, "\n")
	require.NoError(t, err, "an emptied file")
	require.Empty(t, comments)
}

func TestParseDiffCommentsRefusesAMissingEmptyUnchangedLine(t *testing.T) {
	const original = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n \n b\n"
	_, err := parseEdited(original, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n# on a\n b\n# on b\n")
	require.ErrorContains(t, err, "the annotated diff lacks an empty unchanged line of the diff above its line 7")
}

func TestParseDiffCommentsLeavesOutBlankLinesAddedToTheDiff(t *testing.T) {
	const original = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n a\n b\n"
	for _, annotated := range []string{
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n a\n# note\n\n b\n# on b\n",
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n a\n\n# note\n\n\n b\n\n# on b\n\n",
	} {
		comments, err := parseEdited(original, annotated)
		require.NoError(t, err, annotated)
		require.Equal(t, []terminal.ReviewCommentDraft{{Path: "x", Body: "note", OldLine: 1}, {Path: "x", Body: "on b", OldLine: 2}}, comments, annotated)
	}
	comments, err := parseEdited(original, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n a\n# first\n\n# second\n b\n")
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "x", Body: "first\n\nsecond", OldLine: 1}}, comments)
}

func TestParseDiffCommentsPlacesCommentsBesideBlankLinesAndEmptyUnchangedOnes(t *testing.T) {
	const original = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n \n b\n"
	// A blank line added below the comment, and the stripped unchanged line 2.
	comments, err := parseEdited(original, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n# note\n\n\n b\n# on b\n")
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "x", Body: "note", OldLine: 1}, {Path: "x", Body: "on b", OldLine: 3}}, comments)
	// Whether the comment is below line 1 or the unchanged line 2 is unknown.
	_, err = parseEdited(original, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n\n# note\n\n b\n")
	require.ErrorContains(t, err, "the comment on line 7 of the annotated diff may be below an empty line of the diff or below a blank line you added; remove the blank lines around it")
}

// numbersDiff ends its last hunk with two empty unchanged lines.
const numbersDiff = "diff --git a/n.txt b/n.txt\n--- a/n.txt\n+++ b/n.txt\n@@ -16,7 +16,7 @@\n 16\n 17\n 18\n-19\n+nineteen\n 20\n \n \n"

func TestParseDiffCommentsRefusesLinesRemovedFromTheLastHunk(t *testing.T) {
	for _, tc := range []struct {
		name, annotated, err string
	}{
		{"an unchanged line", strings.Replace(numbersDiff, " 17\n 18\n", " 18\n# on 18\n", 1), `line 6 of the annotated diff, " 18", is not the next line of the diff, " 17"`},
		{"a removed and an added line", strings.Replace(numbersDiff, "-19\n+nineteen\n 20\n", " 20\n# on 20\n", 1), `line 8 of the annotated diff, " 20", is not the next line of the diff, "-19"`},
		{"the last line before empty ones", strings.Replace(numbersDiff, " 20\n \n \n", "# on nineteen\n", 1), `the annotated diff lacks the line " 20" of the diff at its end`},
	} {
		_, err := parseEdited(numbersDiff, tc.annotated)
		require.ErrorContains(t, err, tc.err, tc.name)
	}
}

func TestParseDiffCommentsLeavesOutBlankLinesBelowComments(t *testing.T) {
	// Blank lines typed below comments, with trailing whitespace stripped, so that the empty
	// unchanged lines at the end are empty too.
	for name, annotated := range map[string]string{
		"stripped":         strings.NewReplacer(" 17\n", " 17\n# on 17\n\n", " 20\n \n \n", " 20\n# on 20\n\n\n\n").Replace(numbersDiff),
		"two blank lines":  strings.NewReplacer(" 17\n", " 17\n# on 17\n\n\n", " 20\n", " 20\n# on 20\n\n\n").Replace(numbersDiff),
		"blank lines kept": strings.NewReplacer(" 17\n", " 17\n\n\n# on 17\n\n", " 20\n", " 20\n# on 20\n\n").Replace(numbersDiff),
		"crlf":             strings.ReplaceAll(strings.NewReplacer(" 17\n", " 17\n# on 17\n\n", " 20\n", " 20\n# on 20\n").Replace(numbersDiff), "\n", "\r\n"),
		// Emacs deletes trailing whitespace and the empty lines at the end of the file.
		"emacs": strings.NewReplacer(" 17\n", " 17\n# on 17\n\n", " 20\n \n \n", " 20\n# on 20\n").Replace(numbersDiff),
	} {
		comments, err := parseEdited(numbersDiff, annotated)
		require.NoError(t, err, name)
		require.Equal(t, []terminal.ReviewCommentDraft{{Path: "n.txt", Body: "on 17", OldLine: 17}, {Path: "n.txt", Body: "on 20", OldLine: 20}}, comments, name)
	}
	// Two blank lines below line 17 and none below a comment.
	comments, err := parseEdited(numbersDiff, strings.NewReplacer(" 17\n", " 17\n\n\n", "+nineteen\n", "+nineteen\n# on nineteen\n").Replace(numbersDiff))
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "n.txt", Body: "on nineteen", NewLine: 19}}, comments)
}

func TestParseDiffCommentsRefusesACommentAmongEmptyLinesStrippedAtTheEnd(t *testing.T) {
	// Below line 20 with blank lines added, or below an empty line 21 or 22: the comment's line is unknown.
	for _, annotated := range []string{
		strings.Replace(numbersDiff, " 20\n \n \n", " 20\n\n\n# where\n", 1),
		strings.Replace(numbersDiff, " 20\n \n \n", " 20\n \n# where\n \n", 1),
	} {
		_, err := parseEdited(numbersDiff, annotated)
		require.ErrorContains(t, err, "of the annotated diff may be below an empty line of the diff or below a blank line you added", annotated)
	}
}

func TestParseDiffCommentsReadsEmptyLinesStrippedAtTheEnd(t *testing.T) {
	const diff = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,4 +1,4 @@\n a\n-b\n+c\n# on c\n \n \n"
	// An editor that strips trailing whitespace and empty lines at the end of the file.
	for _, annotated := range []string{diff, strings.TrimSuffix(diff, " \n"), strings.TrimSuffix(diff, " \n \n"), strings.ReplaceAll(diff, " \n", "\n")} {
		comments, err := parseEdited(withoutComments(diff), annotated)
		require.NoError(t, err, annotated)
		require.Equal(t, []terminal.ReviewCommentDraft{{Path: "x", Body: "on c", NewLine: 2}}, comments)
	}
}

func TestParseDiffCommentsKeepsACommentBelowNoNewlineAtEndOfFile(t *testing.T) {
	const original = "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n\\ No newline at end of file\n"
	comments, err := parseEdited(original, original+"# on b\n")
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "x", Body: "on b", NewLine: 1}}, comments)
	_, err = parseEdited(original, strings.Replace(original, "\\ No newline at end of file\n", "# on b\n", 1))
	require.ErrorContains(t, err, `the annotated diff lacks the line "\\ No newline at end of file" of the diff at its end`)
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
	comments, err := parseEdited(withoutComments(diff), diff)
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{{Path: "gone.go", Body: "Why remove this?", OldLine: 1}}, comments)
}

func TestParseDiffCommentsOnARenamedFileNameItsNewPath(t *testing.T) {
	diff := `diff --git a/old.go b/new.go
similarity index 80%
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
@@ -1,2 +1,2 @@
-package old
# on the old line
+package renamed
# on the new line
 func f() {}
diff --git a/moved.go b/elsewhere.go
similarity index 100%
rename from moved.go
rename to elsewhere.go
`
	comments, err := parseEdited(withoutComments(diff), diff)
	require.NoError(t, err)
	require.Equal(t, []terminal.ReviewCommentDraft{
		{Path: "new.go", Body: "on the old line", OldLine: 1},
		{Path: "new.go", Body: "on the new line", NewLine: 1},
	}, comments)
	// A file renamed without changes has no line to comment on.
	_, err = parseEdited(withoutComments(diff), diff+"# on the rename\n")
	require.ErrorContains(t, err, "the comment on line 17 of the annotated diff is not below a line of a hunk")
}

func TestParseDiffCommentsRefusesACommentOnABinaryFile(t *testing.T) {
	const original = "diff --git a/img.png b/img.png\nindex 1111111..2222222 100644\nBinary files a/img.png and b/img.png differ\n"
	_, err := parseEdited(original, original+"# looks off\n")
	require.ErrorContains(t, err, "the comment on line 4 of the annotated diff is not below a line of a hunk")
}

func TestSaveDiffReviewIncludesHelpHeader(t *testing.T) {
	path, annotated, err := saveDiffReview(t.TempDir(), 7, "owner/repo", strings.NewReader("diff --git a/x b/x\n"), nil)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, annotated, string(data))
	require.Contains(t, string(data), "PR #7 on owner/repo")
	require.Contains(t, string(data), "diff --git a/x b/x")
}

func TestExistingReviewCommentsAreShownButNotSentAgain(t *testing.T) {
	diff := `diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -1,3 +1,3 @@
 package sample
--- old note
+// new note
 func f() {}
`
	existing := []services.ReviewComment{
		{Author: "bob", Path: "sample.go", OldLine: 2, Body: "Why drop this?"},
		{Author: "carol", Path: "sample.go", Line: 2, Body: "Better\nThanks"},
		{Author: "dave", Path: "other.go", Line: 1, Body: "elsewhere"},
	}
	annotated := annotateDiff(diff, existing)
	require.Equal(t, `diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -1,3 +1,3 @@
 package sample
--- old note
### bob: Why drop this?
+// new note
### carol: Better
### Thanks
 func f() {}
`, annotated)
	comments, err := parseEdited(annotated, annotated+"# A new remark\n")
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, "A new remark", comments[0].Body)
}

func TestExistingReviewCommentsOnUnchangedLinesShowOnEitherSide(t *testing.T) {
	diff := `diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -10,3 +12,3 @@
 first
-gone
+added
 last
`
	existing := []services.ReviewComment{
		{Author: "bob", Path: "sample.go", OldLine: 10, Body: "old side"},
		{Author: "carol", Path: "sample.go", Line: 14, Body: "new side"},
	}
	require.Equal(t, `diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -10,3 +12,3 @@
 first
### bob: old side
-gone
+added
 last
### carol: new side
`, annotateDiff(diff, existing))
}
