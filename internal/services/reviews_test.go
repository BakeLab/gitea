// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewCommentsContinueWhenServerCapsPageSize(t *testing.T) {
	reviewPages, collections := 0, 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/owner/repo/pulls/3/reviews" {
			reviewPages++
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write([]byte(`[{"id":1,"comments_count":1}]`))
			case "2":
				_, _ = w.Write([]byte(`[{"id":2,"comments_count":1}]`))
			case "3":
				_, _ = w.Write([]byte(`[]`))
			default:
				t.Errorf("unexpected review page %q", r.URL.Query().Get("page"))
				w.WriteHeader(http.StatusBadRequest)
			}
			return
		}
		collections++
		assert.Empty(t, r.URL.RawQuery, "per-review comments are unpaginated")
		if r.URL.Path == "/api/v1/repos/owner/repo/pulls/3/reviews/1/comments" {
			_, _ = w.Write([]byte(`[{"id":5,"position":4,"pull_request_review_id":1,"user":{"login":"alice"},"resolver":{"login":"bob"}}]`))
		} else {
			assert.Equal(t, "/api/v1/repos/owner/repo/pulls/3/reviews/2/comments", r.URL.Path)
			_, _ = w.Write([]byte(`[{"id":2}]`))
		}
	}))
	items, err := (Reviews{Client: client, Repository: "owner/repo"}).Comments(context.Background(), 3, 0, 1, 2)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 5, items[0].ID)
	require.EqualValues(t, 4, items[0].Line)
	require.Equal(t, "alice", items[0].Author)
	require.Equal(t, "bob", items[0].Resolver)
	require.Equal(t, 3, reviewPages)
	require.Equal(t, 2, collections)
}

func TestReviewDismissalReturnsTheUpdatedReview(t *testing.T) {
	for _, dismiss := range []bool{false, true} {
		t.Run(fmt.Sprintf("dismiss=%t", dismiss), func(t *testing.T) {
			calls := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method == http.MethodGet {
					assert.Equal(t, 2, calls, "the review is read after the mutation")
					assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews/2", r.URL.Path)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "dismissed": dismiss})
					return
				}
				assert.Equal(t, http.MethodPost, r.Method)
				if dismiss {
					assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews/2/dismissals", r.URL.Path)
					var data map[string]string
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&data))
					assert.Equal(t, map[string]string{"message": "Reason"}, data)
				} else {
					assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews/2/undismissals", r.URL.Path)
					assert.Zero(t, r.ContentLength)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "dismissed": dismiss})
			}))
			item, err := (Reviews{Client: client, Repository: "owner/repo"}).Dismiss(context.Background(), 1, 2, dismiss, "Reason")
			require.NoError(t, err)
			require.EqualValues(t, 2, item.ID)
			require.Equal(t, dismiss, item.Dismissed)
			require.Equal(t, 2, calls)
		})
	}
}

func TestReviewSubmitPreservesBodyUnlessReplaced(t *testing.T) {
	empty := ""
	for name, test := range map[string]struct {
		body     *string
		wantBody string
		wantGets int
	}{
		"omitted keeps current": {wantBody: "Pending notes", wantGets: 1},
		"explicit empty clears": {body: &empty, wantBody: ""},
	} {
		t.Run(name, func(t *testing.T) {
			gets := 0
			var submitted map[string]string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews/2", r.URL.Path)
				if r.Method == http.MethodGet {
					gets++
					_, _ = w.Write([]byte(`{"id":2,"body":"Pending notes","state":"PENDING"}`))
					return
				}
				assert.Equal(t, http.MethodPost, r.Method)
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&submitted))
				_, _ = w.Write([]byte(`{"id":2,"state":"APPROVED"}`))
			}))
			item, err := (Reviews{Client: client, Repository: "owner/repo"}).Submit(context.Background(), 1, 2, ReviewSubmitOptions{Event: "APPROVED", Body: test.body})
			require.NoError(t, err)
			require.EqualValues(t, 2, item.ID)
			require.Equal(t, test.wantGets, gets)
			require.Equal(t, map[string]string{"event": "APPROVED", "body": test.wantBody}, submitted)
		})
	}
}

