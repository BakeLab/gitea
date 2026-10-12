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
	p, _ := testPrompter(bytes.NewBufferString("Drafted Title\r"))
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
	choices := IssueFormChoices{AskNext: true, CanPush: true, Assignees: []string{"alice"}, EditBody: noEdit}
	for _, tc := range []struct {
		name, input string
		want        IssueFormValues
		err         error
	}{
		{name: "submit", input: "Title\n1\n", want: IssueFormValues{Title: "Title"}},
		{name: "cancel", input: "Title\n3\n", err: ErrCanceled},
		{name: "metadata", input: "Title\n2\n1\n0\n2028-01-15\n", want: IssueFormValues{Title: "Title", Assignees: []string{"alice"}, Deadline: mustDueDate(t, "2028-01-15")}},
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
	p, rendered := accessiblePrompter(t, "Title\n")
	_, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Title (required):")
	require.NotContains(t, rendered.String(), "What's next?")
}
