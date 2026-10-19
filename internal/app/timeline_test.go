// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTimelineListsWhatHappenedOldestFirst(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash"}`))
		case "/api/v1/repos/alice/project/issues/3/timeline":
			writeFirstPage(w, r, `[
				{"id":1,"type":"label","body":"1","label":{"name":"bug"},"user":{"login":"bob"},"created_at":"2026-01-01T00:00:00Z"},
				{"id":2,"type":"change_title","old_title":"crash","new_title":"Crash","user":{"login":"bob"},"created_at":"2026-01-02T00:00:00Z"},
				{"id":3,"type":"comment","body":"Fixed in main\nThanks","user":{"login":"alice"},"created_at":"2026-01-03T00:00:00Z"},
				{"id":4,"type":"close","user":{"login":"alice"},"created_at":"2026-01-03T00:00:00Z"}]`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project"}))
	require.Equal(t, "2026-01-01T00:00:00Z\tbob\tadded label \"bug\"\n2026-01-02T00:00:00Z\tbob\trenamed \"crash\" to \"Crash\"\n2026-01-03T00:00:00Z\talice\tcommented: Fixed in main\n2026-01-03T00:00:00Z\talice\tclosed\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "timeline", "3", "-R", "alice/project", "--json", "type,label", "--jq", ".[0]"}))
	require.JSONEq(t, `{"type":"label","label":"bug"}`, out.String())
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"pr", "timeline", "3", "-R", "alice/project"}), "is an issue, not a pull request")
}
