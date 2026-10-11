// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"io"
	"strings"

	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) labelExportCommand(service func(*cobra.Command) (services.Labels, error)) *cobra.Command {
	var output string
	command := &cobra.Command{Use: "export", Short: "Export all labels in the selected scope", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateDownloadDestination(output); err != nil {
			return err
		}
		labels, err := service(cmd)
		if err != nil {
			return err
		}
		return writeOutput(cmd, output, func(destination io.Writer) error {
			items, err := labels.All(cmd.Context())
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return terminal.WriteJSON(destination, items, a.options.json, a.options.jq)
			}
			var text strings.Builder
			for _, item := range items {
				row, err := formatLabel(item)
				if err != nil {
					return err
				}
				text.WriteString(row)
			}
			_, err = io.WriteString(destination, text.String())
			return err
		})
	}}
	command.Flags().StringVar(&output, "output-file", "", "New export file, or - for stdout")
	return command
}
