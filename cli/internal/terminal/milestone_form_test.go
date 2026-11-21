// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMilestoneFormMarksTitleRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "v1\n\n\n")
	_, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Milestone title (required):")
}

func TestMilestoneFormStartsFromCurrentValues(t *testing.T) {
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	p, _ := testPrompter(strings.NewReader("\r\r\r"))
	values, err := p.EditMilestone(context.Background(), "Editing milestone v1.0", MilestoneFormValues{Title: "v1.0", Description: "First", Deadline: &due})
	require.NoError(t, err)
	require.Equal(t, "v1.0", values.Title)
	require.Equal(t, "First", values.Description)
	require.Equal(t, due, *values.Deadline)
}

func TestMilestoneFormKeepsSeededDescriptionOnEnter(t *testing.T) {
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	seed := "a\r\nb\tc"
	p, _ := testPrompter(strings.NewReader("\r\r\r"))
	values, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: "v1.0", Description: seed, Deadline: &due})
	require.NoError(t, err)
	require.Equal(t, seed, values.Description)
}

func TestEditFormsKeepSeededTitleOnEnter(t *testing.T) {
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	seed := " v1\t\x1b[1mbold\r\nnext\xff "
	p, _ := testPrompter(strings.NewReader("\r\r\r"))
	milestone, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: seed, Deadline: &due})
	require.NoError(t, err)
	require.Equal(t, seed, milestone.Title)
	p, _ = testPrompter(strings.NewReader("\r\r\r"))
	issue, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: seed, Deadline: &due, EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, seed, issue.Title)
	// An edited title is trimmed.
	p, _ = testPrompter(strings.NewReader("x \r\r\r"))
	milestone, err = p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: " v1", Deadline: &due})
	require.NoError(t, err)
	require.Equal(t, "v1x", milestone.Title)
}

func TestAccessibleFormsKeepSeededRequiredFieldsOnEnter(t *testing.T) {
	p, rendered := linePrompter(t, "\nNotes\n.\n\n")
	milestone, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: "v1.0"})
	require.NoError(t, err)
	require.Equal(t, MilestoneFormValues{Title: "v1.0", Description: "Notes"}, milestone)
	require.NotContains(t, rendered.String(), "input cannot be empty")

	p, rendered = linePrompter(t, "\n\n")
	issue, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Crash on start", EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "Crash on start", issue.Title)
	require.NotContains(t, rendered.String(), "input cannot be empty")

	p, rendered = linePrompter(t, "\nn\n\nfix-typo\n\n\nFix Typo\n\n1\n")
	pull, err := p.CreatePull(context.Background(), PullFormChoices{Repo: "alice/x", BaseOwner: "alice", Base: "main"})
	require.NoError(t, err)
	require.Equal(t, "main", pull.Base)
	require.Equal(t, "fix-typo", pull.Head)
	require.NotContains(t, rendered.String(), "input cannot be empty")

	// A blank seed still needs an answer.
	p, rendered = linePrompter(t, "\nv2\n\n\n")
	milestone, err = p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: " \t"})
	require.NoError(t, err)
	require.Equal(t, "v2", milestone.Title)
	require.Contains(t, rendered.String(), "input cannot be empty")
}

func TestAccessibleOneLineFieldsShowTheirValueAndClearOptionalOnes(t *testing.T) {
	due := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	p, rendered := linePrompter(t, "\n-\n")
	issue, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Crash\x1b[31m on start", Deadline: &due, EditBody: noEdit})
	require.NoError(t, err)
	require.True(t, issue.ClearDeadline, "- clears the due date")
	require.Nil(t, issue.Deadline)
	require.Contains(t, rendered.String(), "Title (required) [Crash[31m on start]:")
	require.Contains(t, rendered.String(), `Due date [2027-01-15; "-" clears]:`)

	// An empty answer, or one of spaces, keeps the value.
	p, _ = linePrompter(t, "\n   \n")
	issue, err = p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Crash", Deadline: &due, EditBody: noEdit})
	require.NoError(t, err)
	require.False(t, issue.ClearDeadline)

	p, rendered = linePrompter(t, "\n\n-\n")
	milestone, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: "v1.0", Deadline: &due})
	require.NoError(t, err)
	require.Nil(t, milestone.Deadline)
	require.Contains(t, rendered.String(), "Milestone title (required) [v1.0]:")
	require.Contains(t, rendered.String(), `Milestone deadline [2027-01-15; "-" clears]:`)

	// A required field takes "-" as its value, and a field without a value shows no brackets.
	p, rendered = linePrompter(t, "-\n\n\n")
	milestone, err = p.EditMilestone(context.Background(), "", MilestoneFormValues{Title: "v1.0"})
	require.NoError(t, err)
	require.Equal(t, "-", milestone.Title)
	require.Contains(t, rendered.String(), "Milestone deadline:")

	// Enter accepts a prefilled instance, which the validation checks rather than the empty answer.
	p, rendered = linePrompter(t, "\n")
	login := LoginAnswers{Host: "gitea.com"}
	require.NoError(t, p.Login(context.Background(), LoginQuestions{Host: true, ResolveHost: func(s string) (string, error) {
		if s == "" {
			return "", errors.New("enter an instance")
		}
		return "https://" + s, nil
	}}, &login))
	require.Equal(t, "https://gitea.com", login.Host)
	require.Contains(t, rendered.String(), "Gitea instance [gitea.com]")
}

