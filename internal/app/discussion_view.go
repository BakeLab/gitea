// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

// discussionView is what issue view and pr view show: a terminal.View with the discussion's
// comments, which --comments adds before the footer.
type discussionView struct {
	view             terminal.View
	number, comments int64
	// summary describes the author and history after the state badge, relative to now, such as
	// "bob opened about 2 hours ago"; fields are the view's fields at that time.
	summary func(now time.Time) string
	fields  func(now time.Time) []terminal.Field
	// extraComments adds kind-specific entries, such as pull request reviews, to --comments.
	extraComments func(*cobra.Command, target, int64) ([]discussionComment, error)
}

// discussionComment is one --comments entry. heading words what a review did, such as "approved
// these changes"; plain comments have none, but an ID. meta is a key and value for piped output.
type discussionComment struct {
	author, heading, createdAt, body string
	id                               int64
	meta                             [2]string
}

func stateBadge(state string) terminal.Cell {
	return terminal.Cell{Text: titleCase(state), Color: stateColors[state]}
}

func labelCells(labels []services.Label) []terminal.Cell {
	cells := make([]terminal.Cell, len(labels))
	for i, label := range labels {
		cells[i] = terminal.Cell{Text: label.Name, Color: label.Color}
	}
	return cells
}

// whenText words an API time relative to now, or returns it unchanged when it is no time.
func whenText(now time.Time, raw string) string {
	if t := terminal.ParseTime(raw); !t.IsZero() {
		return terminal.RelativeTime(now, t)
	}
	return raw
}

func issueView(item services.Issue, repo string, dependencies services.IssueDependencies) discussionView {
	due := ""
	if item.Deadline != nil {
		due = *item.Deadline
	}
	return discussionView{
		number: item.Number, comments: item.Comments,
		summary: func(now time.Time) string { return item.Author + " opened " + whenText(now, item.CreatedAt) },
		fields: func(now time.Time) []terminal.Field {
			return []terminal.Field{
				{Label: "State", Value: item.State, PipedOnly: true},
				{Label: "Author", Value: item.Author, PipedOnly: true},
				{Label: "Labels", Cells: labelCells(item.Labels)},
				{Label: "Assignees", Value: strings.Join(item.Assignees, ", ")},
				{Label: "Milestone", Value: milestoneTitle(item.Milestone)},
				{Label: "Due", Value: dueText(now, due), TTYOnly: true},
				{Label: "Due", Value: due, PipedOnly: true},
				{Label: "Ref", Value: item.Ref},
				{Label: "Blocked by", Value: issueReferences(repo, dependencies.BlockedBy)},
				{Label: "Blocks", Value: issueReferences(repo, dependencies.Blocks)},
				{Label: "Comments", Value: strconv.FormatInt(item.Comments, 10), PipedOnly: true},
				{Label: "Number", Value: strconv.FormatInt(item.Number, 10), PipedOnly: true},
			}
		},
		view: terminal.View{
			Title: item.Title, ID: "#" + strconv.FormatInt(item.Number, 10), Badges: []terminal.Cell{stateBadge(item.State)},
			Body: item.Body, Kind: "issue", URL: item.URL,
		},
	}
}

// dueText words a due date for people: "Dec 24, 2026 (in about 2 months)". The day is the one in the
// offset Gitea returned: it stores a due date as the end of a day in its own time zone.
func dueText(now time.Time, raw string) string {
	due := terminal.ParseTime(raw)
	if due.IsZero() {
		return raw
	}
	return due.Format("Jan 2, 2006") + " (" + terminal.RelativeTime(now, due) + ")"
}

// issueReferences lists issues as #N, qualifying those from other repositories.
func issueReferences(repo string, refs []services.IssueReference) string {
	names := make([]string, len(refs))
	for i, ref := range refs {
		switch {
		case ref.Number < 1:
			names[i] = "hidden"
		case ref.Repository == "" || strings.EqualFold(ref.Repository, repo):
			names[i] = fmt.Sprintf("#%d", ref.Number)
		default:
			names[i] = fmt.Sprintf("%s#%d", ref.Repository, ref.Number)
		}
	}
	return strings.Join(names, ", ")
}

// pullDetails are what pr view reads besides the pull request: the reviews, and the checks of an
// open pull request's head.
type pullDetails struct {
	reviews []services.Review
	checks  []services.Check
}

