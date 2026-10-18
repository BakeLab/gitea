// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeadlineInputAndExplicitClearing(t *testing.T) {
	for _, family := range []string{"issue", "pr"} {
		for _, tc := range []struct{ value, payload string }{
			{"2028-01-15", `{"due_date":"2028-01-15T00:00:00Z"}`},
			{"2028-01-15T00:30:00+02:00", `{"due_date":"2028-01-15T00:30:00+02:00"}`},
			{"Jan 15 2028", `{"due_date":"2028-01-15T00:00:00Z"}`},
			{"1704067200000", `{"due_date":"2024-01-01T00:00:00Z"}`},
			{"1704067200", `{"due_date":"2024-01-01T00:00:00Z"}`},
			{"", `{"unset_due_date":true}`},
		} {
			t.Run(family+"/"+tc.value, func(t *testing.T) {
				server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, http.MethodPatch, r.Method)
					collection := "issues"
					if family == "pr" {
						collection = "pulls"
					}
					require.Equal(t, "/api/v1/repos/owner/repo/"+collection+"/1", r.URL.Path)
					requirePayload(t, tc.payload, r.Body)
					_, _ = w.Write([]byte(`{"number":1,"due_date":null}`))
				}))
				defer server.Close()
				deps, out, _ := serverDeps(t, server.URL)
				require.NoError(t, Execute(context.Background(), deps, []string{family, "edit", "1", "-R", "owner/repo", "--deadline=" + tc.value, "--json=number,due_date"}))
				require.JSONEq(t, `{"number":1,"due_date":null}`, out.String())
			})
		}
	}
}

func TestAmbiguousNumericDeadlinesAreRejected(t *testing.T) {
	for _, value := range []string{"03/04/2026", "04/03/2026", "01/15/2028", "15.01.2028", "3/4/26", "04/03/2026 17:00", "4/3/2026 17:00:00", "04/03/2026 5:00 PM"} {
		for _, command := range [][]string{{"issue", "edit", "1"}, {"issue", "create", "--title", "x"}, {"milestone", "create", "--title", "x"}} {
			requireUsageBeforeRequests(t, fmt.Sprintf("invalid --deadline %q: the order of day and month is ambiguous; use YYYY-MM-DD", value), append(command, "--deadline="+value)...)
		}
	}
}

func TestInvalidDeadlineBeforeInputCollectionOrNetwork(t *testing.T) {
	for _, value := range []string{"bad", "2026-02-30", "0", "not-a-timestamp", "999999999999999999999999999999999999"} {
		for _, command := range [][]string{{"issue", "edit", "1"}, {"pr", "edit", "1", "--add-reviewers=alice"}, {"issue", "create"}, {"pr", "create"}} {
			requireUsageBeforeRequests(t, "invalid --deadline", append(command, "--deadline="+value)...)
		}
	}
}