func TestReviewSubmitAllowsEmptyCommentBody(t *testing.T) {
	var submitted map[string]string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST /api/v1/repos/owner/repo/pulls/1/reviews/2", r.Method+" "+r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&submitted))
		_, _ = w.Write([]byte(`{"id":2,"state":"COMMENT","comments_count":1}`))
	}))
	empty := ""
	item, err := (Reviews{Client: client, Repository: "owner/repo"}).Submit(context.Background(), 1, 2, ReviewSubmitOptions{Event: "COMMENT", Body: &empty})
	require.NoError(t, err)
	require.Equal(t, Review{ID: 2, State: "COMMENT", CommentsCount: 1}, item)
	require.Equal(t, map[string]string{"event": "COMMENT", "body": ""}, submitted)
}

func TestReviewCreateSendsInlineComments(t *testing.T) {
	var created struct {
		Event    string `json:"event"`
		Body     string `json:"body"`
		Commit   string `json:"commit_id"`
		Comments []InlineComment
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews", r.URL.Path)
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&created))
		_, _ = w.Write([]byte(`{"id":7,"state":"REQUEST_CHANGES","user":{"login":"alice"},"commit_id":"abc"}`))
	}))
	item, err := (Reviews{Client: client, Repository: "owner/repo"}).Create(context.Background(), 1, ReviewOptions{
		Event: "REQUEST_CHANGES", Body: "Fix", Commit: "abc",
		Comments: []InlineComment{{Path: "a.go", Body: "here", NewLine: 3}, {Path: "b.go", Body: "there", OldLine: 2}},
	})
	require.NoError(t, err)
	require.Equal(t, Review{ID: 7, State: "REQUEST_CHANGES", Author: "alice", Commit: "abc"}, item)
	require.Equal(t, "REQUEST_CHANGES", created.Event)
	require.Equal(t, "Fix", created.Body)
	require.Equal(t, "abc", created.Commit)
	require.Equal(t, []InlineComment{{Path: "a.go", Body: "here", NewLine: 3}, {Path: "b.go", Body: "there", OldLine: 2}}, created.Comments)
}

func TestReviewResolveAndReplyUseCommentEndpoints(t *testing.T) {
	paths := []string{}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/v1/repos/owner/repo/pulls/1/comments/5/replies" {
			var reply map[string]string
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&reply))
			assert.Equal(t, map[string]string{"body": "Done"}, reply)
			_, _ = w.Write([]byte(`{"id":6,"body":"Done","user":{"login":"bob"},"pull_request_review_id":3}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	service := Reviews{Client: client, Repository: "owner/repo"}
	require.NoError(t, service.Resolve(context.Background(), 5, true))
	require.NoError(t, service.Resolve(context.Background(), 5, false))
	reply, err := service.Reply(context.Background(), 1, 5, "Done")
	require.NoError(t, err)
	require.Equal(t, ReviewComment{ID: 6, ReviewID: 3, Body: "Done", Author: "bob"}, reply)
	require.Equal(t, []string{
		"/api/v1/repos/owner/repo/pulls/comments/5/resolve",
		"/api/v1/repos/owner/repo/pulls/comments/5/unresolve",
		"/api/v1/repos/owner/repo/pulls/1/comments/5/replies",
	}, paths)
}

func TestReviewFromMapsServerFields(t *testing.T) {
	review := decode[gitea.PullReview](t, `{"id":2,"body":"LGTM","state":"APPROVED","user":{"login":"alice"},"html_url":"https://gitea.test/r/2",
		"commit_id":"abc","submitted_at":"2026-10-01T12:00:00Z","comments_count":3,"dismissed":true}`)
	require.Equal(t, Review{
		ID: 2, Body: "LGTM", State: "APPROVED", Author: "alice", URL: "https://gitea.test/r/2", Commit: "abc",
		SubmittedAt: "2026-10-01T12:00:00Z", CommentsCount: 3, Dismissed: true,
	}, reviewFrom(review))
}

func TestReviewCommentFromMapsServerFields(t *testing.T) {
	comment := decode[gitea.PullReviewComment](t, `{"id":5,"pull_request_review_id":1,"body":"Nit","user":{"login":"alice"},
		"resolver":{"login":"bob"},"html_url":"https://gitea.test/c/5","path":"main.go","position":12,
		"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T12:00:00Z"}`)
	require.Equal(t, ReviewComment{
		ID: 5, ReviewID: 1, Body: "Nit", Author: "alice", Resolver: "bob", URL: "https://gitea.test/c/5", Path: "main.go", Line: 12,
		CreatedAt: "2026-10-01T12:00:00Z", UpdatedAt: "2026-10-02T12:00:00Z",
	}, reviewCommentFrom(comment))
}