func pullView(item services.Pull, repo string, details pullDetails) discussionView {
	state := pullState(item)
	head := pullHead(item, repo)
	summary := func(now time.Time) string {
		return fmt.Sprintf("%s wants to merge into %s from %s • %s", item.Author, item.Base, head, whenText(now, item.CreatedAt))
	}
	if item.Merged {
		summary = func(now time.Time) string {
			merged := cmp.Or(item.MergedBy, item.Author) + " merged into " + item.Base + " from " + head
			if item.MergedAt != nil {
				merged += " • " + whenText(now, *item.MergedAt)
			}
			return merged
		}
	}
	mergeable := ""
	switch {
	case state == "draft":
		mergeable = fmt.Sprintf("no — draft; mark it ready with: gt pr ready %d", item.Number)
	case state != "open":
	case item.Mergeable:
		mergeable = "yes"
	default:
		mergeable = "no — conflicts with " + item.Base
	}
	checks, checksHint := checkSummary(details.checks), ""
	if checks != "" {
		checksHint = fmt.Sprintf("%s (gt pr checks %d)", checks, item.Number)
	}
	return discussionView{
		number: item.Number, comments: item.Comments, summary: summary,
		view: terminal.View{
			Title: item.Title, ID: "#" + strconv.FormatInt(item.Number, 10), Badges: []terminal.Cell{stateBadge(state)},
			Body: item.Body, Kind: "pull request", URL: item.URL,
		},
		fields: func(time.Time) []terminal.Field {
			return []terminal.Field{
				{Label: "State", Value: state, PipedOnly: true},
				{Label: "Author", Value: item.Author, PipedOnly: true},
				{Label: "Base", Value: item.Base, PipedOnly: true},
				{Label: "Head", Value: head, PipedOnly: true},
				{Label: "Labels", Cells: labelCells(item.Labels)},
				{Label: "Assignees", Value: strings.Join(item.Assignees, ", ")},
				{Label: "Reviewers", Value: reviewerSummary(item, details.reviews)},
				{Label: "Milestone", Value: milestoneTitle(item.Milestone)},
				{Label: "Checks", Value: checksHint, TTYOnly: true},
				{Label: "Checks", Value: checks, PipedOnly: true},
				{Label: "Mergeable", Value: mergeable},
				{Label: "Comments", Value: strconv.FormatInt(item.Comments, 10), PipedOnly: true},
				{Label: "Number", Value: strconv.FormatInt(item.Number, 10), PipedOnly: true},
			}
		},
		extraComments: func(cmd *cobra.Command, t target, n int64) ([]discussionComment, error) {
			return pullReviewComments(cmd, t, n, details.reviews)
		},
	}
}

// reviewerSummary lists each reviewer with the state of their review in effect, then the requested
// reviewers and teams that have not reviewed yet: "bob (Approved), carol (Requested)".
func reviewerSummary(item services.Pull, reviews []services.Review) string {
	var names []string
	seen := map[string]bool{}
	for _, review := range services.EffectiveReviews(reviews) {
		seen[strings.ToLower(review.Author)] = true
		names = append(names, review.Author+" ("+reviewStateCell(review).Text+")")
	}
	for _, name := range slices.Concat(item.Reviewers, item.ReviewerTeams) {
		if !seen[strings.ToLower(name)] {
			names = append(names, name+" (Requested)")
		}
	}
	return strings.Join(names, ", ")
}

// checkSummary counts checks by outcome: "1 failing, 2 pending, 3 successful", or "" without checks.
func checkSummary(checks []services.Check) string {
	failing, pending, passed := services.Tally(checks)
	var parts []string
	for _, count := range []struct {
		n    int
		word string
	}{{failing, "failing"}, {pending, "pending"}, {passed, "successful"}} {
		if count.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count.n, count.word))
		}
	}
	return strings.Join(parts, ", ")
}

// reviewHeadings word what a review did in --comments, by its state.
var reviewHeadings = map[string]string{
	"APPROVED": "approved these changes", "REQUEST_CHANGES": "requested changes", "COMMENT": "reviewed",
}

