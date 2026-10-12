// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"strconv"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) artifactCommand() *cobra.Command {
	group := &cobra.Command{Use: "artifact", Short: "Inspect and download Actions artifacts"}
	var limit, page int
	list := &cobra.Command{Use: "list <run-id>", Short: "List artifacts belonging to a workflow run", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		run, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Actions{Client: target.client, Repository: target.Repo}).Artifacts(cmd.Context(), run, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, artifactRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	var output string
	download := &cobra.Command{Use: "download <artifact-id>", Short: "Download an artifact ZIP without extracting it", Annotations: textOutput("artifact downloads are binary"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		if err := validateDownloadDestination(output); err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		body, err := (services.Actions{Client: target.client, Repository: target.Repo}).DownloadArtifact(cmd.Context(), id)
		if err != nil {
			return err
		}
		defer body.Close()
		return writeDownload(cmd, body, output)
	}}
	download.Flags().StringVar(&output, "output-file", "", "Destination file (must not exist), or - for stdout")
	group.AddCommand(download)
	return group
}

func artifactRow(item services.Artifact) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Name, strconv.FormatInt(item.Size, 10), strconv.FormatBool(item.Expired))
}
