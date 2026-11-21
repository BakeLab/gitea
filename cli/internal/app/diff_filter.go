// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
)

// diffFile is the part of a unified diff that concerns one file.
type diffFile struct {
	// path is the file as it is after the change, or before it when the change deletes it; oldPath
	// differs for a rename.
	path, oldPath string
	// status is A for a new file, D for a deleted one, R for a rename and M for the rest.
	status string
	// added and removed count the changed lines; binary files have none.
	added, removed int
	binary         bool
	text           []byte
}

// parseDiff splits a unified diff, as Gitea's .diff endpoints return it, into its files. Text before
// the first file is not part of any, and is dropped.
func parseDiff(data []byte) []diffFile {
	var files []diffFile
	var current *diffFile
	inHunk := false
	for raw := range bytes.Lines(data) {
		line := strings.TrimRight(string(raw), "\r\n")
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			files = append(files, diffFile{status: "M"})
			current, inHunk = &files[len(files)-1], false
			current.oldPath, current.path = splitDiffHeader(rest)
		}
		if current == nil {
			continue
		}
		current.text = append(current.text, raw...) // unchanged, line endings too, so that a filtered diff still applies
		switch {
		case inHunk && strings.HasPrefix(line, "+"):
			current.added++
		case inHunk && strings.HasPrefix(line, "-"):
			current.removed++
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk:
		case strings.HasPrefix(line, "new file mode"):
			current.status = "A"
		case strings.HasPrefix(line, "deleted file mode"):
			current.status = "D"
		case strings.HasPrefix(line, "rename from "):
			current.status, current.oldPath = "R", unquoteDiffPath(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			current.status, current.path = "R", unquoteDiffPath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "--- "):
			if old := diffHeaderPath(line, "a/"); old != "/dev/null" {
				current.oldPath = old
			}
		case strings.HasPrefix(line, "+++ "):
			if next := diffHeaderPath(line, "b/"); next == "/dev/null" {
				current.path = current.oldPath
			} else {
				current.path = next
			}
		case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
			current.binary = true
		}
	}
	return files
}

// splitDiffHeader reads the two paths of a "diff --git a/X b/Y" line. Either can be quoted, and Git
// quotes any path that holds a quote, so a quote starts or ends a path. An unquoted name that holds
// " b/" is ambiguous, so paths of the same name are tried first and --- and +++ lines correct the rest.
func splitDiffHeader(rest string) (oldPath, newPath string) {
	if left, err := strconv.QuotedPrefix(rest); err == nil {
		right := strings.TrimPrefix(rest[len(left):], " ")
		return strings.TrimPrefix(unquoteDiffPath(left), "a/"), strings.TrimPrefix(unquoteDiffPath(right), "b/")
	}
	if left, right, ok := strings.Cut(rest, ` "`); ok {
		return strings.TrimPrefix(left, "a/"), strings.TrimPrefix(unquoteDiffPath(`"`+right), "b/")
	}
	if len(rest)%2 == 1 {
		half := len(rest) / 2
		left, right := rest[:half], rest[half+1:]
		if strings.HasPrefix(left, "a/") && strings.HasPrefix(right, "b/") && left[2:] == right[2:] {
			return left[2:], right[2:]
		}
	}
	left, right, _ := strings.Cut(rest, " b/")
	return strings.TrimPrefix(left, "a/"), right
}

// unquoteDiffPath reads a path that Git quoted like a Go string, as it does one with unusual characters.
func unquoteDiffPath(path string) string {
	if unquoted, err := strconv.Unquote(path); err == nil && strings.HasPrefix(path, `"`) {
		return unquoted
	}
	return path
}

// diffGlob turns the wildcards of a pattern, as regexp.QuoteMeta leaves them, into a regular expression.
var diffGlob = strings.NewReplacer(`\*\*/`, "(?:.*/)?", `\*\*`, ".*", `\*`, "[^/]*", `\?`, "[^/]")

// diffMatcher returns the test of a pattern of --path or --exclude: a file or directory name, a
// directory ending in /, or a glob where * stays within a directory, ** crosses them and ? is one
// character. A pattern without a slash also matches the name of the file in any directory.
func diffMatcher(pattern string) func(path string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	bare := !strings.Contains(pattern, "/")
	switch {
	case pattern == "" || pattern == ".":
		return func(string) bool { return true }
	case strings.HasSuffix(pattern, "/"):
		return func(path string) bool { return strings.HasPrefix(path, pattern) }
	case !strings.ContainsAny(pattern, "*?"):
		return func(path string) bool {
			return path == pattern || strings.HasPrefix(path, pattern+"/") || (bare && strings.HasSuffix(path, "/"+pattern))
		}
	}
	anchor := "^"
	if bare {
		anchor = "^(?:.*/)?"
	}
	expression, err := regexp.Compile(anchor + diffGlob.Replace(regexp.QuoteMeta(pattern)) + "$")
	if err != nil {
		return func(string) bool { return false }
	}
	return expression.MatchString
}

// diffOptions are the flags that narrow a diff or change how it is shown.
type diffOptions struct {
	paths, excludes []string
	nameOnly        bool
	nameStatus      bool
	stat            bool
	color           string
}

