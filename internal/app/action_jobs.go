// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"strconv"

	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) runJobsCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{Use: "jobs <run-id>", Short: "List jobs belonging to a workflow run", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		run, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Actions{Client: target.client, Repository: target.Repo}).JobsPage(cmd.Context(), run, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, jobRow)
	}}
	pageFlags(cmd, &limit, &page)
	return cmd
}

func jobRow(item services.Job) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Name, item.Status, item.Conclusion)
}
