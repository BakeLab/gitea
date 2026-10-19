// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strings"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) timelineCommand(kind refKind) *cobra.Command {
	words := commentWords(kind)
	var limit, page int
	cmd := &cobra.Command{
		Use: "timeline {<number> | <url>}", Short: "Show the activity on " + kind.article(),
		Long: fmt.Sprintf(`Show what happened on %s, oldest first: comments, and changes such as labels,
milestones, titles, assignees, references and state changes.`, kind.article()),
		Example: fmt.Sprintf(`$ gt %[1]s timeline %[2]s
$ gt %[1]s timeline %[2]s --json type,author,created_at`, words.group, words.example),
		Annotations: map[string]string{"gt.arguments": words.arguments}, Args: argsExact(1), ValidArgsFunction: firstArg(a.completeDiscussions(kind)),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, n, err := a.resolveRef(cmd, args[0], kind)
			if err != nil {
				return err
			}
			if kind == pullRef {
				if err := requirePull(cmd, t, n); err != nil {
					return err
				}
			}
			result, err := (services.Issues{Client: t.client, Repository: t.Repo}).Timeline(cmd.Context(), n, limit, page)
			if err != nil {
				return err
			}
			ref := fmt.Sprintf("%s#%d", t.Repo, n)
			return writeTable(a, cmd, result.Items, listSpec[services.TimelineEvent]{
				Columns: []terminal.Column{{Header: "When"}, {Header: "Author"}, {Header: "Event", Flex: true}},
				Row: func(event services.TimelineEvent) []terminal.Cell {
					return []terminal.Cell{{Time: terminal.ParseTime(event.CreatedAt)}, {Text: event.Author, Bold: true}, {Text: timelineText(event), Link: event.URL}}
				},
				Empty: "no activity on " + ref, Context: "activity on " + ref,
			}, servicePage(result, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return jsonOutput(cmd, jsonFields[services.TimelineEvent]())
}

// timelineText words a timeline event, such as `added label "bug"` or a comment's first line.
func timelineText(event services.TimelineEvent) string {
	switch event.Type {
	case "comment":
		first, _, _ := strings.Cut(strings.TrimSpace(event.Body), "\n")
		return "commented: " + first
	case "label":
		// Gitea records an added label with the body "1".
		if event.Body == "1" {
			return fmt.Sprintf("added label %q", event.Label)
		}
		return fmt.Sprintf("removed label %q", event.Label)
	case "milestone":
		if event.Milestone == "" {
			return fmt.Sprintf("removed milestone %q", event.OldMilestone)
		}
		return fmt.Sprintf("set milestone %q", event.Milestone)
	case "change_title":
		return fmt.Sprintf("renamed %q to %q", event.OldTitle, event.NewTitle)
	case "close":
		return "closed"
	case "reopen":
		return "reopened"
	case "merge_pull":
		return "merged"
	case "review":
		return "reviewed"
	case "assignees":
		return "changed the assignees"
	case "commit_ref", "comment_ref", "issue_ref", "pull_ref":
		return "referenced this from elsewhere"
	case "pull_push":
		return "pushed commits"
	case "delete_branch":
		return "deleted the head branch"
	}
	return strings.ReplaceAll(event.Type, "_", " ")
}
