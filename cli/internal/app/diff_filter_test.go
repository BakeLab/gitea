// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const sampleDiff = `diff --git a/internal/app/main.go b/internal/app/main.go
index 1111111..2222222 100644
--- a/internal/app/main.go
+++ b/internal/app/main.go
@@ -1,3 +1,4 @@
 package app
-var a = 1
+var a = 2
+var b = 3
 func f() {}
diff --git a/internal/app/main_test.go b/internal/app/main_test.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/internal/app/main_test.go
@@ -0,0 +1,2 @@
+package app
+func TestF() {}
diff --git a/docs/old name.md b/docs/new name.md
similarity index 90%
rename from docs/old name.md
rename to docs/new name.md
index 4444444..5555555 100644
--- a/docs/old name.md
+++ b/docs/new name.md
@@ -1 +1 @@
-old
+new
diff --git a/logo.png b/logo.png
index 6666666..7777777 100644
Binary files a/logo.png and b/logo.png differ
diff --git a/old.txt b/old.txt
deleted file mode 100644
index 8888888..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-gone
-gone
`

func TestParseDiff(t *testing.T) {
	files := parseDiff([]byte(sampleDiff))
	type summary struct {
		path, oldPath, status string
		added, removed        int
		binary                bool
	}
	var got []summary
	for _, f := range files {
		got = append(got, summary{f.path, f.oldPath, f.status, f.added, f.removed, f.binary})
	}
	require.Equal(t, []summary{
		{"internal/app/main.go", "internal/app/main.go", "M", 2, 1, false},
		{"internal/app/main_test.go", "internal/app/main_test.go", "A", 2, 0, false},
		{"docs/new name.md", "docs/old name.md", "R", 1, 1, false},
		{"logo.png", "logo.png", "M", 0, 0, true},
		{"old.txt", "old.txt", "D", 0, 2, false},
	}, got)
	var joined []byte
	for _, f := range files {
		joined = append(joined, f.text...)
	}
	require.Equal(t, sampleDiff, string(joined), "the files hold the diff as it was, so that a filtered diff still applies")

	crlf := "diff --git a/a.txt b/a.txt\r\nnew file mode 100644\r\n--- /dev/null\r\n+++ b/a.txt\r\n@@ -0,0 +1 @@\r\n+new\r\n"
	files = parseDiff([]byte(crlf))
	require.Len(t, files, 1)
	require.Equal(t, crlf, string(files[0].text), "a filtered diff of CRLF lines still applies")
	require.Equal(t, "a.txt", files[0].path)
	require.Equal(t, "A", files[0].status)
	require.Equal(t, 1, files[0].added)

	files = parseDiff([]byte("diff --git a/my file.txt b/my file.txt\n--- a/my file.txt\t\n+++ b/my file.txt\t\n@@ -1 +1 @@\n-old\n+new\n"))
	require.Len(t, files, 1)
	require.Equal(t, "my file.txt", files[0].path, "git ends the header of a path with a space in a tab")
	require.Equal(t, "my file.txt", files[0].oldPath)

	files = parseDiff([]byte("diff --git a/big b/big\n@@ -1 +1 @@\n+" + strings.Repeat("x", 64*1024*1024) + "\ndiff --git a/next b/next\n"))
	require.Equal(t, "next", files[len(files)-1].path, "a long line does not cut the diff short")

	files = parseDiff([]byte(`diff --git "a/a\"b.bin" "b/a\"b.bin"
new file mode 100644
index 0000000..9f6cfe2
Binary files /dev/null and "b/a\"b.bin" differ
diff --git "a/caf\303\251.txt" "b/caf\303\251.txt"
new file mode 100644
index 0000000..e69de29
diff --git "a/logo \303\251.png" "b/logo \303\251.png"
new file mode 100644
index 0000000..bdc955b
Binary files /dev/null and "b/logo \303\251.png" differ
`))
	var paths []string
	for _, f := range files {
		paths = append(paths, f.path)
	}
	require.Equal(t, []string{`a"b.bin`, "café.txt", "logo é.png"}, paths, "quoted files with no --- or +++ lines")
}

