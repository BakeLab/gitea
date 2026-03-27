// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"encoding/json"
	"io"
	"time"

	"code.gitea.io/sdk/gitea"
)

type detailLabelData struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type detailCommentData struct {
	ID      int64     `json:"id"`
	Author  string    `json:"author"`
	Created time.Time `json:"created"`
	Body    string    `json:"body"`
}

type detailReviewData struct {
	ID       int64                 `json:"id"`
	Reviewer string                `json:"reviewer"`
	State    gitea.ReviewStateType `json:"state"`
	Body     string                `json:"body"`
	Created  time.Time             `json:"created"`
}

func buildDetailLabels(labels []*gitea.Label) []detailLabelData {
	labelSlice := make([]detailLabelData, 0, len(labels))
	for _, label := range labels {
		labelSlice = append(labelSlice, detailLabelData{
			Name:        label.Name,
			Color:       label.Color,
			Description: label.Description,
		})
	}
	return labelSlice
}

func buildDetailAssignees(assignees []*gitea.User) []string {
	assigneeSlice := make([]string, 0, len(assignees))
	for _, assignee := range assignees {
		assigneeSlice = append(assigneeSlice, username(assignee))
	}
	return assigneeSlice
}

func buildDetailComments(comments []*gitea.Comment) []detailCommentData {
	commentSlice := make([]detailCommentData, 0, len(comments))
	for _, comment := range comments {
		commentSlice = append(commentSlice, detailCommentData{
			ID:      comment.ID,
			Author:  username(comment.Poster),
			Body:    comment.Body,
			Created: comment.Created,
		})
	}
	return commentSlice
}

func buildDetailReviews(reviews []*gitea.PullReview) []detailReviewData {
	reviewSlice := make([]detailReviewData, 0, len(reviews))
	for _, review := range reviews {
		reviewSlice = append(reviewSlice, detailReviewData{
			ID:       review.ID,
			Reviewer: username(review.Reviewer),
			State:    review.State,
			Body:     review.Body,
			Created:  review.Submitted,
		})
	}
	return reviewSlice
}

func username(user *gitea.User) string {
	if user == nil {
		return "ghost"
	}
	return user.UserName
}

func writeIndentedJSON(w io.Writer, data any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "\t")
	return encoder.Encode(data)
}
