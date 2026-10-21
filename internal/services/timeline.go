// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	// asked for a review; RemovedAssignee tells that it unassigned them or withdrew the request.
	Assignee        *string `json:"assignee"`
	RemovedAssignee *bool   `json:"removed_assignee"`
	// DependentIssue is the issue a dependency event added or removed, as OWNER/REPO#NUMBER.
	DependentIssue *string `json:"dependent_issue"`
	// Deadline is the due date, as YYYY-MM-DD, that an "added_deadline" or "modified_deadline"
	// event set; OldDeadline is the one a "modified_deadline" or "removed_deadline" event replaced.
	Deadline    *string `json:"deadline"`
	OldDeadline *string `json:"old_deadline"`
}

// timelineComment is a timeline event as Gitea sends it.
// TODO(sdk): TimelineComment lacks assignee, removed_assignee and dependent_issue.
type timelineComment struct {
	gitea.TimelineComment
	Assignee        *gitea.User  `json:"assignee"`
	RemovedAssignee bool         `json:"removed_assignee"`
	DependentIssue  *gitea.Issue `json:"dependent_issue"`
}

// timelineBodies are the event types whose body is text someone wrote. Gitea keeps internal
// values in the body of others, such as "1" for an added label.
var timelineBodies = []string{"comment", "code", "review", "dismiss_review"}

func timelineEventFrom(full *timelineComment) TimelineEvent {
	event := &full.TimelineComment
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
	if full.Assignee != nil {
		assignee := login(full.Assignee)
		result.Assignee, result.RemovedAssignee = &assignee, &full.RemovedAssignee
	}
	if issue := full.DependentIssue; issue != nil && issue.Repository != nil {
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
	list := func(limit, page int) ([]*timelineComment, error) {
		var events []*timelineComment
		endpoint := fmt.Sprintf("repos/%s/%s/issues/%d/timeline?limit=%d&page=%d", url.PathEscape(owner), url.PathEscape(repo), n, limit, page)
		err := getJSON(ctx, s.Client, endpoint, &events)
		return events, err
	}
	events, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*timelineComment, http.Header, error) {
		events, err := list(limit, page)
		headers := http.Header{}
		// A full page may continue, so it asks for the event after it. A page shorter than
		// the limit is the last one unless the instance capped it to its largest page size.
		next := false
		switch size := len(events); {
		case err != nil || size == 0:
		case size == limit:
			var after []*timelineComment
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
	return convertPage(events, timelineEventFrom), s.issueError(ctx, err, n)
}
