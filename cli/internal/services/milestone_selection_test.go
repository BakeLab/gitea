// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMilestoneNamesIncludeClosedAndRejectAmbiguity(t *testing.T) {
	duplicate := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "all", r.URL.Query().Get("state"))
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"id":1,"title":"Other"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":2,"title":"Release","state":"closed"}]`))
		case "3":
			if duplicate {
				_, _ = w.Write([]byte(`[{"id":3,"title":"Release"}]`))
				return
			}
			fallthrough
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	service := Milestones{Client: client, Repository: "alice/project"}
	item, err := service.FindOne(context.Background(), "Release", false)
	require.NoError(t, err)
	require.EqualValues(t, 2, item.ID)
	_, err = service.FindOne(context.Background(), "missing", false)
	require.ErrorContains(t, err, "not found")
	duplicate = true
	_, err = service.FindOne(context.Background(), "Release", false)
	require.ErrorContains(t, err, "ambiguous")
}

func TestMilestoneCreateUniqueRefusesOrUpdatesAFoldedTitle(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/milestones":
			writeFirstPage(w, r, `[{"id":1,"title":"v1.0","due_on":"2027-01-01T00:00:00Z"}]`)
			return
		case r.Method == http.MethodGet:
			assert.Equal(t, "/api/v1/repos/alice/project/milestones/1", r.URL.Path)
			_, _ = w.Write([]byte(`{"id":1,"title":"v1.0","due_on":"2027-01-01T00:00:00Z"}`))
			return
		}
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		_, _ = w.Write([]byte(`{"id":1,"title":"v1.0"}`))
	}))
	service := Milestones{Client: client, Repository: "alice/project"}
	_, _, err := service.CreateUnique(context.Background(), MilestoneOptions{Title: new("V1.0")}, false)
	require.EqualError(t, err, `milestone "v1.0" already exists in alice/project`)
	require.Empty(t, requests)

	_, updated, err := service.CreateUnique(context.Background(), MilestoneOptions{Title: new("V1.0"), Description: new("First")}, true)
	require.NoError(t, err)
	require.True(t, updated)
	_, updated, err = service.CreateUnique(context.Background(), MilestoneOptions{Title: new("v2.0")}, true)
	require.NoError(t, err)
	require.False(t, updated)
	require.Equal(t, []string{
		`PATCH /api/v1/repos/alice/project/milestones/1 {"title":"","description":"First","state":null,"due_on":"2027-01-01T00:00:00Z"}`,
		`POST /api/v1/repos/alice/project/milestones {"title":"v2.0","description":"","state":"","due_on":null}`,
	}, requests)

	existing := []Milestone{{ID: 1, Title: "v1.0"}, {ID: 2, Title: "v2.0"}}
	_, err = service.EditUnique(context.Background(), existing, existing[1], MilestoneOptions{Title: new(" V1.0 ")})
	require.EqualError(t, err, `milestone "v1.0" already exists in alice/project`)
	require.Len(t, requests, 2)
}

func TestMilestoneFilterIDsSendValuesGiteaReadsBackAsTheSameMilestone(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFirstPage(w, r, `[{"id":1,"title":"v1.0"},{"id":2,"title":"1"},{"id":3,"title":"a,b"},{"id":4,"title":"dup"},{"id":5,"title":"dup"},{"id":6,"title":"x,y"},{"id":7,"title":"6"}]`)
	}))
	service := Milestones{Client: client, Repository: "alice/project"}
	values, err := service.FilterIDs(context.Background(), []string{"1", "2", "a,b", "4", "v1.0"})
	require.NoError(t, err)
	require.Equal(t, []string{"v1.0", "1", "3", "4", "v1.0"}, values)
	_, err = service.FilterIDs(context.Background(), []string{"6"})
	require.EqualError(t, err, `milestone "x,y" (ID 6) cannot filter issues: another milestone of alice/project is titled "6"`)
	_, err = service.FilterIDs(context.Background(), []string{"nope"})
	require.EqualError(t, err, `milestone "nope" not found in alice/project`)
	value, err := service.FilterValue(context.Background(), Milestone{ID: 5, Title: "dup"})
	require.NoError(t, err)
	require.Equal(t, "5", value)
}