// register adds the flags to cmd. The Gitea API takes no options for what a diff contains, whitespace
// among them, so these work on the diff after it is read.
func (o *diffOptions) register(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&o.paths, "path", nil, "Show only files matching `pattern`: a file, a directory/, or a glob such as '*.go' or 'internal/**/*_test.go' (repeatable)")
	cmd.Flags().StringArrayVarP(&o.excludes, "exclude", "e", nil, "Leave out files matching `pattern`, which is written like --path (repeatable)")
	cmd.Flags().BoolVar(&o.nameOnly, "name-only", false, "List only the names of the changed files")
	cmd.Flags().BoolVar(&o.nameStatus, "name-status", false, "List the changed files with A, M, D or R for how they changed")
	cmd.Flags().BoolVar(&o.stat, "stat", false, "Show how many lines each file changed, and the total, instead of the diff")
	enumFlag(cmd, &o.color, "color", "", "auto", "Color the diff", "auto", "always", "never")
}

// narrowed reports whether --path or --exclude leave out files.
func (o diffOptions) narrowed() bool {
	return len(o.paths)+len(o.excludes) > 0
}

// summary reports whether the output describes the files instead of showing the diff.
func (o diffOptions) summary() bool {
	return o.nameOnly || o.nameStatus || o.stat
}

// validate rejects flags that cannot go together, before any request.
func (o diffOptions) validate() error {
	modes := 0
	for _, set := range []bool{o.nameOnly, o.nameStatus, o.stat} {
		if set {
			modes++
		}
	}
	if modes > 1 {
		return clierr.Wrap(clierr.Usage, errors.New("--name-only, --name-status and --stat cannot be combined"))
	}
	for _, pattern := range slices.Concat(o.paths, o.excludes) {
		if strings.TrimSpace(pattern) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--path and --exclude require a pattern"))
		}
	}
	return nil
}

// selected returns the files that --path and --exclude leave in.
func (o diffOptions) selected(files []diffFile) []diffFile {
	matcher := func(patterns []string) func(diffFile) bool {
		tests := make([]func(string) bool, len(patterns))
		for i, pattern := range patterns {
			tests[i] = diffMatcher(pattern)
		}
		return func(file diffFile) bool {
			return slices.ContainsFunc(tests, func(test func(string) bool) bool {
				return test(file.path) || (file.oldPath != file.path && file.oldPath != "" && test(file.oldPath))
			})
		}
	}
	included, excluded := matcher(o.paths), matcher(o.excludes)
	kept := make([]diffFile, 0, len(files))
	for _, file := range files {
		if (len(o.paths) == 0 || included(file)) && !excluded(file) {
			kept = append(kept, file)
		}
	}
	return kept
}

// writeDiff shows a unified diff as the flags ask: whole, or narrowed to files, or as names or counts.
// On a terminal it is colored and paged; piped, it is written as it is, so that it can be applied.
func (a *application) writeDiff(cmd *cobra.Command, data []byte, o diffOptions) error {
	d := a.display()
	var files []diffFile
	if o.narrowed() || o.summary() {
		files = o.selected(parseDiff(data))
		if len(files) == 0 && o.narrowed() {
			a.warn(cmd, "no changed file matches --path and --exclude")
		}
	}
	if o.summary() {
		_, err := io.WriteString(cmd.OutOrStdout(), o.summarize(files, d.TTY))
		return err
	}
	if o.narrowed() {
		data = nil
		for _, file := range files {
			data = append(data, file.text...)
		}
	}
	colored := o.color == "always" || (o.color == "auto" && d.Color)
	if !d.TTY && !colored {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}
	text, err := diffText(bytes.NewReader(data), colored)
	if err != nil {
		return err
	}
	if !d.TTY {
		_, err = io.WriteString(cmd.OutOrStdout(), text)
		return err
	}
	return a.writeHuman(cmd, text)
}

// summarize renders the files as --name-only, --name-status or --stat ask.
func (o diffOptions) summarize(files []diffFile, tty bool) string {
	name := func(s string) string {
		if tty {
			return terminal.SafeLine(s)
		}
		return s // piped names stay exact, so that they can be passed on to other commands
	}
	if o.stat {
		return diffStat(files, name)
	}
	var b strings.Builder
	for _, file := range files {
		switch {
		case o.nameOnly:
			b.WriteString(name(file.path) + "\n")
		case file.status == "R":
			b.WriteString("R\t" + name(file.oldPath) + "\t" + name(file.path) + "\n")
		default:
			b.WriteString(file.status + "\t" + name(file.path) + "\n")
		}
	}
	return b.String()
}

// diffStat renders the counts of changed lines like git diff --stat: a bar per file, scaled to
// fit, and a total.
func diffStat(files []diffFile, name func(string) string) string {
	if len(files) == 0 {
		return ""
	}
	nameWidth, countWidth, most := 0, 1, 1
	for _, file := range files {
		nameWidth = max(nameWidth, runewidth.StringWidth(name(file.path)))
		most = max(most, file.added+file.removed)
		countWidth = max(countWidth, len(strconv.Itoa(file.added+file.removed)))
	}
	const barWidth = 40
	scale := func(n int) int {
		if most <= barWidth || n == 0 {
			return n
		}
		return max(1, n*barWidth/most)
	}
	var b strings.Builder
	added, removed := 0, 0
	for _, file := range files {
		added, removed = added+file.added, removed+file.removed
		changes := "Bin"
		if !file.binary {
			changes = fmt.Sprintf("%*d %s%s", countWidth, file.added+file.removed, strings.Repeat("+", scale(file.added)), strings.Repeat("-", scale(file.removed)))
		}
		shown := name(file.path)
		fmt.Fprintf(&b, " %s%s | %s\n", shown, strings.Repeat(" ", nameWidth-runewidth.StringWidth(shown)), strings.TrimRight(changes, " "))
	}
	fmt.Fprintf(&b, " %s changed, %s(+), %s(-)\n", terminal.Plural(len(files), "file"), terminal.Plural(added, "insertion"), terminal.Plural(removed, "deletion"))
	return b.String()
}
