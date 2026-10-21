// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectIssuePropertiesUsesEditBodyDraft(t *testing.T) {
	var initial string
	p, _ := testPrompter(bytes.NewBufferString("Drafted Title\r\r"))
	values, err := p.collectIssueProperties(context.Background(), "", "template body", IssueFormChoices{
		EditBody: func(_ context.Context, body string) (Draft, error) {
			initial = body
			return Draft{Path: filepath.Join(t.TempDir(), "draft.md"), Body: "edited body"}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, "template body", initial)
	require.Equal(t, "Drafted Title", values.Title)
	require.Equal(t, "edited body", values.Body)
	require.NotEmpty(t, values.Draft.Path)
}

func TestIssueFormAsksWhatsNextBeforeMetadata(t *testing.T) {
	choices := IssueFormChoices{AskNext: true, Assignees: []string{"alice"}, EditBody: noEdit}
	for _, tc := range []struct {
		name, input string
		want        IssueFormValues
		err         error
	}{
		{name: "submit", input: "Title\n1\n", want: IssueFormValues{Title: "Title"}},
		{name: "cancel", input: "Title\n3\n", err: ErrCanceled},
		{name: "metadata", input: "Title\n2\n1\n0\n2028-01-15\n1\n", want: IssueFormValues{Title: "Title", Assignees: []string{"alice"}, Deadline: mustDueDate(t, "2028-01-15")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rendered := linePrompter(t, tc.input)
			values, err := p.collectIssueProperties(context.Background(), "", "", choices)
			require.ErrorIs(t, err, tc.err)
			require.Contains(t, rendered.String(), "What's next?")
			require.Equal(t, tc.name == "metadata", strings.Contains(rendered.String(), "Assignees:"))
			if tc.err == nil {
				require.Equal(t, tc.want, values)
			}
		})
	}
}

func TestIssueFormMarksTitleRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "Title\n\n")
	_, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Title (required):")
	require.NotContains(t, rendered.String(), "What's next?")
	require.Contains(t, rendered.String(), "Due date:", "metadata needs no push access")
}

func TestIssueFormWritesBodyBeforeWhatsNext(t *testing.T) {
	edited := false
	choices := IssueFormChoices{AskNext: true, EditBody: func(context.Context, string) (Draft, error) {
		edited = true
		return Draft{Path: "draft.md", Body: "body"}, nil
	}}
	p, _ := linePrompter(t, "Title\n2\n")
	values, err := p.collectIssueProperties(context.Background(), "", "", choices)
	require.ErrorIs(t, err, ErrCanceled)
	require.True(t, edited, "the editor runs before the user can cancel")
	require.Equal(t, "draft.md", values.Draft.Path)
}

func TestRequiredFieldsRejectBlankAnswers(t *testing.T) {
	require.Error(t, notBlank("   \t"))
	require.Error(t, notBlank(""))
	require.NoError(t, notBlank(" x "))
	p, rendered := linePrompter(t, "   \nTitle\n\n")
	_, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "input cannot be empty")
}

func TestCreateIssueStartsWithTemplateTitle(t *testing.T) {
	p, _, ctx := screenPrompter(t, keyStep{"enter next", "\r"}, keyStep{"new line", "\r"}, keyStep{"What's next?", "\r"})
	values, err := p.CreateIssue(ctx, IssueFormChoices{Template: &IssueTemplateChoice{Name: "Bug", Title: "bug: crash", Body: "## Steps"}})
	require.NoError(t, err)
	require.Equal(t, "bug: crash", values.Title)

	p, rendered := linePrompter(t, "\n\n1\n")
	values, err = p.CreateIssue(context.Background(), IssueFormChoices{Template: &IssueTemplateChoice{Name: "Bug", Title: "bug: crash"}})
	require.NoError(t, err)
	require.Equal(t, "bug: crash", values.Title, "an empty accessible answer keeps the template title")
	require.NotContains(t, rendered.String(), "input cannot be empty")
}

func TestCreateIssueRejectsClearedTemplateTitle(t *testing.T) {
	p, _, ctx := screenPrompter(t, keyStep{"enter next", "\x15\r"}, keyStep{"input cannot be empty", "x\r"}, keyStep{"new line", "\r"}, keyStep{"What's next?", "\r"})
	values, err := p.CreateIssue(ctx, IssueFormChoices{Template: &IssueTemplateChoice{Name: "Bug", Title: "bug: crash"}})
	require.NoError(t, err)
	require.Equal(t, "x", values.Title)
}

func TestCreateIssueReturnsLabelsOfTemplate(t *testing.T) {
	bug := IssueTemplateChoice{Name: "Bug", Title: "bug: ", Body: "## Steps", Labels: []string{"bug"}}
	for _, tc := range []struct {
		name, input string
		choices     IssueFormChoices
		want        IssueFormValues
	}{
		{name: "given", input: "Crash\n\n1\n", choices: IssueFormChoices{Template: &bug}, want: IssueFormValues{Title: "Crash", Body: "## Steps", TemplateLabels: []string{"bug"}}},
		{name: "picked", input: "2\nCrash\n\n1\n", choices: IssueFormChoices{Templates: []IssueTemplateChoice{bug}}, want: IssueFormValues{Title: "Crash", Body: "## Steps", TemplateLabels: []string{"bug"}}},
		{name: "none", input: "1\nCrash\n\n1\n", choices: IssueFormChoices{Templates: []IssueTemplateChoice{bug}}, want: IssueFormValues{Title: "Crash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := linePrompter(t, tc.input)
			values, err := p.CreateIssue(context.Background(), tc.choices)
			require.NoError(t, err)
			require.Equal(t, tc.want, values)
		})
	}
}

func TestCreateIssueInlineBodyKeepsTabsAndCRLFOfUnchangedLines(t *testing.T) {
	body := "intro\r\n```make\r\nall:\r\n\techo hi\r\n```\r\n"
	template := &IssueTemplateChoice{Name: "Bug", Title: "Title", Body: body}
	p, _, ctx := screenPrompter(t, keyStep{"enter next", "\r"}, keyStep{"new line", "top\n\r"}, keyStep{"What's next?", "\r"})
	values, err := p.CreateIssue(ctx, IssueFormChoices{Template: template})
	require.NoError(t, err)
	require.Equal(t, "top\r\n"+body, values.Body)

	p, _, ctx = screenPrompter(t, keyStep{"enter next", "\r"}, keyStep{"new line", "\r"}, keyStep{"What's next?", "\r"})
	values, err = p.CreateIssue(ctx, IssueFormChoices{Template: template})
	require.NoError(t, err)
	require.Equal(t, body, values.Body)
}
