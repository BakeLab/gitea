// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratedReferenceIsCompleteAndReproducibleWithoutConfiguration(t *testing.T) {
	// A malformed personal configuration must never enter packaged documentation.
	config := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(config, "config.yml"), []byte("malformed: ["), 0o600))
	t.Setenv("GITEA_CLI_CONFIG_DIR", config)
	t.Setenv("SOURCE_DATE_EPOCH", "1786795200")
	first, second := t.TempDir(), t.TempDir()
	require.NoError(t, generate(first))
	require.NoError(t, generate(second))
	links := regexp.MustCompile(`\]\((gt[^)]+\.md)\)`)
	files := 0
	require.NoError(t, filepath.WalkDir(first, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(first, path)
		require.NoError(t, err)
		original, err := os.ReadFile(path)
		require.NoError(t, err)
		regenerated, err := os.ReadFile(filepath.Join(second, relative))
		require.NoError(t, err)
		require.Equal(t, string(original), string(regenerated), relative)
		require.NotContains(t, string(original), "Auto generated")
		if filepath.Ext(path) == ".md" {
			require.NotContains(t, string(original), "]()", relative)
			for _, link := range links.FindAllStringSubmatch(string(original), -1) {
				_, err := os.Stat(filepath.Join(filepath.Dir(path), link[1]))
				require.NoError(t, err, "broken reference link in %s: %s", relative, link[1])
			}
		}
		files++
		return nil
	}))
	require.Greater(t, files, 100)
	for _, path := range []string{"reference/gt.md", "reference/gt_issue_create.md", "reference/gt_milestone_close.md", "man/gt-auth-login.1", "completion/gt.bash", "completion/_gt", "completion/gt.fish", "completion/gt.ps1"} {
		content, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(path)))
		require.NoError(t, err, path)
		require.NotEmpty(t, strings.TrimSpace(string(content)), path)
	}
	root, err := os.ReadFile(filepath.Join(first, "reference", "gt.md"))
	require.NoError(t, err)
	require.Contains(t, string(root), "--no-input")
	require.Contains(t, string(root), "--repo")
	for _, topic := range []string{"environment", "exit-codes", "extensions", "formatting"} {
		require.Contains(t, string(root), "* [gt help "+topic+"](gt_"+topic+".md)", topic)
		for _, page := range []string{"reference/gt_" + topic + ".md", "man/gt-" + topic + ".1"} {
			content, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(page)))
			require.NoError(t, err)
			require.NotRegexp(t, `(?i)(###|\.SH) options`, string(content), page)
			require.Regexp(t, `(?i)(###|\.SH) see also`, string(content), page)
			require.NotContains(t, string(content), "[flags]", page)
		}
	}
	codes, err := os.ReadFile(filepath.Join(first, "reference", "gt_exit-codes.md"))
	require.NoError(t, err)
	require.Contains(t, string(codes), "\n\n      0    success", "the table renders as code")
	man, err := os.ReadFile(filepath.Join(first, "man", "gt.1"))
	require.NoError(t, err)
	require.Contains(t, string(man), `"Aug 2026"`)
	require.Contains(t, string(man), `\fBgt-workflow(1)\fP, \fBgt-environment(1)\fP, \fBgt-exit-codes(1)\fP`)
	view, err := os.ReadFile(filepath.Join(first, "man", "gt-issue-view.1"))
	require.NoError(t, err)
	require.Contains(t, string(view), `.TH "GT-ISSUE-VIEW"`)
	require.Contains(t, string(view), `\fBgt issue view {<number> | <url>} [flags]\fP`)
	extensions, err := os.ReadFile(filepath.Join(first, "man", "gt-extensions.1"))
	require.NoError(t, err)
	require.Contains(t, string(extensions), "except for GITEA_TOKEN,\nGITEA_INSTANCE_*, GH_TOKEN,")
	settings, err := os.ReadFile(filepath.Join(first, "reference", "gt_config.md"))
	require.NoError(t, err)
	require.Contains(t, string(settings), `Account settings (--host \<url> --user \<name>)`, "a placeholder in prose is not inline HTML")
	reference, err := os.ReadFile(filepath.Join(first, "reference", "gt_issue_view.md"))
	require.NoError(t, err)
	require.Contains(t, string(reference), "```\ngt issue view {<number> | <url>} [flags]\n```", "the usage line is code")
	milestone, err := os.ReadFile(filepath.Join(first, "reference", "gt_milestone_close.md"))
	require.NoError(t, err)
	require.Contains(t, string(milestone), "--by-name")
	for _, page := range []string{"reference/gt_version.md", "reference/gt_run_logs.md", "man/gt-run-logs.1"} {
		content, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(page)))
		require.NoError(t, err)
		require.NotContains(t, string(content), "json", page)
	}
	logs, err := os.ReadFile(filepath.Join(first, "man", "gt-run-logs.1"))
	require.NoError(t, err)
	require.Contains(t, string(logs), ".SS ARGUMENTS\n.EX\nA run can be supplied")
	require.Contains(t, string(logs), `\fB--repo\fP`)
	issue, err := os.ReadFile(filepath.Join(first, "reference", "gt_issue_view.md"))
	require.NoError(t, err)
	require.Contains(t, string(issue), "### JSON FIELDS\n\n    assignees, author,")
	require.Contains(t, string(issue), "--jq")
	require.NotContains(t, string(issue), "--yes")
	exec, err := os.ReadFile(filepath.Join(first, "reference", "gt_extension_exec.md"))
	require.NoError(t, err)
	for _, flag := range []string{"--host", "--user", "--repo", "--cwd", "--no-input", "--yes"} {
		require.Contains(t, string(exec), flag)
	}
	for _, flag := range []string{"--sudo", "--debug", "--interactive", "--remote", "--json"} {
		require.NotContains(t, string(exec), flag)
	}
}
