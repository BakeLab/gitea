// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// PullReviewCommentFields are all available fields to print with PullReviewCommentsList()
var PullReviewCommentFields = []string{
	"id",
	"body",
	"reviewer",
	"path",
	"line",
	"resolver",
	"created",
	"updated",
	"url",
}

// PullReviewCommentsList prints a listing of pull review comments
func PullReviewCommentsList(comments []*gitea.PullReviewComment, output string, fields []string) error {
	printables := make([]printable, len(comments))
	for i, c := range comments {
		printables[i] = &printablePullReviewComment{c}
	}
	t := tableFromItems(fields, printables, isMachineReadable(output))
	return t.print(output)
}

type printablePullReviewComment struct {
	*gitea.PullReviewComment
}

func (x printablePullReviewComment) FormatField(field string, machineReadable bool) string {
	switch field {
	case "id":
		return fmt.Sprintf("%d", x.ID)
	case "body":
		return x.Body
	case "reviewer":
		if x.Reviewer != nil {
			return formatUserName(x.Reviewer)
		}
		return ""
	case "path":
		return x.Path
	case "line":
		if x.LineNum != 0 {
			return fmt.Sprintf("%d", x.LineNum)
		}
		if x.OldLineNum != 0 {
			return fmt.Sprintf("%d", x.OldLineNum)
		}
		return ""
	case "resolver":
		if x.Resolver != nil {
			return formatUserName(x.Resolver)
		}
		return ""
	case "created":
		return FormatTime(x.Created, machineReadable)
	case "updated":
		return FormatTime(x.Updated, machineReadable)
	case "url":
		return x.HTMLURL
	}
	return ""
}
