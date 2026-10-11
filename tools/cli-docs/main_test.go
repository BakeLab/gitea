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
	t.Setenv("TEA_CONFIG_DIR", config)
	t.Setenv("SOURCE_DATE_EPOCH", "1786795200")
	first, second := t.TempDir(), t.TempDir()
	require.NoError(t, generate(first))
	require.NoError(t, generate(second))
	links := regexp.MustCompile(`\]\((tea[^)]+\.md)\)`)
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
			for _, link := range links.FindAllStringSubmatch(string(original), -1) {
				_, err := os.Stat(filepath.Join(filepath.Dir(path), link[1]))
				require.NoError(t, err, "broken reference link in %s: %s", relative, link[1])
			}
		}
		files++
		return nil
	}))
	require.Greater(t, files, 100)
	for _, path := range []string{"reference/tea.md", "reference/tea_issue_create.md", "reference/tea_milestone_close.md", "man/tea-auth-login.1", "completion/tea.bash", "completion/_tea", "completion/tea.fish", "completion/tea.ps1"} {
		content, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(path)))
		require.NoError(t, err, path)
		require.NotEmpty(t, strings.TrimSpace(string(content)), path)
	}
	root, err := os.ReadFile(filepath.Join(first, "reference", "tea.md"))
	require.NoError(t, err)
	require.Contains(t, string(root), "--no-input")
	require.Contains(t, string(root), "--repo")
	man, err := os.ReadFile(filepath.Join(first, "man", "tea.1"))
	require.NoError(t, err)
	require.Contains(t, string(man), `"Aug 2026"`)
	milestone, err := os.ReadFile(filepath.Join(first, "reference", "tea_milestone_close.md"))
	require.NoError(t, err)
	require.Contains(t, string(milestone), "--by-name")
}
