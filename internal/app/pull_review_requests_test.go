// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestReviewRequestOutputUsesPullFields(t *testing.T) {
	requested := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/users/alice":
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/api/v1/repos/owner/repo/pulls/1/requested_reviewers":
			require.Equal(t, http.MethodPost, r.Method)
			requested = true
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/repos/owner/repo/pulls/1":
			require.Equal(t, http.MethodGet, r.Method)
			_, _ = w.Write([]byte(`{"number":1,"requested_reviewers":[{"login":"alice","email":"omitted@example.test"},{"login":"bob"}],"requested_reviewers_teams":[]}`))
		case "/api/v1/repos/owner/repo/pulls/1/reviews":
			// Gitea still lists bob, who reviewed after his request was withdrawn.
			if !requested {
				writeFirstPage(w, r, `[{"id":2,"state":"COMMENT","user":{"login":"bob"}}]`)
				return
			}
			writeFirstPage(w, r, `[{"id":1,"state":"REQUEST_REVIEW","user":{"login":"alice"}},{"id":2,"state":"COMMENT","user":{"login":"bob"}}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "request", "1", "-R", "owner/repo", "--reviewer=alice", "--json=number,reviewers,reviewer_teams"}))
	require.JSONEq(t, `{"number":1,"reviewers":["alice"],"reviewer_teams":[]}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestReviewRequestRefusesAnUnknownUserBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"pr", "review", "request", "1", "--reviewer=carol,nobody"},
		{"pr", "review", "cancel-request", "1", "--reviewer=nobody"},
		{"pr", "edit", "1", "--remove-reviewer=carol", "--add-reviewer=nobody"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method, "no review request may change")
				switch r.URL.Path {
				case "/api/v1/users/carol":
					_, _ = w.Write([]byte(`{"login":"carol"}`))
				case "/api/v1/repos/owner/repo/pulls/1/reviews":
					writeFirstPage(w, r, `[]`)
				case "/api/v1/users/nobody":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"user redirect does not exist [name: nobody]"}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append(args, "-R", "owner/repo"))
			require.EqualError(t, err, `user "nobody" not found`)
		})
	}
}

func TestReviewRequestWarnsAboutUnchangedRequests(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/users/carol", "/api/v1/users/dave":
			_, _ = w.Write([]byte(`{"login":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/users/") + `"}`))
		case "/api/v1/repos/owner/repo/pulls/1/requested_reviewers":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/repos/owner/repo/pulls/1":
			_, _ = w.Write([]byte(`{"number":1,"title":"Fix"}`))
		case "/api/v1/repos/owner/repo/pulls/1/reviews":
			writeFirstPage(w, r, `[{"id":1,"state":"REQUEST_REVIEW","user":{"login":"carol"}}]`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		verb, reviewers, want string
	}{
		{"request", "carol,dave", "! carol already has a review request on owner/repo#1 (Fix)\n✓ Requested review from dave on owner/repo#1 (Fix)\n"},
		{"cancel-request", "dave", "! dave has no review request on owner/repo#1 (Fix)\n"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			deps, _, diagnostics := serverDeps(t, server.URL)
			deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
			deps.Streams.OutTTY = true
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", tc.verb, "1", "-R", "owner/repo", "--reviewer", tc.reviewers}))
			require.Equal(t, tc.want, diagnostics.String())
		})
	}
}

func TestReviewRequestRefusesADuplicateOfMe(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s", r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "review", "request", "1", "-R", "owner/repo", "--reviewer", "@me,Alice"})
	require.EqualError(t, err, `reviewer "Alice" is duplicated or present in both add and remove`)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
}
