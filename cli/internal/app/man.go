// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/spf13/pflag"
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
					if err := GenMan(page, header, writer); err != nil {
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

// GenMan writes the man page of command. A help topic takes no flags, so its page lists none,
// and the root page refers to the topics, which Cobra skips as hidden.
func GenMan(command *cobra.Command, header *doc.GenManHeader, w io.Writer) error {
	if command.HasParent() {
		return genTopicPage(command, w, ".SH OPTIONS\n", ".SH SEE ALSO\n", func(w io.Writer) error { return doc.GenMan(command, header, w) })
	}
	var page bytes.Buffer
	if err := doc.GenMan(command, header, &page); err != nil {
		return err
	}
	var topics strings.Builder
	for _, child := range ManualCommands(command) {
		if child.Annotations[topicAnnotation] != "" {
			fmt.Fprintf(&topics, `, \fB%s-%s(1)\fP`, command.Name(), child.Name())
		}
	}
	before, seeAlso, _ := strings.Cut(page.String(), ".SH SEE ALSO\n")
	references, after, _ := strings.Cut(seeAlso, "\n")
	_, err := io.WriteString(w, before+".SH SEE ALSO\n"+strings.TrimPrefix(references+topics.String(), ", ")+"\n"+after)
	return err
}

// GenMarkdown writes the reference page of command like GenMan.
func GenMarkdown(command *cobra.Command, w io.Writer) error {
	return genTopicPage(command, w, "### Options\n", "### SEE ALSO\n", func(w io.Writer) error {
		return doc.GenMarkdownCustom(command, w, func(name string) string { return name })
	})
}

// genTopicPage writes the page that gen renders, dropping a help topic's flags and option
// sections, which run from options up to next. The topic's indented tables become code blocks.
func genTopicPage(command *cobra.Command, w io.Writer, options, next string, gen func(io.Writer) error) error {
	if command.Annotations[topicAnnotation] == "" {
		return genCommandPage(command, w, gen)
	}
	long, flagsInUseLine := command.Long, command.DisableFlagsInUseLine
	defer func() { command.Long, command.DisableFlagsInUseLine = long, flagsInUseLine }()
	lines := strings.Split(long, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, " ") {
			lines[i] = "    " + line
		}
	}
	command.Long, command.DisableFlagsInUseLine = strings.Join(lines, "\n"), true
	var page bytes.Buffer
	if err := gen(&page); err != nil {
		return err
	}
	before, rest, _ := strings.Cut(page.String(), options)
	if _, after, found := strings.Cut(rest, next); found {
		before += next + after
	}
	_, err := io.WriteString(w, before)
	return err
}

// genCommandPage writes the page that gen renders with the flags and sections of the command's
// help page: without the global flags that do not apply, and with ARGUMENTS and JSON FIELDS.
func genCommandPage(command *cobra.Command, w io.Writer, gen func(io.Writer) error) error {
	if !command.HasParent() {
		return gen(w)
	}
	globals := command.Root().PersistentFlags()
	applies := sharedFlags(command)
	if runsExtension(command) {
		applies = map[string]bool{}
		globals.VisitAll(func(flag *pflag.Flag) { _, applies[flag.Name] = extensionFlags[flag.Name] })
	}
	for name, shown := range applies {
		if flag := globals.Lookup(name); flag != nil && !shown && !flag.Hidden {
			flag.Hidden = true
			defer func() { flag.Hidden = false }()
		}
	}
	var sections strings.Builder
	for _, extra := range extraSections(command) {
		fmt.Fprintf(&sections, "\n\n### %s\n\n%s", extra.title, indent(indent(extra.body)))
	}
	if sections.Len() > 0 {
		long := command.Long
		defer func() { command.Long = long }()
		command.Long = cmp.Or(long, command.Short) + sections.String()
	}
	return gen(w)
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
// <placeholder> as inline HTML and reads _WORD_ as emphasis, so both are escaped outside
// indented code blocks.
func ManualRoot() *cobra.Command {
	return docsRoot(true, func(line string) string {
		return strings.ReplaceAll(manualPlaceholder.ReplaceAllString(line, `\<$1`), "_", `\_`)
	})
}

// ReferenceRoot returns the built-in command tree prepared for the Markdown reference, which
// drops a <placeholder> outside code as inline HTML too. Its usage line is a code block.
func ReferenceRoot() *cobra.Command {
	return docsRoot(false, func(line string) string { return manualPlaceholder.ReplaceAllString(line, `\<$1`) })
}

// docsRoot returns the built-in command tree without auto-generated tags, with escapeLine applied
// to the prose lines of every short and long description, and of the usage line when use is set.
func docsRoot(use bool, escapeLine func(string) string) *cobra.Command {
	root := NewRoot(Dependencies{})
	escape := func(text, code string) string {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, code) {
				lines[i] = escapeLine(line)
			}
		}
		return strings.Join(lines, "\n")
	}
	var prepare func(*cobra.Command)
	prepare = func(command *cobra.Command) {
		command.DisableAutoGenTag = true
		code := "    "
		if command.Annotations[topicAnnotation] != "" {
			code = " " // genTopicPage turns every indented line of a topic into code
		}
		if use {
			command.Use = escape(command.Use, "    ")
		}
		command.Short, command.Long = escape(command.Short, "    "), escape(command.Long, code)
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
