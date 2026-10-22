// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackedTimeFromFallsBackToSelectedRepository(t *testing.T) {
	require.Equal(t, TrackedTime{ID: 1, Seconds: 30, Repository: "alice/project"},
		trackedTimeFrom(decode[gitea.TrackedTime](t, `{"id":1,"time":30,"issue":null}`), "alice/project"))
	require.Equal(t, TrackedTime{ID: 2, CreatedAt: "2026-10-01T12:00:00Z", Repository: "alice/project", IssueNumber: 4},
		trackedTimeFrom(decode[gitea.TrackedTime](t, `{"id":2,"created":"2026-10-01T12:00:00Z","issue":{"number":4,"repository":null}}`), "alice/project"))
}

func TestTimeListSelectsScopeAndFilters(t *testing.T) {
	since := time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	before := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for name, test := range map[string]struct {
		opts      TimeOptions
		wantPath  string
		wantQuery url.Values
	}{
		"repository": {
			opts:      TimeOptions{Limit: 30, Page: 1},
			wantPath:  "/api/v1/repos/alice/project/times",
			wantQuery: url.Values{"limit": {"30"}, "page": {"1"}},
		},
		"issue with filters": {
			opts:      TimeOptions{Issue: 7, Username: "bob", Since: &since, Before: &before, Limit: 10, Page: 2},
			wantPath:  "/api/v1/repos/alice/project/issues/7/times",
			wantQuery: url.Values{"limit": {"10"}, "page": {"2"}, "user": {"bob"}, "since": {"2026-10-01T08:00:00+02:00"}, "before": {"2026-10-02T00:00:00Z"}},
		},
		"mine wins over issue": {
			opts:      TimeOptions{Mine: true, Issue: 7, Limit: 30, Page: 1},
			wantPath:  "/api/v1/user/times",
			wantQuery: url.Values{"limit": {"30"}, "page": {"1"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, test.wantPath, r.URL.Path)
				assert.Equal(t, test.wantQuery, r.URL.Query())
				_, _ = w.Write([]byte(`[
					{"id":1,"time":60,"user_name":"bob","issue":{"number":7,"html_url":"https://example.test/7"}},
					{"id":2,"time":30,"issue":{"number":3,"repository":{"full_name":"carol/other"}}}
				]`))
			}))
			page, err := (Times{Client: client, Repository: "alice/project"}).ListPage(context.Background(), test.opts)
			require.NoError(t, err)
			items := page.Items
			require.Equal(t, []TrackedTime{
				{ID: 1, Seconds: 60, User: "bob", Repository: "alice/project", IssueNumber: 7, IssueURL: "https://example.test/7"},
				{ID: 2, Seconds: 30, Repository: "carol/other", IssueNumber: 3},
			}, items)
		})
	}
}
