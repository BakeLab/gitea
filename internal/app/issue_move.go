// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func (a *application) issueMoveCommand() *cobra.Command {
	var project string
	cmd := &cobra.Command{Use: "move {<number> | <url>} [to] <column>", Short: "Move an issue to a column of its project board", Long: `Move an issue to another column of the project board it is on. The column is matched by ID,
then by name, then ignoring case, spaces and punctuation, so that in-progress finds "In
progress". An issue on several projects needs --project to say which board to move it on.
Moving issues on organization projects needs a token that can write to the organization.`, Example: `$ gt issue move 47 to Todo
$ gt issue move 47 to "In progress"
$ gt issue move 47 in-progress --project Roadmap`, Annotations: map[string]string{"gt.arguments": issueArguments, "gt.text": "moving an issue does not return a resource"}, Args: moveArgs, RunE: func(cmd *cobra.Command, args []string) error {
		project = strings.TrimSpace(project)
		if cmd.Flags().Changed("project") && project == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--project cannot be empty"))
		}
		target, number, err := a.resolveRef(cmd, args[0], issueRef)
		if err != nil {
			return err
		}
		moved, err := (services.Projects{Client: target.client, Repository: target.Repo}).Move(cmd.Context(), number, strings.TrimSpace(args[len(args)-1]), project)
		if err != nil {
			return err
		}
		label := discussionNoun(moved.Issue.Kind) + " " + refLabel(target.Repo, number, moved.Issue.Title)
		if moved.Unchanged {
			a.warn(cmd, "%s is already in %q on project %q", titleCase(label), moved.Column, moved.Project)
			return nil
		}
		a.done(cmd, "Moved %s to %q on project %q", label, moved.Column, moved.Project)
		return nil
	}}
	cmd.Flags().StringVar(&project, "project", "", "Move the issue on the project with this ID or `title`, a number being an ID first")
	cmd.ValidArgsFunction = a.completeIssues("open")
	return cmd
}

// moveArgs takes an issue and a column, with an optional lowercase "to" between them. A column
// name with spaces is one quoted argument, so that the words of To Do are not read as "to" and "Do".
var moveArgs = nonBlankArgs(func(cmd *cobra.Command, args []string) error {
	switch {
	case len(args) == 3 && args[1] == "to":
		return nil
	case len(args) > 2:
		return fmt.Errorf("issue move takes one column; quote a column name with spaces, such as %q", strings.Join(args[1:], " "))
	}
	return cobra.ExactArgs(2)(cmd, args)
}, "issue", "column")
