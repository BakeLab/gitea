// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestPR(index int64, title string) *gitea.PullRequest {
	now := time.Now()
	return &gitea.PullRequest{
		Index:   index,
		Title:   title,
		State:   gitea.StateOpen,
		Poster:  &gitea.User{UserName: "testuser"},
		Head:    &gitea.PRBranchInfo{Ref: "branch", Name: "branch"},
		Base:    &gitea.PRBranchInfo{Ref: "main", Name: "main"},
		Created: &now,
		Updated: &now,
	}
}

func TestFormatCIStatusNil(t *testing.T) {
	assert.Equal(t, "", formatCIStatus(nil, false))
	assert.Equal(t, "", formatCIStatus(nil, true))
}

func TestFormatCIStatusEmpty(t *testing.T) {
	ci := &gitea.CombinedStatus{Statuses: []*gitea.Status{}}
	assert.Equal(t, "", formatCIStatus(ci, false))
	assert.Equal(t, "", formatCIStatus(ci, true))
}

func TestFormatCIStatusMachineReadable(t *testing.T) {
	ci := &gitea.CombinedStatus{
		State: gitea.StatusSuccess,
		Statuses: []*gitea.Status{
			{State: gitea.StatusSuccess, Context: "lint"},
		},
	}
	assert.Equal(t, "success", formatCIStatus(ci, true))

	ci.State = gitea.StatusPending
	ci.Statuses = []*gitea.Status{
		{State: gitea.StatusPending, Context: "build"},
	}
	assert.Equal(t, "pending", formatCIStatus(ci, true))
}

func TestFormatCIStatusSingle(t *testing.T) {
	ci := &gitea.CombinedStatus{
		State: gitea.StatusSuccess,
		Statuses: []*gitea.Status{
			{State: gitea.StatusSuccess, Context: "lint"},
		},
	}
	assert.Equal(t, "✓ lint", formatCIStatus(ci, false))
}

func TestFormatCIStatusMultiple(t *testing.T) {
	ci := &gitea.CombinedStatus{
		State: gitea.StatusFailure,
		Statuses: []*gitea.Status{
			{State: gitea.StatusSuccess, Context: "lint"},
			{State: gitea.StatusPending, Context: "build"},
			{State: gitea.StatusFailure, Context: "test"},
		},
	}
	assert.Equal(t, "✓ lint, ⏳ build, ❌ test", formatCIStatus(ci, false))
}

func TestFormatCIStatusAllStates(t *testing.T) {
	tests := []struct {
		state    gitea.StatusState
		context  string
		expected string
	}{
		{gitea.StatusSuccess, "s", "✓ s"},
		{gitea.StatusPending, "p", "⏳ p"},
		{gitea.StatusWarning, "w", "⚠ w"},
		{gitea.StatusError, "e", "✘ e"},
		{gitea.StatusFailure, "f", "❌ f"},
	}
	for _, tt := range tests {
		ci := &gitea.CombinedStatus{
			State:    tt.state,
			Statuses: []*gitea.Status{{State: tt.state, Context: tt.context}},
		}
		assert.Equal(t, tt.expected, formatCIStatus(ci, false), "state: %s", tt.state)
	}
}

func TestPullsListWithCIField(t *testing.T) {
	prs := []*gitea.PullRequest{
		newTestPR(1, "feat: add feature"),
		newTestPR(2, "fix: bug fix"),
	}

	ciStatuses := map[int64]*gitea.CombinedStatus{
		1: {
			State: gitea.StatusSuccess,
			Statuses: []*gitea.Status{
				{State: gitea.StatusSuccess, Context: "ci/build"},
			},
		},
		2: {
			State: gitea.StatusFailure,
			Statuses: []*gitea.Status{
				{State: gitea.StatusFailure, Context: "ci/test"},
			},
		},
	}

	buf := &bytes.Buffer{}
	tbl := tableFromItems(
		[]string{"index", "ci"},
		[]printable{
			&printablePull{prs[0], &map[int64]string{}, &ciStatuses},
			&printablePull{prs[1], &map[int64]string{}, &ciStatuses},
		},
		true,
	)
	require.NoError(t, tbl.fprint(buf, "json"))

	var result []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0]["index"])
	assert.Equal(t, "success", result[0]["ci"])
	assert.Equal(t, "2", result[1]["index"])
	assert.Equal(t, "failure", result[1]["ci"])
}

func TestPullsListCIFieldEmpty(t *testing.T) {
	prs := []*gitea.PullRequest{newTestPR(1, "no ci")}
	ciStatuses := map[int64]*gitea.CombinedStatus{}

	buf := &bytes.Buffer{}
	tbl := tableFromItems(
		[]string{"index", "ci"},
		[]printable{
			&printablePull{prs[0], &map[int64]string{}, &ciStatuses},
		},
		true,
	)
	require.NoError(t, tbl.fprint(buf, "json"))

	var result []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "", result[0]["ci"])
}

func TestPullsListNilCIStatusesWithCIField(t *testing.T) {
	prs := []*gitea.PullRequest{newTestPR(1, "nil ci")}

	buf := &bytes.Buffer{}
	tbl := tableFromItems(
		[]string{"index", "ci"},
		[]printable{
			&printablePull{prs[0], &map[int64]string{}, nil},
		},
		true,
	)
	require.NoError(t, tbl.fprint(buf, "json"))

	var result []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &result))
	require.Len(t, result, 1)
	assert.Equal(t, "", result[0]["ci"])
}

func TestPullsListNoCIFieldNoPanic(t *testing.T) {
	prs := []*gitea.PullRequest{newTestPR(1, "test")}
	require.NoError(t, PullsList(prs, "", []string{"index", "title"}, nil))
}

func TestPullFieldsContainsCI(t *testing.T) {
	assert.True(t, slices.Contains(PullFields, "ci"), "PullFields should contain 'ci'")
}
