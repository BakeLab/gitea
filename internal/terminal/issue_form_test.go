// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// lineReader returns one line per read, so that each accessible prompt reads only its answer.
type lineReader struct{ text string }

func (r *lineReader) Read(p []byte) (int, error) {
	if r.text == "" {
		return 0, io.EOF
	}
	line, _, _ := strings.Cut(r.text, "\n")
	n := copy(p, line+"\n")
	r.text = r.text[min(n, len(r.text)):]
	return n, nil
}

func TestIssueFormAsksWhatsNextBeforeMetadata(t *testing.T) {
	choices := IssueFormChoices{AskNext: true, CanPush: true, Assignees: []string{"alice"}, EditBody: noEdit}
	for _, test := range []struct {
		name, input string
		canceled    bool
	}{
		{"submit", "Title\n1\n", false},
		{"cancel", "Title\n3\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TERM", "dumb")
			p, rendered := testPrompter(&lineReader{text: test.input})
			values, err := p.collectIssueProperties(context.Background(), "", "", choices)
			require.Contains(t, rendered.String(), "What's next?")
			require.Contains(t, rendered.String(), "Add metadata")
			require.NotContains(t, rendered.String(), "Assignees:")
			if test.canceled {
				require.ErrorIs(t, err, ErrCanceled)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "Title", values.Title)
			require.Empty(t, values.Assignees)
		})
	}
	t.Setenv("TERM", "dumb")
	p, rendered := testPrompter(&lineReader{text: "Title\n2\n1\n0\n2028-01-15\n"})
	values, err := p.collectIssueProperties(context.Background(), "", "", choices)
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Assignees:")
	require.Equal(t, []string{"alice"}, values.Assignees)
	require.Equal(t, "2028-01-15", values.Deadline.Format(time.DateOnly))
	p, rendered = accessiblePrompter(t, "Title\n")
	_, err = p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.NotContains(t, rendered.String(), "What's next?")
}

func TestIssueFormMarksTitleRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "Title\n")
	_, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Title (required):")
}