// pullReviewComments returns the submitted reviews, including those without a body, and the inline
// review comments of pull request n.
func pullReviewComments(cmd *cobra.Command, t target, n int64, reviews []services.Review) ([]discussionComment, error) {
	var result []discussionComment
	for _, review := range reviews {
		heading, ok := reviewHeadings[review.State]
		if !ok {
			continue
		}
		if review.Dismissed {
			heading += " (dismissed)"
		}
		result = append(result, discussionComment{
			author: review.Author, createdAt: review.SubmittedAt, body: review.Body, heading: heading, meta: [2]string{"review", pipedReviewState(review)},
		})
	}
	inline, err := (services.Reviews{Client: t.client, Repository: t.Repo}).Inline(cmd.Context(), n, reviews)
	if err != nil {
		return nil, err
	}
	for _, comment := range inline {
		location := comment.Location()
		result = append(result, discussionComment{
			author: comment.Author, createdAt: comment.CreatedAt, body: comment.Body, id: comment.ID,
			heading: "commented on " + location, meta: [2]string{"path", location},
		})
	}
	return result, nil
}

func milestoneTitle(m *services.IssueMilestone) string {
	if m == nil {
		return ""
	}
	return m.Title
}

// writeDiscussion renders an issue or pull request like gh: a terminal.View, then the comments when
// withComments is set, or a hint at them, and the footer. Piped, the comments follow the view as
// key/value blocks.
func (a *application) writeDiscussion(cmd *cobra.Command, target target, v discussionView, withComments bool) error {
	var comments []discussionComment
	if withComments {
		items, err := (services.Comments{Client: target.client, Repository: target.Repo}).List(cmd.Context(), v.number)
		if err != nil {
			return err
		}
		for _, item := range items {
			comments = append(comments, discussionComment{author: item.Author, createdAt: item.CreatedAt, body: item.Body, id: item.ID})
		}
		if v.extraComments != nil {
			extra, err := v.extraComments(cmd, target, v.number)
			if err != nil {
				return err
			}
			comments = append(comments, extra...)
			slices.SortStableFunc(comments, func(x, y discussionComment) int {
				return terminal.ParseTime(x.createdAt).Compare(terminal.ParseTime(y.createdAt))
			})
		}
	}
	d := a.display()
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	view := v.view
	view.Fields = v.fields(now)
	if !d.TTY {
		var b strings.Builder
		b.WriteString(view.Render(d))
		for _, comment := range comments {
			fmt.Fprintf(&b, "--\nauthor:\t%s\ncreated_at:\t%s\n", terminal.SafeLine(comment.author), terminal.SafeLine(pipedTime(comment.createdAt)))
			if comment.id > 0 {
				fmt.Fprintf(&b, "id:\t%d\n", comment.id)
			}
			if comment.meta[0] != "" {
				fmt.Fprintf(&b, "%s:\t%s\n", comment.meta[0], terminal.SafeLine(comment.meta[1]))
			}
			fmt.Fprintf(&b, "--\n%s\n", terminal.SafeText(comment.body))
		}
		return a.writeHuman(cmd, b.String())
	}
	view.Meta = v.summary(now) + " • " + terminal.Plural(int(v.comments), "comment")
	view.URL = ""
	muted := func(text string) string { return terminal.Colorize("muted", text, d.Color) }
	var b strings.Builder
	b.WriteString(view.Render(d))
	if !withComments && v.comments > 0 {
		b.WriteString("\n" + muted(terminal.Plural(int(v.comments), "comment")+" — use --comments to view them") + "\n")
	}
	for _, comment := range comments {
		heading := []string{terminal.Emphasis(terminal.SafeLine(comment.author), d.Color)}
		if comment.heading != "" {
			heading[0] += " " + terminal.SafeLine(comment.heading)
		}
		heading = append(heading, whenText(now, comment.createdAt))
		if comment.id > 0 && comment.meta[0] == "" {
			heading = append(heading, "#"+strconv.FormatInt(comment.id, 10))
		}
		body := strings.TrimSpace(comment.body)
		if d.Markdown && body != "" {
			body = terminal.Markdown(body, d)
		} else {
			body = terminal.SafeText(body)
		}
		b.WriteString("\n" + strings.Join(heading, " • ") + "\n")
		if body != "" {
			b.WriteString(body + "\n")
		}
	}
	b.WriteString("\n" + muted("View this "+v.view.Kind+" on Gitea: "+terminal.SafeLine(v.view.URL)) + "\n")
	return a.writeHuman(cmd, b.String())
}
