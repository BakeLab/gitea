// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultPullTitle(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"feature", "Feature"},
		{"fix-my-bug", "Fix My Bug"},
		{"alice:add_login", "Add Login"},
		{"über-fix", "Über Fix"},
	} {
		require.Equal(t, tc.want, DefaultPullTitle(tc.in), tc.in)
	}
}

func TestPullFormAsksTitleBodyThenWhatsNext(t *testing.T) {
	known := PullFormChoices{Repo: "alice/x", BaseOwner: "alice", Base: "main", HeadOwner: "alice", HeadBranch: "feature", Title: "Seeded", Body: "Seeded body", AllowMaintainerEdits: true}
	for _, tc := range []struct {
		name, input string
		choices     PullFormChoices
		want        PullFormValues
		err         error
		rendered    []string
		hidden      []string
	}{
		{
			name: "submit", input: "Seeded\n\n1\n", choices: known,
			want:     PullFormValues{IssueFormValues: IssueFormValues{Title: "Seeded", Body: "Seeded body"}, Base: "main", Head: "feature", Topic: "feature", AllowMaintainerEdits: true},
			rendered: []string{"Creating pull request for feature into main in alice/x", "Title (required) [Seeded]:", BodyTitle, "What's next?", "Change branches"},
			hidden:   []string{"Base branch", "AGit", "Issue", "Add metadata"},
		},
		{
			name: "submit as draft", input: "Add login\n\n2\n", choices: known,
			want: PullFormValues{IssueFormValues: IssueFormValues{Title: "Add login", Body: "Seeded body"}, Base: "main", Head: "feature", Topic: "feature", AllowMaintainerEdits: true, AsDraft: true},
		},
		{name: "cancel", input: "Seeded\n\n4\n", choices: known, err: ErrCanceled},
		{
			name: "unknown head asks the branches first", input: "main\nn\nbob\nfix-typo\n\n\nFix Typo\n\n1\n", choices: PullFormChoices{Repo: "alice/x", BaseOwner: "alice", Base: "main"},
			want:     PullFormValues{IssueFormValues: IssueFormValues{Title: "Fix Typo"}, Base: "main", Head: "bob:fix-typo"},
			rendered: []string{"Base branch (required) [main]:", "Push through AGit", "Head branch (required):", "Creating pull request for bob:fix-typo into main in alice/x"},
		},
		{
			name: "another base asked first seeds its body", input: "release\nn\n\nfix\n\n\nFix\n\n1\n", choices: PullFormChoices{
				Repo: "alice/x", BaseOwner: "alice", Base: "main", Body: "Template of main",
				SeedBody: func(_ context.Context, base, head string) (string, error) {
					return "Template of " + base + " for " + head, nil
				},
			},
			want: PullFormValues{IssueFormValues: IssueFormValues{Title: "Fix", Body: "Template of release for fix"}, Base: "release", Head: "fix"},
		},
		{
			name: "another head asked first seeds its body", input: "\nn\n\nfix\n\n\nFix\n\n1\n", choices: PullFormChoices{
				Repo: "alice/x", BaseOwner: "alice", Base: "main", HeadBranch: "main", Body: "Commit on main",
				SeedBody: func(_ context.Context, base, head string) (string, error) {
					return "Template of " + base + " for " + head, nil
				},
			},
			want: PullFormValues{IssueFormValues: IssueFormValues{Title: "Fix", Body: "Template of main for fix"}, Base: "main", Head: "fix", Topic: "main"},
		},
		{
			name: "metadata", input: "Seeded\n\n3\n2\n2026-12-24\n1\n", choices: func() PullFormChoices {
				c := known
				c.CanPush, c.Milestones = true, []IssueMilestoneChoice{{Title: "v1.0", ID: 4}}
				return c
			}(),
			want:     PullFormValues{IssueFormValues: IssueFormValues{Title: "Seeded", Body: "Seeded body", Milestone: 4, Deadline: mustDueDate(t, "2026-12-24")}, Base: "main", Head: "feature", Topic: "feature", AllowMaintainerEdits: true},
			rendered: []string{"Add metadata (assignees, labels, milestone, due date)", "Milestone:", "Due date:"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rendered := linePrompter(t, tc.input)
			values, err := p.CreatePull(context.Background(), tc.choices)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, values)
			for _, want := range tc.rendered {
				require.Contains(t, rendered.String(), want)
			}
			for _, unwanted := range tc.hidden {
				require.NotContains(t, rendered.String(), unwanted)
			}
		})
	}
}

func mustDueDate(t *testing.T, raw string) *time.Time {
	t.Helper()
	due, ok, err := parseDueDate(raw)
	require.NoError(t, err)
	if !ok {
		return nil
	}
	return &due
}

func TestCreatePullPreservesDraftOnEditorFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.md")
	called := false
	p, _ := linePrompter(t, "Title\n")
	values, err := p.CreatePull(context.Background(), PullFormChoices{
		BaseOwner: "alice", Base: "main", HeadOwner: "alice", HeadBranch: "feature",
		EditBody: func(context.Context, string) (Draft, error) {
			called = true
			return Draft{Path: path, Body: "partial"}, errors.New("editor failed")
		},
	})
	require.True(t, called)
	require.ErrorContains(t, err, "editor failed")
	require.Equal(t, path, values.Draft.Path)
}

func TestCreatePullInlineBodyKeepsTabsAndCRLFOfUnchangedLines(t *testing.T) {
	body := "intro\r\n```make\r\nall:\r\n\techo hi\r\n```\r\n"
	choices := PullFormChoices{BaseOwner: "alice", Base: "main", HeadOwner: "alice", HeadBranch: "feature", Body: body}
	p, _, ctx := screenPrompter(t, keyStep{"enter next", "\r"}, keyStep{"new line", "top\n\r"}, keyStep{"What's next?", "\r"})
	values, err := p.CreatePull(ctx, choices)
	require.NoError(t, err)
	require.Equal(t, "top\r\n"+body, values.Body)

	p, _, ctx = screenPrompter(t, keyStep{"enter next", "\r"}, keyStep{"new line", "\r"}, keyStep{"What's next?", "\r"})
	values, err = p.CreatePull(ctx, choices)
	require.NoError(t, err)
	require.Equal(t, body, values.Body)
}
