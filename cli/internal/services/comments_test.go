// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/require"
)

func TestCommentFromMapsServerFields(t *testing.T) {
	require.Equal(t, Comment{ID: 2, Body: "text", Author: "alice", CreatedAt: "2026-10-01T12:00:00Z", UpdatedAt: "2026-10-02T12:00:00+02:00"},
		commentFrom(decode[gitea.Comment](t, `{"id":2,"body":"text","user":{"login":"alice"},"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T12:00:00+02:00"}`)))
}

func TestCommentPaginationIsLocal(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			require.Equal(t, "0", r.URL.Query().Get("limit"))
			_, _ = w.Write([]byte(`[{"id":1},{"id":2},{"id":3}]`))
			return
		}
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "text", body["body"])
		require.Equal(t, "/api/v1/repos/alice/project/issues/7/comments", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":8,"body":"text","html_url":"https://example.test/comment","user":{"login":"alice"}}`))
	}))
	service := Comments{Client: client, Repository: "alice/project"}
	items, err := service.Page(context.Background(), 7, 2, 2)
	require.NoError(t, err)
	require.Len(t, items.Items, 1)
	require.Equal(t, int64(3), items.Items[0].ID)
	require.False(t, items.More)
	first, err := service.Page(context.Background(), 7, 2, 1)
	require.NoError(t, err)
	require.True(t, first.More)
	require.Equal(t, 3, first.Total)
	items, err = service.Page(context.Background(), 7, 2, 3)
	require.NoError(t, err)
	require.Empty(t, items.Items)
	require.NotNil(t, items.Items)
	item, err := service.Create(context.Background(), 7, "text")
	require.NoError(t, err)
	require.Equal(t, "alice", item.Author)
	require.Equal(t, "https://example.test/comment", item.URL)
}

func TestRemoveMilestoneRejectsDifferentMembership(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"milestone":{"id":9}}`))
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	_, err := service.RemoveMilestone(context.Background(), 7, Milestone{ID: 8, Title: "v1.0"})
	require.EqualError(t, err, `#7 is not in milestone "v1.0"`)
	require.Zero(t, mutations)
}

func TestCommentListNamesAMissingIssue(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/o/r/issues/99/comments", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 209, index: 99]"}`))
	}))
	_, err := (Comments{Client: client, Repository: "o/r"}).Page(context.Background(), 99, 30, 1)
	require.EqualError(t, err, "issue #99 not found in o/r")
}

func TestCommentViewExplainsAnEmptyAnswerForReviewComments(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	_, err := Comments{Client: client, Repository: "alice/project"}.View(context.Background(), 9)
	require.EqualError(t, err, "comment 9 in alice/project is not a plain comment, such as an inline review comment, which this command cannot show")
}

func TestCommentEditAndDeleteExplainTimelineEvents(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "PATCH /api/v1/repos/o/r/issues/comments/5":
			require.Equal(t, map[string]any{"body": "new"}, requestJSON(t, r))
			_, _ = w.Write([]byte(`{"id":5,"body":"new","user":{"login":"alice"}}`))
		case "PATCH /api/v1/repos/o/r/issues/comments/6":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /api/v1/repos/o/r/issues/comments/5":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /api/v1/repos/o/r/issues/comments/6":
			w.WriteHeader(http.StatusBadRequest)
		case "GET /api/v1/repos/o/r":
			_, _ = w.Write([]byte(`{"id":1,"full_name":"o/r"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := Comments{Client: client, Repository: "o/r"}
	edited, err := service.Edit(context.Background(), 5, "new")
	require.NoError(t, err)
	require.Equal(t, Comment{ID: 5, Body: "new", Author: "alice"}, edited)
	_, err = service.Edit(context.Background(), 6, "new")
	require.EqualError(t, err, "comment 6 in o/r is a timeline event, such as a label or title change, which cannot be edited")
	_, err = service.Edit(context.Background(), 9, "new")
	require.EqualError(t, err, "comment 9 not found in o/r")

	require.NoError(t, service.Delete(context.Background(), 5))
	require.EqualError(t, service.Delete(context.Background(), 6), "comment 6 in o/r is a timeline event, such as a label or title change, which cannot be deleted")
	require.EqualError(t, service.Delete(context.Background(), 9), "comment 9 not found in o/r")
}

func TestCommentAttachmentUploadStreamsTheFile(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST /api/v1/repos/o/r/issues/comments/5/assets", r.Method+" "+r.URL.Path)
		file, header, err := r.FormFile("attachment")
		require.NoError(t, err)
		content, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, "notes.txt hello", header.Filename+" "+string(content))
		_, _ = w.Write([]byte(`{"id":3,"name":"notes.txt","size":5}`))
	}))
	attachment, err := (Comments{Client: client, Repository: "o/r"}).UploadAttachment(context.Background(), 5, "notes.txt", strings.NewReader("hello"))
	require.NoError(t, err)
	require.Equal(t, IssueAttachment{ID: 3, Name: "notes.txt", Size: 5}, attachment)
}
