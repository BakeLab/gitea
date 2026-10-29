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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryAssigneesParseLogins(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/assignees", r.URL.Path)
		_, _ = w.Write([]byte(`[{"login":"alice","email":"hidden@example.test"},{"login":"bob"}]`))
	}))
	names, err := (Repositories{Client: client}).assignees(context.Background(), "alice/project")
	require.NoError(t, err)
	require.Equal(t, []string{"alice", "bob"}, names)
}

func TestIssueTemplatesParseMarkdownAndForm(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/issue_templates", r.URL.Path)
		_, _ = w.Write([]byte(`[
			{"name":"Bug","about":"Report a bug","title":"bug: ","content":"## Steps\n","body":null},
			{"name":"Form","about":"Structured","title":"form: ","content":"","body":[{"type":"markdown","attributes":{"value":"Hi"}}]}
		]`))
	}))
	templates, err := (Issues{Client: client, Repository: "alice/project"}).IssueTemplates(context.Background())
	require.NoError(t, err)
	require.Equal(t, []IssueTemplate{
		{Name: "Bug", About: "Report a bug", Title: "bug: ", Content: "## Steps\n", Form: false},
		{Name: "Form", About: "Structured", Title: "form: ", Content: "", Form: true},
	}, templates)
}

func TestIssueTemplatesNullBecomesEmpty(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`null`))
	}))
	templates, err := (Issues{Client: client, Repository: "alice/project"}).IssueTemplates(context.Background())
	require.NoError(t, err)
	require.Empty(t, templates)
	require.NotNil(t, templates)
}

func TestIssueSelectablesLoadMetadataWithoutPush(t *testing.T) {
	pages := map[string]map[string]string{
		"/api/v1/repos/alice/project/milestones": {"1": `[{"id":1,"title":"v1"}]`, "2": `[{"id":2,"title":"v2"}]`},
		"/api/v1/repos/alice/project/labels":     {"1": `[{"id":3,"name":"bug"}]`, "2": `[{"id":4,"name":"docs"}]`},
	}
	lists := CreateSelectables{
		Assignees:  []string{"alice", "bob"},
		Milestones: []Milestone{{ID: 1, Title: "v1"}, {ID: 2, Title: "v2"}},
		Labels:     []Label{{ID: 3, Name: "bug"}, {ID: 4, Name: "docs"}},
	}
	for _, push := range []bool{false, true} {
		t.Run(fmt.Sprintf("push=%t", push), func(t *testing.T) {
			var mu sync.Mutex
			calls := map[string]int{}
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls[r.URL.Path]++
				mu.Unlock()
				switch r.URL.Path {
				case "/api/v1/repos/alice/project":
					_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","permissions":{"push":%t,"pull":true}}`, push)
				case "/api/v1/orgs/alice":
					w.WriteHeader(http.StatusNotFound)
				case "/api/v1/repos/alice/project/issue_templates":
					_, _ = w.Write([]byte(`[{"name":"Bug","title":"t","content":"c"}]`))
				case "/api/v1/repos/alice/project/assignees":
					_, _ = w.Write([]byte(`[{"login":"alice"},{"login":"bob"}]`))
				case "/api/v1/repos/alice/project/milestones", "/api/v1/repos/alice/project/labels":
					if strings.HasSuffix(r.URL.Path, "/milestones") {
						assert.Equal(t, "open", r.URL.Query().Get("state"))
					}
					body, ok := pages[r.URL.Path][r.URL.Query().Get("page")]
					if !ok {
						body = `[]`
					}
					_, _ = w.Write([]byte(body))
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			service := Issues{Client: client, Repository: "alice/project"}
			// People who can write to the issues set metadata without push access, which the
			// permissions of the repository cannot tell, so create and edit offer it to everyone.
			loaded, err := service.CreateSelectables(context.Background())
			require.NoError(t, err)
			require.Len(t, loaded.Templates, 1)
			want := lists
			want.Milestones = []Milestone{{ID: 1, Title: "v1", URL: client.BaseURL + "/alice/project/milestone/1"}, {ID: 2, Title: "v2", URL: client.BaseURL + "/alice/project/milestone/2"}}
			require.Equal(t, want, loaded.CreateSelectables)
			require.Zero(t, calls["/api/v1/repos/alice/project"])
			require.Equal(t, 1, calls["/api/v1/repos/alice/project/issue_templates"])

			clear(calls)
			edit, err := service.EditSelectables(context.Background())
			require.NoError(t, err)
			require.Equal(t, want, edit)
			require.Zero(t, calls["/api/v1/repos/alice/project"])
			require.Zero(t, calls["/api/v1/repos/alice/project/issue_templates"])
		})
	}
}

func TestEditSelectablesIncludeOrganizationLabels(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/acme/service":
			_, _ = w.Write([]byte(`{"full_name":"acme/service","permissions":{"push":true,"pull":true}}`))
		case "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":5,"name":"acme"}`))
		case "/api/v1/repos/acme/service/assignees", "/api/v1/repos/acme/service/milestones":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/acme/service/labels":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/orgs/acme/labels":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":9,"name":"needs-triage"}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	result, err := (Issues{Client: client, Repository: "acme/service"}).EditSelectables(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Label{{ID: 9, Name: "needs-triage"}}, result.Labels)
}
