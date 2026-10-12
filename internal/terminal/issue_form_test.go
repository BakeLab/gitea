// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"path/filepath"
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

func TestIssueFormMarksTitleRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "Title\n")
	_, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Title (required):")
}
