// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrganizationLabelArchivingIsSentOnEdit(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		_, _ = w.Write([]byte(`{"id":5,"name":"bug","color":"ee0701","is_archived":true}`))
	}))
	service := Labels{Client: client, Organization: "company"}
	name, color, archived, active := "bug", "ee0701", true, false

	label, err := service.Create(context.Background(), LabelOptions{Name: &name, Color: &color, Archived: &archived})
	require.NoError(t, err)
	require.Equal(t, Label{ID: 5, Name: "bug", Color: "ee0701", Archived: true}, label)
	_, err = service.Create(context.Background(), LabelOptions{Name: &name, Color: &color, Archived: &active})
	require.NoError(t, err)
	_, err = service.Edit(context.Background(), 5, LabelOptions{Archived: &active})
	require.NoError(t, err)
	_, err = service.Edit(context.Background(), 5, LabelOptions{Name: &name})
	require.NoError(t, err)

	require.Len(t, requests, 4)
	assert.Equal(t, `POST /api/v1/orgs/company/labels {"name":"bug","color":"ee0701","description":"","exclusive":false}`, requests[0])
	assert.NotContains(t, requests[1], "is_archived")
	assert.Equal(t, `PATCH /api/v1/orgs/company/labels/5 {"name":null,"color":null,"description":null,"is_archived":false}`, requests[2])
	assert.NotContains(t, requests[3], "is_archived")
	assert.Contains(t, requests[3], `"name":"bug"`)
}

func TestRepositoryLabelEditSendsOnlySuppliedFields(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PATCH /api/v1/repos/alice/project/labels/5", r.Method+" "+r.URL.Path)
		var body gitea.EditLabelOption
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		archived := true
		assert.Equal(t, gitea.EditLabelOption{IsArchived: &archived}, body)
		_, _ = w.Write([]byte(`{"id":5,"name":"bug","is_archived":true}`))
	}))
	archived := true
	label, err := (Labels{Client: client, Repository: "alice/project"}).Edit(context.Background(), 5, LabelOptions{Archived: &archived})
	require.NoError(t, err)
	require.True(t, label.Archived)
}

func TestMilestoneFromMapsServerFields(t *testing.T) {
	deadline := "2028-01-15T23:59:59-08:00"
	require.Equal(t, Milestone{ID: 1, Title: "v1", Description: "First", State: "closed", Deadline: &deadline, OpenIssues: 2, ClosedIssues: 3},
		milestoneFrom(decode[gitea.Milestone](t, `{"id":1,"title":"v1","description":"First","state":"closed","due_on":"2028-01-15T23:59:59-08:00","open_issues":2,"closed_issues":3}`)))
}

func TestMilestoneRejectsInvalidDeadlineBeforeRequests(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	service := Milestones{Client: client, Repository: "alice/project"}
	invalid := "tomorrow"
	_, err := service.Create(context.Background(), MilestoneOptions{Deadline: &invalid})
	require.ErrorContains(t, err, "invalid milestone deadline")
	_, err = service.Edit(context.Background(), 1, MilestoneOptions{Deadline: &invalid})
	require.ErrorContains(t, err, "invalid milestone deadline")
}

func TestOrganizationArchivedLabelCreateFollowsUpWithPatch(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		// Gitea ignores is_archived on create and honors it on edit.
		archived := "false"
		if r.Method == http.MethodPatch {
			archived = "true"
		}
		_, _ = w.Write([]byte(`{"id":5,"name":"bug","is_archived":` + archived + `}`))
	}))
	name, archived := "bug", true
	label, err := (Labels{Client: client, Organization: "company"}).Create(context.Background(), LabelOptions{Name: &name, Archived: &archived})
	require.NoError(t, err)
	require.True(t, label.Archived)
	require.Equal(t, []string{
		`POST /api/v1/orgs/company/labels {"name":"bug","color":"","description":"","exclusive":false}`,
		`PATCH /api/v1/orgs/company/labels/5 {"name":null,"color":null,"description":null,"is_archived":true}`,
	}, requests)
}

func TestLabelAndMilestoneMutationsUseTheirScopes(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"id":4,"title":"v1.0","state":"closed","due_on":"2027-01-01T00:00:00Z"}`))
	}))
	require.NoError(t, (Labels{Client: client, Repository: "alice/project"}).Delete(context.Background(), 5))
	require.NoError(t, (Labels{Client: client, Organization: "company"}).Delete(context.Background(), 6))
	milestones := Milestones{Client: client, Repository: "alice/project"}
	created, err := milestones.Create(context.Background(), MilestoneOptions{Title: new("v1.0"), State: new("closed"), Deadline: new("2027-01-01T00:00:00Z")})
	require.NoError(t, err)
	require.Equal(t, client.BaseURL+"/alice/project/milestone/4", created.URL)
	require.NoError(t, milestones.Delete(context.Background(), 4))
	require.Equal(t, []string{
		"DELETE /api/v1/repos/alice/project/labels/5 ",
		"DELETE /api/v1/orgs/company/labels/6 ",
		`POST /api/v1/repos/alice/project/milestones {"title":"v1.0","description":"","state":"closed","due_on":"2027-01-01T00:00:00Z"}`,
		"DELETE /api/v1/repos/alice/project/milestones/4 ",
	}, requests)
}
