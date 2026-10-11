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
	p, _ := testPrompter(bytes.NewBufferString("New Title\r"))
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
	values, err := p.EditIssue(context.Background(), IssueEditFormChoices{Title: "Title", Deadline: &deadline, CanPush: true})
	require.NoError(t, err)
	require.Nil(t, values.Deadline)
	require.False(t, values.ClearDeadline)
}

func TestWithCurrentKeepsSeededValuesSelectable(t *testing.T) {
	options := []IssueLabelChoice{{Name: "bug", ID: 2}}
	merged := withCurrent(options, IssueLabelChoice{Name: "bug", ID: 2}, IssueLabelChoice{Name: "org", ID: 7})
	require.Equal(t, []IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "org", ID: 7}}, merged)
	require.Len(t, options, 1)
}
