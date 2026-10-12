// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
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

func TestPullFormMarksRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name, input   string
		choices       PullFormChoices
		want, without []string
	}{
		{
			"without head", "n\nmain\nalice\nfeature\ny\nTitle\n",
			PullFormChoices{},
			[]string{"Target branch (required):", "Source branch (required):", "Title (required):"},
			nil,
		},
		{
			"with head", "n\n\n\n\n\nTitle\n",
			PullFormChoices{Base: "main", HeadOwner: "alice", HeadBranch: "feature"},
			[]string{"Source branch:", "Allow maintainers to push to the source branch:"},
			[]string{"Source branch (required)"},
		},
		{"agit", "y\nmain\ntopic\nTitle\n", PullFormChoices{}, []string{"Topic branch (required):"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rendered := accessiblePrompter(t, tc.input)
			tc.choices.EditBody = noEdit
			_, err := p.CreatePull(context.Background(), tc.choices)
			require.NoError(t, err)
			for _, want := range tc.want {
				require.Contains(t, rendered.String(), want)
			}
			for _, unwanted := range tc.without {
				require.NotContains(t, rendered.String(), unwanted)
			}
		})
	}
}

// stagedInput feeds one form's keystrokes per stage. A finished form's canceled read loop can still be inside Read,
// and bubbletea drops the byte that Read returns. When a stage runs dry, the finished form's loop blocks first and
// the next form's loop second, so the next stage is handed to that second reader and never to the stale one.
type stagedInput struct {
	mu     sync.Mutex
	cur    string
	stages []string
	idle   int
}

func (s *stagedInput) Read(p []byte) (int, error) {
	s.mu.Lock()
	for s.cur == "" {
		s.idle++
		if s.idle < 2 || len(s.stages) == 0 {
			s.mu.Unlock()
			select {} // no more input for this reader; the loop is abandoned with its form
		}
		s.cur, s.stages, s.idle = s.stages[0], s.stages[1:], 0
	}
	p[0], s.cur = s.cur[0], s.cur[1:]
	s.mu.Unlock()
	return 1, nil
}

func TestCreatePullPreservesDraftOnEditorFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "draft.md")
	called := false
	p, _ := testPrompter(&stagedInput{cur: "n\r", stages: []string{"main\ralice\rfeature\ry\r", "Title\r"}})
	values, err := p.CreatePull(ctx, PullFormChoices{
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
