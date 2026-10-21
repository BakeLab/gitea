// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeadlineInputAndExplicitClearing(t *testing.T) {
	for _, family := range []string{"issue", "pr"} {
		for _, tc := range []struct{ value, payload string }{
			{"2028-01-15", `{"due_date":"2028-01-15T23:59:59Z"}`},
			{"2028-01-15T00:30:00+02:00", `{"due_date":"2028-01-15T23:59:59Z"}`},
			{"2028-01-15T23:30:00-05:00", `{"due_date":"2028-01-15T23:59:59Z"}`},
			{"Jan 15 2028", `{"due_date":"2028-01-15T23:59:59Z"}`},
			{"1704067200000", `{"due_date":"2024-01-01T23:59:59Z"}`},
			{"1704067200", `{"due_date":"2024-01-01T23:59:59Z"}`},
			{"", `{"unset_due_date":true}`},
		} {
			t.Run(family+"/"+tc.value, func(t *testing.T) {
				// Gitea answers with the due date it stored.
				var payload map[string]any
				require.NoError(t, json.Unmarshal([]byte(tc.payload), &payload))
				answer, err := json.Marshal(map[string]any{"number": 1, "due_date": payload["due_date"]})
				require.NoError(t, err)
				stored := string(answer)
				server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, http.MethodPatch, r.Method)
					collection := "issues"
					if family == "pr" {
						collection = "pulls"
					}
					require.Equal(t, "/api/v1/repos/owner/repo/"+collection+"/1", r.URL.Path)
					requirePayload(t, tc.payload, r.Body)
					_, _ = w.Write([]byte(stored))
				}))
				defer server.Close()
				deps, out, _ := serverDeps(t, server.URL)
				require.NoError(t, Execute(context.Background(), deps, []string{family, "edit", "1", "-R", "owner/repo", "--deadline=" + tc.value, "--json=number,due_date"}))
				require.JSONEq(t, stored, out.String())
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
		for _, command := range [][]string{{"issue", "edit", "1"}, {"pr", "edit", "1", "--add-reviewer=alice"}, {"issue", "create"}, {"pr", "create"}} {
			requireUsageBeforeRequests(t, "invalid --deadline", append(command, "--deadline="+value)...)
		}
	}
}

func TestDateOnlyDeadlineIsTheEndOfTheDayOnCreate(t *testing.T) {
	sent := map[string]any{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeFirstPage(w, r, `[]`)
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		maps.Copy(sent, body)
		_, _ = w.Write([]byte(`{"id":1,"number":1,"due_date":"2028-01-15T23:59:59Z"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "owner/repo", "--title", "x", "--deadline", "2028-01-15", "--json=number"}))
	require.Equal(t, "2028-01-15T23:59:59Z", sent["due_date"])
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "owner/repo", "--title", "x", "--deadline", "2028-01-15", "--json=id"}))
	require.Equal(t, "2028-01-15T23:59:59Z", sent["due_on"])
	// Only the date counts, as edits keep only the date too.
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "owner/repo", "--title", "x", "--deadline", "2028-01-15T10:00:00+02:00", "--json=number"}))
	require.Equal(t, "2028-01-15T23:59:59Z", sent["due_date"])
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "owner/repo", "--title", "x", "--deadline", "2028-01-15T10:00:00+02:00", "--json=id"}))
	require.Equal(t, "2028-01-15T23:59:59Z", sent["due_on"])
}

func TestEmptyDeadlineIsRejectedOnCreate(t *testing.T) {
	for _, command := range [][]string{{"issue", "create", "--title", "x"}, {"pr", "create", "--title", "x"}} {
		requireUsageBeforeRequests(t, "--deadline cannot be empty", append(command, "--deadline=")...)
	}
}

func TestDeadlineHelpSaysOnlyTheDateCounts(t *testing.T) {
	deps, _, _ := testDeps(t)
	root := NewRoot(deps)
	const usage = "Due `date`: YYYY-MM-DD, RFC3339 or Unix timestamp; only the date counts, as the end of that day in UTC"
	for _, tc := range []struct {
		path     []string
		clearing string
	}{
		{[]string{"issue", "create"}, ""},
		{[]string{"pr", "create"}, ""},
		{[]string{"milestone", "create"}, "; with --force, empty clears the deadline of an existing milestone"},
		{[]string{"issue", "edit"}, "; empty clears it"},
		{[]string{"pr", "edit"}, "; empty clears it"},
		{[]string{"milestone", "edit"}, "; empty clears it"},
	} {
		cmd, _, err := root.Find(tc.path)
		require.NoError(t, err)
		require.Equal(t, usage+tc.clearing, cmd.Flags().Lookup("deadline").Usage, tc.path)
	}
}
