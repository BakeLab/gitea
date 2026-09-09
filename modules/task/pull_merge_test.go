// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergeTestServer answers the merge POST with mergeStatus and the PR GET with
// prJSON, or a 404 if prJSON is empty.
func mergeTestServer(t *testing.T, prJSON string, mergeStatus int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
			w.WriteHeader(mergeStatus)
			// Gitea explains itself here; the SDK discards it.
			_, _ = w.Write([]byte(`{"message":"Please try again later"}`))
		case r.Method == http.MethodGet:
			if prJSON == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"pull request does not exist"}`))
				return
			}
			_, _ = w.Write([]byte(prJSON))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

func pullJSON(state string, merged, draft, mergeable bool) string {
	return fmt.Sprintf(
		`{"number":3,"state":%q,"merged":%t,"draft":%t,"mergeable":%t,"head":{"sha":"abc123"}}`,
		state, merged, draft, mergeable)
}

func TestPullMerge(t *testing.T) {
	tests := []struct {
		name        string
		pr          string
		mergeStatus int
		wantErr     string
	}{
		{
			name:        "success",
			pr:          pullJSON("open", false, false, true),
			mergeStatus: http.StatusOK,
		},
		{
			name:        "created is also success",
			pr:          pullJSON("open", false, false, true),
			mergeStatus: http.StatusCreated,
		},
		{
			// gitea/tea#1022: an open PR with conflicts was reported as
			// possibly not open.
			name:        "conflicting files",
			pr:          pullJSON("open", false, false, false),
			mergeStatus: http.StatusMethodNotAllowed,
			wantErr:     "failed to merge PR #3: it has conflicting files or is otherwise not mergeable",
		},
		{
			name:        "already merged",
			pr:          pullJSON("closed", true, false, false),
			mergeStatus: http.StatusMethodNotAllowed,
			wantErr:     "failed to merge PR #3: it has already been merged",
		},
		{
			name:        "closed",
			pr:          pullJSON("closed", false, false, false),
			mergeStatus: http.StatusMethodNotAllowed,
			wantErr:     "failed to merge PR #3: it is closed",
		},
		{
			name:        "draft",
			pr:          pullJSON("open", false, true, false),
			mergeStatus: http.StatusMethodNotAllowed,
			wantErr:     "failed to merge PR #3: it is a draft; mark it ready for review first",
		},
		{
			// Open and mergeable, yet refused.
			name:        "refused while mergeable",
			pr:          pullJSON("open", false, false, true),
			mergeStatus: http.StatusMethodNotAllowed,
			wantErr:     "failed to merge PR #3: the server refused the merge; check required status checks, requested reviews, or branch protection rules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mergeTestServer(t, tt.pr, tt.mergeStatus)
			defer server.Close()

			err := PullMerge(t.Context(), &config.Login{
				Name:         "test",
				URL:          server.URL,
				Token:        "secret-token",
				VersionCheck: false,
			}, "owner", "repo", 3, gitea.MergePullRequestOption{Style: gitea.MergeStyleMerge})

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

// A refusal must still explain itself when the follow-up PR lookup fails.
func TestPullMergeReasonUnavailable(t *testing.T) {
	server := mergeTestServer(t, "", http.StatusMethodNotAllowed)
	defer server.Close()

	err := PullMerge(t.Context(), &config.Login{
		Name:         "test",
		URL:          server.URL,
		Token:        "secret-token",
		VersionCheck: false,
	}, "owner", "repo", 3, gitea.MergePullRequestOption{
		Style: gitea.MergeStyleMerge,
		// Set so the SDK skips its own pre-merge PR lookup.
		HeadCommitId: "abc123",
	})

	require.Error(t, err)
	assert.Equal(t, "failed to merge PR #3: the server refused the merge; check required status checks, requested reviews, or branch protection rules", err.Error())
}
