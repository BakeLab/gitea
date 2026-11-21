// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullCreateChoicesFetchRepositoryOnce(t *testing.T) {
	for _, push := range []bool{false, true} {
		t.Run(fmt.Sprintf("push=%t", push), func(t *testing.T) {
			var mu sync.Mutex
			calls := map[string]int{}
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls[r.URL.Path]++
				mu.Unlock()
				switch {
				case r.URL.Path == "/api/v1/orgs/alice":
					w.WriteHeader(http.StatusNotFound)
				case r.URL.Path == "/api/v1/repos/alice/project":
					_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","default_branch":"trunk","has_pull_requests":true,"permissions":{"push":%t}}`, push)
				case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
					assert.Equal(t, "trunk", r.URL.Query().Get("ref"))
					if r.URL.Path != "/api/v1/repos/alice/project/raw/.gitea/PULL_REQUEST_TEMPLATE.md" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					_, _ = w.Write([]byte("## Checklist\n"))
				case r.URL.Path == "/api/v1/repos/alice/project/assignees":
					_, _ = w.Write([]byte(`[{"login":"alice"}]`))
				case r.URL.Path == "/api/v1/repos/alice/project/milestones" && r.URL.Query().Get("page") == "1":
					_, _ = w.Write([]byte(`[{"id":1,"title":"v1"}]`))
				case r.URL.Path == "/api/v1/repos/alice/project/labels" && r.URL.Query().Get("page") == "1":
					_, _ = w.Write([]byte(`[{"id":2,"name":"bug"}]`))
				case r.URL.Path == "/api/v1/repos/alice/project/milestones", r.URL.Path == "/api/v1/repos/alice/project/labels":
					_, _ = w.Write([]byte(`[]`))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			choices, err := (Pulls{Client: client, Repository: "alice/project"}).CreateChoices(context.Background())
			require.NoError(t, err)
			require.Equal(t, "alice/project", choices.Repository.FullName)
			require.Equal(t, "trunk", choices.Repository.DefaultBranch)
			require.Equal(t, "## Checklist\n", choices.Template)
			want := CreateSelectables{Assignees: []string{}, Milestones: []Milestone{}, Labels: []Label{}}
			if push {
				want = CreateSelectables{
					CanPush:    true,
					Assignees:  []string{"alice"},
					Milestones: []Milestone{{ID: 1, Title: "v1", URL: client.BaseURL + "/alice/project/milestone/1"}},
					Labels:     []Label{{ID: 2, Name: "bug"}},
				}
			}
			require.Equal(t, want, choices.CreateSelectables)
			require.Equal(t, 1, calls["/api/v1/repos/alice/project"])
			if !push {
				require.Zero(t, calls["/api/v1/repos/alice/project/assignees"])
			}
		})
	}
}

func TestPullCreateChoicesLoadConcurrently(t *testing.T) {
	// The template, assignees, milestones, and labels requests only complete once all four are in flight.
	paths := []string{"raw/PULL_REQUEST_TEMPLATE.md", "assignees", "milestones", "labels"}
	arrived := map[string]chan struct{}{}
	for _, path := range paths {
		arrived[path] = make(chan struct{})
	}
	var once sync.Map
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/")
		if r.URL.Path == "/api/v1/orgs/alice" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main","has_pull_requests":true,"permissions":{"push":true}}`))
			return
		}
		if ch, ok := arrived[path]; ok {
			if _, loaded := once.LoadOrStore(path, true); !loaded {
				close(ch)
			}
			for _, other := range paths {
				select {
				case <-arrived[other]:
				case <-time.After(5 * time.Second):
					t.Errorf("%s did not run concurrently with %s", other, path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
		}
		if strings.HasPrefix(path, "raw/") {
			_, _ = w.Write([]byte("template"))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	choices, err := (Pulls{Client: client, Repository: "alice/project"}).CreateChoices(context.Background())
	require.NoError(t, err)
	require.Equal(t, "template", choices.Template)
}

func TestPullCreateChoicesReportFailures(t *testing.T) {
	for _, failing := range []string{"/api/v1/repos/alice/project", "/api/v1/repos/alice/project/labels", "/api/v1/repos/alice/project/raw/PULL_REQUEST_TEMPLATE.md"} {
		t.Run(failing, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == failing:
					w.WriteHeader(http.StatusInternalServerError)
				case r.URL.Path == "/api/v1/repos/alice/project":
					_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main","has_pull_requests":true,"permissions":{"push":true}}`))
				default:
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			_, err := (Pulls{Client: client, Repository: "alice/project"}).CreateChoices(context.Background())
			require.ErrorContains(t, err, "500")
		})
	}
}
