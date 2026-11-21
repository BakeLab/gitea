// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTimelinePagesTellAFullOrCappedPageFromTheLastOne(t *testing.T) {
	for _, tc := range []struct{ total, maxItems, limit, page int }{
		{40, 50, 10, 1},
		{40, 50, 10, 3},
		{40, 50, 10, 4},
		{40, 50, 10, 5},
		{40, 50, 30, 2},
		{120, 50, 100, 1},
		{120, 50, 100, 2},
		{60, 50, 100, 1},
		{50, 50, 100, 1},
		{100, 50, 70, 2},
	} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			// Like Gitea, the timeline sends no Link header and counts only the returned page.
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/settings/api" {
					_, _ = fmt.Fprintf(w, `{"max_response_items":%d}`, tc.maxItems)
					return
				}
				require.Equal(t, "/api/v1/repos/o/r/issues/3/timeline", r.URL.Path)
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				size := min(limit, tc.maxItems)
				events := []string{}
				for id := (page-1)*size + 1; id <= min(page*size, tc.total); id++ {
					events = append(events, fmt.Sprintf(`{"id":%d,"type":"comment"}`, id))
				}
				w.Header().Set("X-Total-Count", strconv.Itoa(len(events)))
				_, _ = w.Write([]byte("[" + strings.Join(events, ",") + "]"))
			}))
			events, err := (Issues{Client: client, Repository: "o/r"}).Timeline(context.Background(), 3, tc.limit, tc.page)
			require.NoError(t, err)
			want, got := []int64{}, []int64{}
			for id := (tc.page-1)*tc.limit + 1; id <= min(tc.page*tc.limit, tc.total); id++ {
				want = append(want, int64(id))
			}
			for _, event := range events.Items {
				got = append(got, event.ID)
			}
			require.Equal(t, want, got)
			require.Equal(t, tc.total > tc.page*tc.limit, events.More)
			require.False(t, events.Counted)
		})
	}
}
