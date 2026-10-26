// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func (a *application) manCommand() *cobra.Command {
	var output string
	command := &cobra.Command{
		Use: "man [command...]", Short: "Generate the full manual or a command's man page", Hidden: true,
		Annotations: textOutput("man pages are troff"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Use built-ins only so personal aliases and settings cannot change the manual.
			root := ManualRoot()
			target, remaining, err := root.Find(args)
			if err != nil || len(remaining) != 0 {
				return clierr.Wrap(clierr.Usage, errors.New("unknown manual command; use gt help to list commands"))
			}
			date := ManualDate(a.deps.Getenv)
			// Manual pages are for everyone to read, unlike the private files other commands write.
			write := func(writer io.Writer) error {
				var render func(*cobra.Command) error
				render = func(page *cobra.Command) error {
					if err := cmd.Context().Err(); err != nil {
						return err
					}
					// GenMan fills an empty Title from the command path by mutating the header, so each page needs its own.
					header := &doc.GenManHeader{Section: "1", Date: &date, Source: "gitea-cli", Manual: "gitea-cli Manual"}
					if err := doc.GenMan(page, header, writer); err != nil {
						return err
					}
					if len(args) == 0 {
						for _, child := range ManualCommands(page) {
							if err := render(child); err != nil {
								return err
							}
						}
					}
					return nil
				}
				return render(target)
			}
			if output == "" || output == "-" {
				return write(cmd.OutOrStdout())
			}
			return publish(cmd, output, false, false, write)
		},
	}
	command.Flags().StringVarP(&output, "output-file", "o", "", "Write the man page to a new file (default stdout)")
	return command
}

// ManualDate dates man pages from SOURCE_DATE_EPOCH, falling back to a fixed date so the
// manual stays reproducible when the variable is unset.
func ManualDate(getenv func(string) string) time.Time {
	if seconds, err := strconv.ParseInt(getenv("SOURCE_DATE_EPOCH"), 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC()
	}
	return time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
}

var manualPlaceholder = regexp.MustCompile(`<([A-Za-z])`)

// ManualRoot returns the built-in command tree prepared for man pages. md2man drops a
// <placeholder> as inline HTML, so it is escaped outside indented code blocks.
func ManualRoot() *cobra.Command {
	root := NewRoot(Dependencies{})
	escape := func(text string) string {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "    ") {
				lines[i] = manualPlaceholder.ReplaceAllString(line, `\<$1`)
			}
		}
		return strings.Join(lines, "\n")
	}
	var prepare func(*cobra.Command)
	prepare = func(command *cobra.Command) {
		command.DisableAutoGenTag = true
		command.Use, command.Short, command.Long = escape(command.Use), escape(command.Short), escape(command.Long)
		for _, child := range command.Commands() {
			prepare(child)
		}
	}
	prepare(root)
	return root
}

// ManualCommands lists the subcommands that get a page in the manual: the available commands,
// without the generated help command, and the help topics such as environment and exit-codes.
func ManualCommands(page *cobra.Command) []*cobra.Command {
	var children []*cobra.Command
	for _, child := range page.Commands() {
		if child.Annotations[topicAnnotation] != "" || (child.IsAvailableCommand() && !child.IsAdditionalHelpTopicCommand()) {
			children = append(children, child)
		}
	}
	return children
}
