// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscussionSearchTeamRequiresOwnerAndInstanceScope(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/issues/search", r.URL.Path)
		require.Equal(t, "team-org", r.URL.Query().Get("owner"))
		require.Equal(t, "devs", r.URL.Query().Get("team"))
		require.Equal(t, "pulls", r.URL.Query().Get("type"))
		_, _ = w.Write([]byte(`[{"id":3,"number":4,"title":"Team PR","html_url":"https://instance/team-org/repo/pulls/4","repository":{"full_name":"team-org/repo"}}]`))
	}))
	service := Issues{Client: client}
	_, err := service.Search(context.Background(), DiscussionSearchOptions{Team: "devs", Pulls: true, State: "open", Limit: 30, Page: 1})
	require.ErrorContains(t, err, "organization owner")
	items, err := service.Search(context.Background(), DiscussionSearchOptions{Owner: "team-org", Team: "devs", Pulls: true, State: "open", Limit: 30, Page: 1})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "pull", items[0].Type)
	repoService := Issues{Client: client, Repository: "team-org/repo"}
	_, err = repoService.Search(context.Background(), DiscussionSearchOptions{Owner: "team-org", Team: "devs", State: "open", Limit: 30, Page: 1})
	require.ErrorContains(t, err, "instance-wide")
}

func TestDiscussionSearchScopeAndSelfFilters(t *testing.T) {
	for _, repository := range []string{"", "owner/repo"} {
		t.Run(repository, func(t *testing.T) {
			requests := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path == "/api/v1/user" {
					_, _ = w.Write([]byte(`{"login":"account"}`))
					return
				}
				require.Equal(t, "issues", r.URL.Query().Get("type"))
				require.Equal(t, "open", r.URL.Query().Get("state"))
				if repository == "" {
					require.Equal(t, "/api/v1/repos/issues/search", r.URL.Path)
					require.Equal(t, "true", r.URL.Query().Get("assigned"))
					require.Equal(t, "true", r.URL.Query().Get("created"))
				} else {
					require.Equal(t, "/api/v1/repos/owner/repo/issues", r.URL.Path)
					require.Equal(t, "account", r.URL.Query().Get("assigned_by"))
					require.Equal(t, "account", r.URL.Query().Get("created_by"))
				}
				_, _ = w.Write([]byte(`[{"id":1,"number":2,"title":"Result","html_url":"https://instance/owner/repo/issues/2","repository":{"full_name":"owner/repo"}}]`))
			}))
			service := Issues{Client: client, Repository: repository}
			items, err := service.Search(context.Background(), DiscussionSearchOptions{State: "open", Assigned: true, Authored: true, Limit: 30, Page: 1})
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Equal(t, "issue", items[0].Type)
			require.Equal(t, "owner/repo", items[0].Repository)
			if repository != "" {
				previous := requests
				_, err := service.Search(context.Background(), DiscussionSearchOptions{Pulls: true, ReviewRequested: true})
				require.ErrorContains(t, err, "instance-wide")
				require.Equal(t, previous, requests)
			}
		})
	}
}

func TestInstanceSearchNamesAnUnknownUser(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusBadRequest} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"user does not exist [uid: 0, name: ghost]"}`))
		}))
		_, err := Issues{Client: client}.Search(context.Background(), DiscussionSearchOptions{Author: "ghost", State: "open", Limit: 30, Page: 1})
		require.EqualError(t, err, `user "ghost" not found`, "HTTP %d", status)
	}
}
