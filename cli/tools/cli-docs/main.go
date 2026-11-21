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
	root := app.ReferenceRoot()
	for _, directory := range []string{"reference", "man", "completion"} {
		if err := os.MkdirAll(filepath.Join(output, directory), 0o755); err != nil {
			return err
		}
	}
	// Date the pages like gt man; make docs sets SOURCE_DATE_EPOCH to the last commit.
	date := app.ManualDate(os.Getenv)
	if err := walk(root, func(command *cobra.Command) error {
		var page strings.Builder
		if err := app.GenMarkdown(command, &page); err != nil {
			return err
		}
		name := strings.ReplaceAll(command.CommandPath(), " ", "_") + ".md"
		return os.WriteFile(filepath.Join(output, "reference", name), []byte(strings.TrimRight(page.String(), "\n")+"\n"), 0o644)
	}); err != nil {
		return err
	}
	if err := walk(app.ManualRoot(), func(command *cobra.Command) error {
		// GenMan fills an empty Title from the command path by mutating the header, so each page needs its own.
		header := &doc.GenManHeader{Section: "1", Date: &date, Source: "gitea-cli", Manual: "gitea-cli Manual"}
		name := strings.ReplaceAll(command.CommandPath(), " ", "-") + ".1"
		return writeFile(filepath.Join(output, "man", name), func(w io.Writer) error { return app.GenMan(command, header, w) })
	}); err != nil {
		return err
	}
	var topics strings.Builder
	for _, topic := range app.ManualCommands(root) {
		if !topic.Runnable() && topic.Annotations["gt.topic"] != "" {
			fmt.Fprintf(&topics, "* [gt help %s](gt_%s.md)\t - %s\n", topic.Name(), topic.Name(), topic.Short)
		}
	}
	index := filepath.Join(output, "reference", "gt.md")
	content, err := os.ReadFile(index)
	if err != nil {
		return err
	}
	if err := os.WriteFile(index, []byte(string(content)+"\n### HELP TOPICS\n\n"+topics.String()), 0o644); err != nil {
		return err
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

// walk calls page for command and every command below it that has a page in the manual.
func walk(command *cobra.Command, page func(*cobra.Command) error) error {
	if err := page(command); err != nil {
		return err
	}
	for _, child := range app.ManualCommands(command) {
		if err := walk(child, page); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(write(file), file.Close())
}