func TestAskTextKeepsSeedOnEnter(t *testing.T) {
	seed := "a\r\nb\tc\nd"
	p, _ := testPrompter(strings.NewReader("\r\r"))
	text, err := p.AskText(context.Background(), "Body", seed)
	require.NoError(t, err)
	require.Equal(t, seed, text)
}

func TestAccessibleAskTextShowsTheTextAndReadsSeveralLines(t *testing.T) {
	seed := "line1\r\n\tindented\r\n\r\ntrailing  \r\n\r\n"
	p, rendered := linePrompter(t, "\n")
	text, err := p.AskText(context.Background(), "Body (markdown):", seed)
	require.NoError(t, err)
	require.Equal(t, seed, text, "Enter keeps the text")
	require.Contains(t, rendered.String(), "Body (markdown):\nCurrent text:\nline1\n    indented\n\ntrailing  \n")
	require.Contains(t, rendered.String(), `end it with a line holding only "."; an empty first line keeps the current text.`)

	p, _ = linePrompter(t, "first line\nsecond line\n.\nnext\n")
	text, err = p.AskText(context.Background(), "Body (markdown):", seed)
	require.NoError(t, err)
	require.Equal(t, "first line\r\nsecond line", text)
	next, err := p.Ask(context.Background(), "Next:", nil)
	require.NoError(t, err)
	require.Equal(t, "next", next, "the text takes no line past its end")

	p, rendered = linePrompter(t, ".\n")
	text, err = p.AskText(context.Background(), "Body (markdown):", "")
	require.NoError(t, err)
	require.Empty(t, text)
	require.Contains(t, rendered.String(), "an empty first line leaves it empty.")

	p, rendered = linePrompter(t, "1\n.\nfixed\n.\n")
	values, err := p.CreateReview(context.Background(), ReviewFormChoices{Author: true})
	require.NoError(t, err)
	require.Equal(t, "fixed", values.Body, "a required text cannot be emptied")
	require.Contains(t, rendered.String(), "input cannot be empty")
}

func TestAskTextKeepsCRLFLineBreaksSingleWhenEdited(t *testing.T) {
	p, _ := testPrompter(strings.NewReader("x\r"))
	text, err := p.AskText(context.Background(), "Body", "a\r\nb\rc")
	require.NoError(t, err)
	require.Equal(t, "xa\r\nb\r\nc", text, "the cursor starts at the beginning and edited lines keep CRLF")
}

func TestBodyFieldSeedsLFLineBreaks(t *testing.T) {
	value := "a\r\nb\rc\nd"
	bodyField("Body", &value)
	require.Equal(t, "a\nb\nc\nd", value)
	require.Equal(t, value, fieldText("a\r\nb\rc\nd"))
}

func TestMilestoneFormOffersNoEditorShortcutInAccessibleMode(t *testing.T) {
	p, rendered := accessiblePrompter(t, "v1\n\n\n")
	_, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{Editor: "vi"})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Milestone description (markdown):")
	require.NotContains(t, rendered.String(), "ctrl+e", "no key opens an editor in accessible mode")
}

func TestBodyFieldOffersTheEditorOutsideAccessibleMode(t *testing.T) {
	for _, tc := range []struct{ term, editor, title string }{
		{"xterm", "vi", "Body (markdown, ctrl+e opens your editor):"},
		{"xterm", "", "Body (markdown):"},
		{"dumb", "vi", "Body (markdown):"},
	} {
		t.Setenv("TERM", tc.term)
		var value string
		require.Equal(t, tc.title, bodyField("Body (markdown):", &value).withEditor(tc.editor).title, tc)
	}
}
