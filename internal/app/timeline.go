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
	// pr timeline takes the pull request like other single pull request commands.
	operand, arguments, operandArgs := "{<number> | <url>}", words.arguments, argsExact(1)
	if kind == pullRef {
		operand, arguments, operandArgs = "[<number> | <url> | <branch>]", pullArguments, usageArgs(cobra.MaximumNArgs(1))
	}
	cmd := &cobra.Command{
		Use: "timeline " + operand, Short: "Show the activity on " + kind.article(),
		Long: fmt.Sprintf(`Show what happened on %s, oldest first: comments, and changes such as labels,
milestones, titles, assignees, references and state changes.`, kind.article()),
		Example: fmt.Sprintf(`$ gt %[1]s timeline %[2]s
$ gt %[1]s timeline %[2]s --json type,author,created_at`, words.group, words.example),
		Annotations: map[string]string{"gt.arguments": arguments}, Args: operandArgs, ValidArgsFunction: firstArg(a.completeDiscussions(kind)),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, n, err := a.discussionRef(cmd, args, kind)
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
					return []terminal.Cell{{Time: terminal.ParseTime(event.CreatedAt)}, {Text: event.Author, Bold: true}, {Text: timelineText(event, t.Repo), Link: event.URL}}
				},
				Empty: "no activity on " + ref, Context: "events on " + ref,
			}, servicePage(result, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return jsonOutput(cmd, jsonFields[services.TimelineEvent]())
}

// timelineText words a timeline event in repository repo, such as `added label "bug"` or a
// comment's first line.
func timelineText(event services.TimelineEvent, repo string) string {
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	switch event.Type {
	case "comment":
		first, _, _ := strings.Cut(strings.TrimSpace(text(event.Body)), "\n")
		return "commented: " + first
	case "label":
		if event.LabelAdded != nil && *event.LabelAdded {
			return fmt.Sprintf("added label %q", text(event.Label))
		}
		return fmt.Sprintf("removed label %q", text(event.Label))
	case "milestone":
		switch {
		case event.Milestone == nil && event.OldMilestone == nil:
			return "changed the milestone" // Gitea forgets a deleted milestone
		case event.Milestone == nil:
			return fmt.Sprintf("removed milestone %q", *event.OldMilestone)
		}
		return fmt.Sprintf("set milestone %q", *event.Milestone)
	case "change_title":
		return fmt.Sprintf("renamed %q to %q", text(event.OldTitle), text(event.NewTitle))
	case "close":
		return "closed"
	case "reopen":
		return "reopened"
	case "merge_pull":
		return "merged"
	case "review":
		return "reviewed"
	case "assignees":
		switch {
		case event.Assignee == nil:
			return "changed the assignees"
		case event.RemovedAssignee != nil && *event.RemovedAssignee:
			return "unassigned " + *event.Assignee
		}
		return "assigned " + *event.Assignee
	case "review_request":
		reviewer := text(event.Assignee)
		if event.AssigneeTeam != nil {
			reviewer = "team " + *event.AssigneeTeam
		}
		switch {
		case reviewer == "":
			return "changed the review requests"
		case event.RemovedAssignee != nil && *event.RemovedAssignee:
			return "removed the review request for " + reviewer
		}
		return "requested a review from " + reviewer
	case "added_deadline":
		return "set the due date to " + text(event.Deadline)
	case "modified_deadline":
		return fmt.Sprintf("changed the due date from %s to %s", text(event.OldDeadline), text(event.Deadline))
	case "removed_deadline":
		return "removed the due date " + text(event.OldDeadline)
	case "add_dependency", "remove_dependency":
		verb := "added"
		if event.Type == "remove_dependency" {
			verb = "removed"
		}
		if event.DependentIssue == nil {
			return verb + " a dependency"
		}
		// An issue of the same repository is named by its number alone.
		name := *event.DependentIssue
		if number, ok := strings.CutPrefix(name, repo+"#"); ok {
			name = "#" + number
		}
		return verb + " dependency " + name
	case "commit_ref", "comment_ref", "issue_ref", "pull_ref":
		return "referenced this from elsewhere"
	case "pull_push":
		return "pushed commits"
	case "delete_branch":
		return "deleted the head branch"
	}
	return strings.ReplaceAll(event.Type, "_", " ")
}
