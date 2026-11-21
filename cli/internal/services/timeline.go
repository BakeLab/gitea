// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	gitea "gitea.dev/sdk"
)

// TimelineEvent is one entry of an issue's or pull request's timeline: a comment, or a change such
// as a label, milestone, title or state change, named by Type as Gitea does. Fields an event
// does not use are nil, so every key is always present in JSON.
type TimelineEvent struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Author    string `json:"author"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
	// Body is the text of comments and reviews.
	Body         *string `json:"body"`
	Label        *string `json:"label"`
	LabelAdded   *bool   `json:"label_added"`
	Milestone    *string `json:"milestone"`
	OldMilestone *string `json:"old_milestone"`
	OldTitle     *string `json:"old_title"`
	NewTitle     *string `json:"new_title"`
	// Assignee is the user an "assignees" event assigned, or the user a "review_request" event
	// asked for a review, and AssigneeTeam the team such an event asked; RemovedAssignee tells
	// that it unassigned them or withdrew the request.
	Assignee        *string `json:"assignee"`
	AssigneeTeam    *string `json:"assignee_team"`
	RemovedAssignee *bool   `json:"removed_assignee"`
	// DependentIssue is the issue a dependency event added or removed, as OWNER/REPO#NUMBER, or
	// nil for an issue the viewer cannot see.
	DependentIssue *string `json:"dependent_issue"`
	// Deadline is the due date, as YYYY-MM-DD, that an "added_deadline" or "modified_deadline"
	// event set; OldDeadline is the one a "modified_deadline" or "removed_deadline" event replaced.
	Deadline    *string `json:"deadline"`
	OldDeadline *string `json:"old_deadline"`
	// Duration is the time, as Gitea words it such as "30 minutes", that an "add_time_manual"
	// or "stop_tracking" event logged or a "delete_time_manual" event deleted.
	Duration *string `json:"duration"`
}

// timelineBodies are the event types whose body is text someone wrote. Gitea keeps internal
// values in the body of others, such as "1" for an added label.
var timelineBodies = []string{"comment", "code", "review", "dismiss_review"}

func timelineEventFrom(event *gitea.TimelineComment) TimelineEvent {
	result := TimelineEvent{ID: event.ID, Type: event.Type, Author: login(event.Poster), URL: event.HTMLURL, CreatedAt: timestamp(event.Created)}
	if slices.Contains(timelineBodies, event.Type) {
		result.Body = &event.Body
	}
	if event.Label != nil {
		result.Label = &event.Label.Name
		// Gitea records an added label with the body "1".
		added := event.Body == "1"
		result.LabelAdded = &added
	}
	if event.NewMilestone != nil {
		result.Milestone = &event.NewMilestone.Title
	}
	if event.OldMilestone != nil {
		result.OldMilestone = &event.OldMilestone.Title
	}
	if event.Type == "change_title" {
		result.OldTitle, result.NewTitle = &event.OldTitle, &event.NewTitle
	}
	if event.Assignee != nil {
		assignee := login(event.Assignee)
		result.Assignee, result.RemovedAssignee = &assignee, &event.RemovedAssignee
	}
	if event.AssigneeTeam != nil {
		result.AssigneeTeam, result.RemovedAssignee = &event.AssigneeTeam.Name, &event.RemovedAssignee
	}
	if issue := event.DependentIssue; issue != nil && issue.Repository != nil {
		dependent := issue.Repository.FullName + "#" + strconv.FormatInt(issue.Index, 10)
		result.DependentIssue = &dependent
	}
	// Gitea keeps the dates of deadline events in the body: "new", "new|old" or "old".
	switch event.Type {
	case "added_deadline":
		result.Deadline = &event.Body
	case "modified_deadline":
		deadline, old, _ := strings.Cut(event.Body, "|")
		result.Deadline, result.OldDeadline = &deadline, &old
	case "removed_deadline":
		result.OldDeadline = &event.Body
	case "add_time_manual", "stop_tracking", "delete_time_manual":
		result.Duration = &event.Body
	}
	return result
}

// Timeline returns a page of the timeline of issue or pull request n, oldest first.
// Gitea sends no Link header for the timeline and counts only the returned page in
// X-Total-Count, so a page that may continue asks for the one event after it.
func (s Issues) Timeline(ctx context.Context, n int64, limit, page int) (Page[TimelineEvent], error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Page[TimelineEvent]{}, err
	}
	list := func(limit, page int) ([]*gitea.TimelineComment, error) {
		events, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.TimelineComment, *gitea.Response, error) {
			return c.Issues.ListIssueTimeline(ctx, owner, repo, n, gitea.ListIssueCommentOptions{ListOptions: listOptions(limit, page)})
		})
		return events, err
	}
	events, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.TimelineComment, http.Header, error) {
		events, err := list(limit, page)
		headers := http.Header{}
		// A full page may continue, so it asks for the event after it. A page shorter than
		// the limit is the last one unless the instance capped it to its largest page size.
		next := false
		switch size := len(events); {
		case err != nil || size == 0:
		case size == limit:
			var after []*gitea.TimelineComment
			after, err = list(1, page*limit+1)
			next = len(after) > 0
		default:
			next = size == maxPageSize(ctx, s.Client)
		}
		if next {
			headers.Set("Link", `<timeline>; rel="next"`)
		}
		return events, headers, err
	})
	result := convertPage(events, timelineEventFrom)
	if err != nil {
		return result, s.issueError(ctx, err, n)
	}
	return result, s.hideInvisibleDependencies(ctx, result.Items)
}

// hideInvisibleDependencies clears the dependent issue of events naming an issue of another
// repository that the viewer cannot see: Gitea names issues of private repositories in the
// timeline of public ones.
func (s Issues) hideInvisibleDependencies(ctx context.Context, events []TimelineEvent) error {
	visible := map[string]bool{}
	for i, event := range events {
		ref := event.DependentIssue
		if ref == nil {
			continue
		}
		at := strings.LastIndex(*ref, "#")
		if at < 0 {
			continue
		}
		repository := (*ref)[:at]
		number, err := strconv.ParseInt((*ref)[at+1:], 10, 64)
		if err != nil || strings.EqualFold(repository, s.Repository) {
			continue
		}
		seen, known := visible[*ref]
		if !known {
			_, err := repoCall(ctx, s.Client, repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
				return c.Issues.GetIssue(ctx, owner, repo, number)
			})
			if err != nil && !IsNotFound(err) {
				return err
			}
			seen = err == nil
			visible[*ref] = seen
		}
		if !seen {
			events[i].DependentIssue = nil
		}
	}
	return nil
}
