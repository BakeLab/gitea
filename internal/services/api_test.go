// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestAPIPaginationRejectsUnsafeAndRepeatingLinks(t *testing.T) {
	foreignCalls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignCalls++ }))
	defer foreign.Close()
	for _, next := range []string{foreign.URL + "/api/v1/user?secret=hidden", "/api/v1/items", "/unrelated/items", "/api/v1/../user"} {
		t.Run(next, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "token secret", r.Header.Get("Authorization"))
				w.Header().Set("Link", "<"+next+">; rel=next")
				_, _ = w.Write([]byte(`[]`))
			}))
			defer server.Close()
			err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "items", Method: "GET", Paginate: true}, func(response *http.Response) error { _, err := io.Copy(io.Discard, response.Body); return err })
			require.Error(t, err)
			require.NotContains(t, err.Error(), "hidden")
			require.Equal(t, 1, calls)
		})
	}
	require.Zero(t, foreignCalls)
	links, err := nextPageLinks([]string{`<https://example.org/items?a=1,2>; title="one,two"; rel="prev next", </last>; rel=last`})
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.org/items?a=1,2"}, links)
}

func TestAPIPaginationFollowsLinksNamingTheInstanceDifferently(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.Host+r.URL.RequestURI())
		if r.URL.Query().Get("page") != "2" {
			// Gitea builds Link headers from its ROOT_URL rather than from the request host.
			w.Header().Set("Link", `<https://git.example.test/api/v1/items?limit=1&page=2>; rel="next"`)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	visit := func(response *http.Response) error { _, err := io.Copy(io.Discard, response.Body); return err }
	err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "items", Method: "GET", Paginate: true}, visit)
	require.NoError(t, err)
	require.Len(t, pages, 2)
	require.Contains(t, pages[1], "/api/v1/items?limit=1&page=2")
	require.Contains(t, pages[1], server.Listener.Addr().String(), "the second page still goes to the configured host")

	err = (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "other", Method: "GET", Paginate: true}, visit)
	require.ErrorContains(t, err, "git.example.test")
}

func TestAPIVisitReturnsTheServerMessageOfAFailedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid color: #nothex"}`))
	}))
	defer server.Close()
	var body []byte
	err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "labels", Method: "POST"}, func(response *http.Response) error {
		var err error
		body, err = io.ReadAll(response.Body)
		return err
	})
	var apiErr *transport.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "invalid color: #nothex", apiErr.Message)
	require.Contains(t, string(body), "nothex", "the visitor still sees the whole body")
}
