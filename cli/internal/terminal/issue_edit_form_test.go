// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEditIssueEditBodyPreservesDraftOnFailure(t *testing.T) {
	p, _ := testPrompter(bytes.NewBufferString("New Title\r\r"))
	path := filepath.Join(t.TempDir(), "draft.md")
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{
		Title: "", Body: "current",
		EditBody: func(context.Context, string) (Draft, error) {
			return Draft{Path: path, Body: "partial"}, errors.New("editor failed")
		},
	})
	require.ErrorContains(t, err, "editor failed")
	require.Equal(t, path, values.Draft.Path)
	require.Equal(t, "New Title", values.Title)
}

func TestEditIssueKeepsUnchangedDeadline(t *testing.T) {
	deadline := time.Date(2026, 10, 6, 23, 59, 59, 0, time.FixedZone("server", -5*3600))
	p, _ := testPrompter(bytes.NewBufferString("\r\r\r"))
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", Deadline: &deadline})
	require.NoError(t, err)
	require.Nil(t, values.Deadline)
	require.False(t, values.ClearDeadline)
}

func TestAccessibleEditIssueKeepsTitleWithControlCharactersOnEnter(t *testing.T) {
	p, _ := linePrompter(t, "\n\n")
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "  a\tb  ", EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "  a\tb  ", values.Title, "Enter keeps the title byte for byte")
}

func TestEditIssueWithoutDeadline(t *testing.T) {
	// Without a deadline the due date shows its placeholder, here on a terminal of unknown width.
	p, _ := testPrompter(bytes.NewBufferString("\r\r"))
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", EditBody: noEdit})
	require.NoError(t, err)
	require.Nil(t, values.Deadline)
	require.False(t, values.ClearDeadline)
}

func TestEditIssueInlineBodyKeepsTabsAndCRLFOfUnchangedLines(t *testing.T) {
	body := "intro\r\n```make\r\nall:\r\n\techo hi\r\n```\r\n"
	// Keys typed right after Enter would reach the title before the form focuses the body.
	p, _, ctx := screenPrompter(t, keyStep{"enter next", "\r"}, keyStep{"new line", "top\n\r"}, keyStep{"Due date", "\r"})
	values, err := p.EditIssue(ctx, IssueEditFormChoices{Title: "Title", Body: body})
	require.NoError(t, err)
	require.Equal(t, "top\r\n"+body, values.Body)

	p, _ = testPrompter(bytes.NewBufferString("\r\r\r"))
	values, err = p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", Body: body})
	require.NoError(t, err)
	require.Equal(t, body, values.Body)
}

func TestWithCurrentKeepsSeededValuesSelectable(t *testing.T) {
	options := []IssueLabelChoice{{Name: "bug", ID: 2}}
	merged := withCurrent(options, IssueLabelChoice{Name: "bug", ID: 2}, IssueLabelChoice{Name: "org", ID: 7})
	require.Equal(t, []IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "org", ID: 7}}, merged)
	require.Len(t, options, 1)
}

func TestEditIssueOffersMetadataWithoutPush(t *testing.T) {
	// People who can write to the issues set metadata without push access, which the form cannot
	// tell, so it always offers it.
	p, rendered := accessiblePrompter(t, "\n2027-03-04\n")
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Due date:")
	require.NotNil(t, values.Deadline)
	require.Equal(t, "2027-03-04", values.Deadline.Format(time.DateOnly))
	require.NotContains(t, rendered.String(), "Base branch", "issues have no base branch")
}

func TestEditIssueAsksForTheBaseOfAPullRequest(t *testing.T) {
	p, rendered := accessiblePrompter(t, "\n stable \n\n")
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", Base: "main", EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Base branch")
	require.Equal(t, "stable", values.Base)

	p, _ = accessiblePrompter(t, "\n\n\n")
	values, err = p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", Base: "main", EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "main", values.Base, "an empty answer keeps the base")
}
