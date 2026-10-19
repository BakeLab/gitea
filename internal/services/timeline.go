// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"

	gitea "gitea.dev/sdk"
)

// TimelineEvent is one entry of an issue's or pull request's timeline: a comment, or a change such
// as a label, milestone, title or state change, named by Type as Gitea does.
type TimelineEvent struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"`
	Author       string `json:"author"`
	Body         string `json:"body"`
	URL          string `json:"url"`
	CreatedAt    string `json:"created_at"`
	Label        string `json:"label,omitempty"`
	Milestone    string `json:"milestone,omitempty"`
	OldMilestone string `json:"old_milestone,omitempty"`
	OldTitle     string `json:"old_title,omitempty"`
	NewTitle     string `json:"new_title,omitempty"`
}

func timelineEventFrom(event *gitea.TimelineComment) TimelineEvent {
	result := TimelineEvent{
		ID: event.ID, Type: event.Type, Author: login(event.Poster), Body: event.Body, URL: event.HTMLURL,
		CreatedAt: timestamp(event.Created), OldTitle: event.OldTitle, NewTitle: event.NewTitle,
	}
	if event.Label != nil {
		result.Label = event.Label.Name
	}
	if event.NewMilestone != nil {
		result.Milestone = event.NewMilestone.Title
	}
	if event.OldMilestone != nil {
		result.OldMilestone = event.OldMilestone.Title
	}
	return result
}

// Timeline returns a page of the timeline of issue or pull request n, oldest first.
func (s Issues) Timeline(ctx context.Context, n int64, limit, page int) (Page[TimelineEvent], error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Page[TimelineEvent]{}, err
	}
	events, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.TimelineComment, *gitea.Response, error) {
		return c.Issues.ListIssueTimeline(ctx, owner, repo, n, gitea.ListIssueCommentOptions{ListOptions: list})
	})
	return convertPage(events, timelineEventFrom), err
}
