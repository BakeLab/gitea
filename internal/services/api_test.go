// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/tea/internal/transport"
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
