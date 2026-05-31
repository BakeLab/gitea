// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"strings"

	"gitea.dev/sdk"
)

// Comments renders a list of comments to stdout
func Comments(comments []*gitea.Comment) {
	var baseURL string
	if len(comments) != 0 {
		baseURL = getRepoURL(comments[0].HTMLURL)
	}

	out := make([]string, len(comments))
	for i, c := range comments {
		out[i] = formatComment(c)
	}

	_ = outputMarkdown(fmt.Sprintf(
		// this will become a heading by means of the first --- from a comment
		"Comments\n%s",
		strings.Join(out, "\n"),
	), baseURL)
}

// CommentsList prints comments in tabular form, including IDs so they can be
// passed to 'tea comments edit' / 'tea comments delete'.
func CommentsList(comments []*gitea.Comment, output string) error {
	if len(comments) == 0 {
		fmt.Println("No comments found")
		return nil
	}

	t := tableWithHeader(
		"ID",
		"Author",
		"Created",
		"Updated",
		"Body",
	)

	for _, c := range comments {
		updated := ""
		if c.Updated.After(c.Created) {
			updated = FormatTime(c.Updated, false)
		}
		t.addRow(
			fmt.Sprintf("%d", c.ID),
			"@"+c.Poster.UserName,
			FormatTime(c.Created, false),
			updated,
			summarizeBody(c.Body),
		)
	}

	return t.print(output)
}

func summarizeBody(body string) string {
	const max = 80
	var b strings.Builder
	for _, r := range body {
		if r == '\n' || r == '\r' {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// Comment renders a comment to stdout
func Comment(c *gitea.Comment) {
	_ = outputMarkdown(formatComment(c), getRepoURL(c.HTMLURL))
}

func formatComment(c *gitea.Comment) string {
	edited := ""
	if c.Updated.After(c.Created) {
		edited = fmt.Sprintf(" *(edited on %s)*", FormatTime(c.Updated, false))
	}
	return fmt.Sprintf(
		"---\n\n**@%s** wrote on %s%s:\n\n%s\n",
		c.Poster.UserName,
		FormatTime(c.Created, false),
		edited,
		c.Body,
	)
}
