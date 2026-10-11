// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// cli-docs generates tea's reference, man pages and shell completions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitea.dev/tea/internal/app"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func main() {
	output := flag.String("out", "docs/generated", "Output directory")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "cli-docs accepts no positional arguments")
		os.Exit(2)
	}
	if err := generate(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(output string) error {
	// Documentation describes built-ins, independently of personal settings,
	// credentials, repository discovery, and environment-specific aliases.
	root := app.NewRoot(app.Dependencies{})
	var prepare func(*cobra.Command)
	prepare = func(command *cobra.Command) {
		command.DisableAutoGenTag = true
		for _, child := range command.Commands() {
			prepare(child)
		}
	}
	prepare(root)
	for _, directory := range []string{"reference", "man", "completion"} {
		if err := os.MkdirAll(filepath.Join(output, directory), 0o755); err != nil {
			return err
		}
	}
	if err := doc.GenMarkdownTree(root, filepath.Join(output, "reference")); err != nil {
		return err
	}
	pages, err := filepath.Glob(filepath.Join(output, "reference", "*.md"))
	if err != nil {
		return err
	}
	for _, page := range pages {
		content, err := os.ReadFile(page)
		if err != nil {
			return err
		}
		if err := os.WriteFile(page, []byte(strings.TrimRight(string(content), "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	// Date the pages like tea man; make docs sets SOURCE_DATE_EPOCH to the last commit.
	date := app.ManualDate(os.Getenv)
	header := &doc.GenManHeader{Title: "TEA", Section: "1", Date: &date, Source: "Tea", Manual: "Tea Manual"}
	if err := doc.GenManTree(root, header, filepath.Join(output, "man")); err != nil {
		return err
	}
	completions := []struct {
		name  string
		write func(io.Writer) error
	}{
		{"tea.bash", func(w io.Writer) error { return root.GenBashCompletionV2(w, true) }},
		{"_tea", root.GenZshCompletion},
		{"tea.fish", func(w io.Writer) error { return root.GenFishCompletion(w, true) }},
		{"tea.ps1", root.GenPowerShellCompletionWithDesc},
	}
	for _, completion := range completions {
		if err := writeFile(filepath.Join(output, "completion", completion.name), completion.write); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(output, "README.md"), []byte(strings.TrimSpace(`
# Tea command reference

Generated from the Cobra command tree with `+"`make docs`"+`.
These files describe the built-in commands and their supported flags.
Personal aliases are expanded dynamically by the installed shell completions.

- [Command reference](reference/tea.md)
- Man pages: `+"`man/tea.1`"+` and one page per command
- Bash: `+"`completion/tea.bash`"+`
- Zsh: `+"`completion/_tea`"+`
- Fish: `+"`completion/tea.fish`"+`
- PowerShell: `+"`completion/tea.ps1`"+`
`)+"\n"), 0o644)
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(write(file), file.Close())
}
