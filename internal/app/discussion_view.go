// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

// discussionView holds the fields `issue view` and `pr view` render.
type discussionView struct {
	kind, title, state, author, action, createdAt, url, body string
	number, comments                                         int64
	labels                                                   []services.Label
	fields                                                   [][2]string
	// extraComments adds kind-specific entries, such as pull request reviews, to --comments.
	extraComments func(*cobra.Command, target, int64) ([]discussionComment, error)
}

// discussionComment is one --comments entry; heading and meta are empty for plain comments.
type discussionComment struct {
	author, createdAt, heading, body string
	meta                             [2]string
}

func issueView(item services.Issue, repo string, dependencies services.IssueDependencies) discussionView {
	return discussionView{
		kind: "issue", title: item.Title, state: item.State, author: item.Author, action: "opened",
		createdAt: item.CreatedAt, url: item.URL, body: item.Body, number: item.Number, comments: item.Comments, labels: item.Labels,
		fields: [][2]string{
			{"Assignees", strings.Join(item.Assignees, ", ")},
			{"Milestone", milestoneTitle(item.Milestone)},
			{"Blocked by", issueReferences(repo, dependencies.BlockedBy)},
			{"Blocks", issueReferences(repo, dependencies.Blocks)},
		},
	}
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

func pullView(item services.Pull) discussionView {
	state := pullState(item)
	v := discussionView{
		kind: "pull request", title: item.Title, state: state, author: item.Author,
		action:    fmt.Sprintf("wants to merge into %s from %s •", item.Base, item.Head),
		createdAt: item.CreatedAt, url: item.URL, body: item.Body, number: item.Number, comments: item.Comments, labels: item.Labels,
		fields: [][2]string{
			{"Reviewers", strings.Join(slices.Concat(item.Reviewers, item.ReviewerTeams), ", ")},
			{"Assignees", strings.Join(item.Assignees, ", ")},
			{"Milestone", milestoneTitle(item.Milestone)},
		},
		extraComments: pullReviewComments,
	}
	if state == "open" {
		mergeable := "no"
		if item.Mergeable {
			mergeable = "yes"
		}
		v.fields = append(v.fields, [2]string{"Mergeable", mergeable})
	}
	return v
}

// pullReviewComments returns submitted review bodies and inline review comments.
func pullReviewComments(cmd *cobra.Command, target target, n int64) ([]discussionComment, error) {
	reviews, inline, err := (services.Reviews{Client: target.client, Repository: target.Repo}).Thread(cmd.Context(), n)
	if err != nil {
		return nil, err
	}
	var result []discussionComment
	for _, review := range reviews {
		if strings.TrimSpace(review.Body) == "" || review.State == "PENDING" {
			continue
		}
		result = append(result, discussionComment{
			author: review.Author, createdAt: review.SubmittedAt, body: review.Body,
			heading: "reviewed (" + review.State + ")", meta: [2]string{"review", review.State},
		})
	}
	for _, comment := range inline {
		location := comment.Path
		if comment.Line > 0 {
			location = fmt.Sprintf("%s:%d", comment.Path, comment.Line)
		}
		result = append(result, discussionComment{
			author: comment.Author, createdAt: comment.CreatedAt, body: comment.Body,
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

// writeDiscussion renders a gh-style detail view: a readable summary on a
// terminal and tab-separated key/value lines followed by the body otherwise.
func (a *application) writeDiscussion(cmd *cobra.Command, target target, v discussionView, withComments bool) error {
	var comments []discussionComment
	if withComments {
		items, err := (services.Comments{Client: target.client, Repository: target.Repo}).List(cmd.Context(), v.number)
		if err != nil {
			return err
		}
		for _, item := range items {
			comments = append(comments, discussionComment{author: item.Author, createdAt: item.CreatedAt, body: item.Body})
		}
		if v.extraComments != nil {
			extra, err := v.extraComments(cmd, target, v.number)
			if err != nil {
				return err
			}
			comments = append(comments, extra...)
			slices.SortStableFunc(comments, func(x, y discussionComment) int { return compareTimes(x.createdAt, y.createdAt) })
		}
	}
	v.title, v.body = terminal.SafeLine(v.title), terminal.SafeText(v.body)
	for i := range v.fields {
		v.fields[i][1] = terminal.SafeLine(v.fields[i][1])
	}
	for i := range comments {
		c := &comments[i]
		c.heading, c.meta[1], c.body = terminal.SafeLine(c.heading), terminal.SafeLine(c.meta[1]), terminal.SafeText(c.body)
	}
	color := a.deps.Streams.OutTTY && a.colorEnabled()
	labels := make([]string, len(v.labels))
	for i, label := range v.labels {
		labels[i] = terminal.Colorize(label.Color, terminal.SafeLine(label.Name), color)
	}
	var b strings.Builder
	if !a.deps.Streams.OutTTY {
		fmt.Fprintf(&b, "title:\t%s\nstate:\t%s\nauthor:\t%s\nlabels:\t%s\n", v.title, v.state, v.author, strings.Join(labels, ", "))
		for _, field := range v.fields {
			fmt.Fprintf(&b, "%s:\t%s\n", strings.ToLower(field[0]), field[1])
		}
		fmt.Fprintf(&b, "comments:\t%d\nnumber:\t%d\nurl:\t%s\n--\n%s\n", v.comments, v.number, v.url, v.body)
		for _, comment := range comments {
			fmt.Fprintf(&b, "--\nauthor:\t%s\ncreated_at:\t%s\n", comment.author, comment.createdAt)
			if comment.meta[0] != "" {
				fmt.Fprintf(&b, "%s:\t%s\n", comment.meta[0], comment.meta[1])
			}
			fmt.Fprintf(&b, "--\n%s\n", comment.body)
		}
		_, err := fmt.Fprint(cmd.OutOrStdout(), b.String())
		return err
	}
	now := a.deps.Now()
	fmt.Fprintf(&b, "%s #%d\n", terminal.Emphasis(v.title, color), v.number)
	fmt.Fprintf(&b, "%s • %s %s %s • %s\n", terminal.Colorize(stateColors[v.state], titleCase(v.state), color), v.author, v.action, relativeTime(now, v.createdAt), plural(int(v.comments), "comment"))
	for _, field := range append([][2]string{{"Labels", strings.Join(labels, ", ")}}, v.fields...) {
		if field[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", terminal.Emphasis(field[0], color), field[1])
		}
	}
	body := strings.TrimSpace(v.body)
	if body == "" {
		body = "No description provided"
	}
	fmt.Fprintf(&b, "\n%s\n\n", body)
	for _, comment := range comments {
		heading := ""
		if comment.heading != "" {
			heading = comment.heading + " • "
		}
		fmt.Fprintf(&b, "%s • %s%s\n%s\n\n", terminal.Emphasis(comment.author, color), heading, relativeTime(now, comment.createdAt), strings.TrimSpace(comment.body))
	}
	fmt.Fprintf(&b, "View this %s on Gitea: %s\n", v.kind, v.url)
	return a.writeHuman(cmd, b.String())
}

// compareTimes orders API timestamps chronologically, falling back to text order.
func compareTimes(x, y string) int {
	tx, errX := time.Parse(time.RFC3339, x)
	ty, errY := time.Parse(time.RFC3339, y)
	if errX != nil || errY != nil {
		return strings.Compare(x, y)
	}
	return tx.Compare(ty)
}

// relativeTime formats an API timestamp like gh: "about 3 days ago".
func relativeTime(now time.Time, raw string) string {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	const day = 24 * time.Hour
	var n time.Duration
	var unit string
	switch d := now.Sub(t); {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		n, unit = d/time.Minute, "minute"
	case d < day:
		n, unit = d/time.Hour, "hour"
	case d < 30*day:
		n, unit = d/day, "day"
	case d < 365*day:
		n, unit = d/(30*day), "month"
	default:
		n, unit = d/(365*day), "year"
	}
	return "about " + plural(int(n), unit) + " ago"
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