func TestSplitDiffHeader(t *testing.T) {
	for _, tc := range []struct{ rest, oldPath, newPath string }{
		{`a/x y.go b/x y.go`, "x y.go", "x y.go"},
		{`a/a b/c.go b/a b/c.go`, "a b/c.go", "a b/c.go"},
		{`a/old.go b/new.go`, "old.go", "new.go"},
		{`"a/caf\303\251.txt" "b/caf\303\251.txt"`, "café.txt", "café.txt"},
		{`a/my cafe.txt "b/renam\303\251.txt"`, "my cafe.txt", "renamé.txt"},
		{`"a/renam\303\251.txt" b/my cafe2.txt`, "renamé.txt", "my cafe2.txt"},
	} {
		oldPath, newPath := splitDiffHeader(tc.rest)
		require.Equal(t, []string{tc.oldPath, tc.newPath}, []string{oldPath, newPath}, tc.rest)
	}
}

func TestMatchDiffPath(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"internal/app/main.go", "internal/app/main.go", true},
		{"internal/", "internal/app/main.go", true},
		{"internal", "internal/app/main.go", true},
		{"internal/app", "internal/application/x.go", false},
		{"main.go", "internal/app/main.go", true},
		{"*.go", "internal/app/main.go", true},
		{"*_test.go", "internal/app/main_test.go", true},
		{"*_test.go", "internal/app/main.go", false},
		{"internal/*.go", "internal/app/main.go", false},
		{"internal/**/*.go", "internal/app/main.go", true},
		{"internal/**/*.go", "internal/main.go", true},
		{"**/main.go", "main.go", true},
		{"**/main.go", "internal/app/main.go", true},
		{"**/main.go", "domain.go", false},
		{"internal/**/main.go", "internal/app/main.go", true},
		{"internal/**/main.go", "internal/main.go", true},
		{"internal/**/main.go", "internal/domain.go", false},
		{"dökümanlar/*.md", "dökümanlar/a.md", true},
		{"ä*.go", "internal/äb.go", true},
		{"docs/?ew name.md", "docs/new name.md", true},
		{"./logo.png", "logo.png", true},
		{".", "internal/app/main.go", true},
		{"./", "logo.png", true},
		{"*.PNG", "logo.png", false},
		{"a.b", "aXb", false},
	} {
		require.Equal(t, tc.want, diffMatcher(tc.pattern)(tc.path), tc.pattern+" on "+tc.path)
	}
}

func TestDiffOptionsSelectFiles(t *testing.T) {
	files := parseDiff([]byte(sampleDiff))
	names := func(o diffOptions) []string {
		var out []string
		for _, f := range o.selected(files) {
			out = append(out, f.path)
		}
		return out
	}
	require.Len(t, names(diffOptions{}), 5)
	require.Equal(t, []string{"internal/app/main.go", "internal/app/main_test.go"}, names(diffOptions{paths: []string{"internal/"}}))
	require.Equal(t, []string{"internal/app/main.go"}, names(diffOptions{paths: []string{"internal/"}, excludes: []string{"*_test.go"}}))
	require.Equal(t, []string{"docs/new name.md", "logo.png"}, names(diffOptions{paths: []string{"*.png", "docs/"}}))
	require.Equal(t, []string{"docs/new name.md"}, names(diffOptions{paths: []string{"docs/old name.md"}}), "a renamed file is found by its old name too")
	require.Empty(t, names(diffOptions{paths: []string{"nothing"}}))
}

func TestDiffStat(t *testing.T) {
	got := diffStat(parseDiff([]byte(sampleDiff)), func(s string) string { return s })
	require.Equal(t, ` internal/app/main.go      | 3 ++-
 internal/app/main_test.go | 2 ++
 docs/new name.md          | 2 +-
 logo.png                  | Bin
 old.txt                   | 2 --
 5 files changed, 5 insertions(+), 4 deletions(-)
`, got)
	require.Contains(t, diffStat([]diffFile{{path: "a", added: 1}}, func(s string) string { return s }), " 1 file changed, 1 insertion(+), 0 deletions(-)\n")
}
