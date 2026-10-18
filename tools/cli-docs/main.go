// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// cli-docs generates gt's reference, man pages and shell completions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/app"
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
	// Date the pages like gt man; make docs sets SOURCE_DATE_EPOCH to the last commit.
	date := app.ManualDate(os.Getenv)
	header := &doc.GenManHeader{Title: "GT", Section: "1", Date: &date, Source: "gitea-cli", Manual: "gitea-cli Manual"}
	if err := doc.GenManTree(root, header, filepath.Join(output, "man")); err != nil {
		return err
	}
	// Cobra skips the hidden help topics (environment, exit-codes, formatting); give them pages too.
	for _, topic := range app.ManualCommands(root) {
		if topic.Runnable() || topic.Annotations["gt.topic"] == "" {
			continue
		}
		var page strings.Builder
		if err := doc.GenMarkdownCustom(topic, &page, func(string) string { return "" }); err != nil {
			return err
		}
		name := "gt_" + topic.Name() + ".md"
		if err := os.WriteFile(filepath.Join(output, "reference", name), []byte(strings.TrimRight(page.String(), "\n")+"\n"), 0o644); err != nil {
			return err
		}
		topicHeader := &doc.GenManHeader{Title: "GT-" + strings.ToUpper(topic.Name()), Section: "1", Date: &date, Source: "gitea-cli", Manual: "gitea-cli Manual"}
		if err := writeFile(filepath.Join(output, "man", "gt-"+topic.Name()+".1"), func(w io.Writer) error { return doc.GenMan(topic, topicHeader, w) }); err != nil {
			return err
		}
	}
	completions := []struct {
		name  string
		write func(io.Writer) error
	}{
		{"gt.bash", func(w io.Writer) error { return root.GenBashCompletionV2(w, true) }},
		{"_gt", root.GenZshCompletion},
		{"gt.fish", func(w io.Writer) error { return root.GenFishCompletion(w, true) }},
		{"gt.ps1", root.GenPowerShellCompletionWithDesc},
	}
	for _, completion := range completions {
		if err := writeFile(filepath.Join(output, "completion", completion.name), completion.write); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(output, "README.md"), []byte(strings.TrimSpace(`
# gitea-cli command reference

Generated from the Cobra command tree with `+"`make docs`"+`.
These files describe the built-in commands and their supported flags.
Personal aliases are expanded dynamically by the installed shell completions.

- [Command reference](reference/gt.md)
- Man pages: `+"`man/gt.1`"+` and one page per command
- Bash: `+"`completion/gt.bash`"+`
- Zsh: `+"`completion/_gt`"+`
- Fish: `+"`completion/gt.fish`"+`
- PowerShell: `+"`completion/gt.ps1`"+`
`)+"\n"), 0o644)
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(write(file), file.Close())
}
