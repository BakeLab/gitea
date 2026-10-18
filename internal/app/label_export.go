// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"io"
	"strings"

	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) labelExportCommand(scope labelScope) *cobra.Command {
	var output string
	command := &cobra.Command{Use: "export", Short: "Export labels to a label file", Long: `Write every label as a row of a label file, the format gt label import reads, to standard
output or to a new file.`, Example: `$ gt label export > labels.txt
$ gt label export --org acme --output labels.txt
# Copy the labels of one repository to another
$ gt label export -R alice/old | gt label import - -R alice/new`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if output != "" {
			if err := validateDownloadDestination(output); err != nil {
				return err
			}
		}
		labels, err := scope(cmd)
		if err != nil {
			return err
		}
		return writeOutput(cmd, output, func(destination io.Writer) error {
			items, err := labels.All(cmd.Context())
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return terminal.JSONOutput{Fields: a.options.json, Query: a.options.jq, Safe: output == "" && a.deps.Streams.OutTTY}.Write(destination, items)
			}
			var text strings.Builder
			for _, item := range items {
				row, err := formatLabel(item)
				if err != nil {
					return err
				}
				text.WriteString(row)
			}
			if output == "" || output == "-" {
				// Label names and descriptions are server text.
				destination = terminal.SafeWriter(destination, a.deps.Streams.OutTTY, false)
			}
			_, err = io.WriteString(destination, text.String())
			return err
		})
	}}
	command.Flags().StringVarP(&output, "output", "O", "", "Write to a new `file` instead of standard output")
	flagAliases(command, map[string]string{"output-file": "output"})
	return command
}
