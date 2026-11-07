// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestIssueCreateSeedsFromTemplate(t *testing.T) {
	var created []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issue_templates":
			_, _ = w.Write([]byte(`[{"name":"Bug Report","title":"[bug] Crash","content":"Steps:","labels":["bug","gone"],"ref":"main","file_name":".gitea/ISSUE_TEMPLATE/bug.md"},{"name":"Feature","about":"Describe the idea","ref":"refs/heads/","body":[{"type":"textarea"}]}]`))
		case "/api/v1/orgs/alice":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/repos/alice/project/labels":
			writeFirstPage(w, r, `[{"id":2,"name":"bug"},{"id":3,"name":"docs"}]`)
		case "/api/v1/repos/alice/project/issues":
			require.Equal(t, http.MethodPost, r.Method)
			payload, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			created = append(created, string(payload))
			_, _ = w.Write([]byte(`{"number":1,"labels":[{"id":2},{"id":3}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	editor := &draftEditor{draft: terminal.Draft{Body: "edited idea"}}
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append([]string{"issue", "create", "-R", "alice/project", "--no-input", "--json=number"}, args...))
	}
	// The title of a template is a prefix to complete, which never becomes the title without the form.
	err := run("--template", "bug report")
	require.ErrorContains(t, err, "issue create requires --title")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Empty(t, created)
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--no-input", "--template", "bug report", "--web"}))
	require.Equal(t, server.URL+"/alice/project/issues/new?template=.gitea%2FISSUE_TEMPLATE%2Fbug.md", browser.resource, "the page applies the whole template")
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--no-input", "--template", "bug report", "--web", "--title", "Mine"}))
	require.Equal(t, server.URL+"/alice/project/issues/new?template=.gitea%2FISSUE_TEMPLATE%2Fbug.md&title=Mine", browser.resource)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--no-input", "--template", "feature", "--web"}))
	require.Equal(t, server.URL+"/alice/project/issues/new?body=&title=", browser.resource, "a template without a file name prefills what it has")
	require.NoError(t, run("--template", "bug report", "--title", "[bug] Crash"))
	require.Equal(t, "! label \"gone\" of the issue template does not exist in alice/project; it was left out\n", diagnostics.String())
	require.NoError(t, run("--template", "Bug Report", "--title", "Explicit", "--body", "Override", "--label", "docs", "--ref", "v1"))
	require.NoError(t, run("--template", "feature", "--title", "Idea", "--editor"))
	require.Empty(t, editor.initial, "a form template seeds no body")
	err = run("--template", "missing", "--title", "x")
	require.ErrorContains(t, err, `issue template "missing" does not exist; available: Bug Report, Feature`)
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	err = run("--template", "feature")
	require.ErrorContains(t, err, "issue create requires --title")
	deps.Streams.In = strings.NewReader("From stdin")
	require.NoError(t, run("--template", "bug report", "--title", "Crash", "--body-file", "-"))
	err = run("--template", "feature", "--body-file", "-")
	require.ErrorContains(t, err, "--body-file - requires --title")
	require.Len(t, created, 4)
	for i, want := range []string{
		`{"title":"[bug] Crash","body":"Steps:","labels":[2],"ref":"main"}`,
		`{"title":"Explicit","body":"Override","labels":[3],"ref":"v1"}`,
		`{"title":"Idea","body":"edited idea"}`,
		`{"title":"Crash","body":"From stdin","labels":[2],"ref":"main"}`,
	} {
		requirePayload(t, want, strings.NewReader(created[i]))
	}
}

func TestIssueCreateTemplateOpensFormWithTemplateBody(t *testing.T) {
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
		_, _ = w.Write([]byte(`{"number":9,"title":"Mine"}`))
	}))
	defer server.Close()
	form := &recordingIssueForm{values: terminal.IssueFormValues{Title: "Mine", Body: "## Steps"}}
	deps, _, _ := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--json=number", "--interactive", "--template", "bug"}))
	require.Equal(t, 1, form.calls)
	require.Equal(t, &terminal.IssueTemplateChoice{Name: "Bug", About: "Report", Title: "bug: ", Body: "## Steps"}, form.choices.Template, "the form starts with the template title and body")
	require.Equal(t, "Mine", createPayload["title"])
}

func TestIssueCreateFormAddsLabelsOfPickedTemplate(t *testing.T) {
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
		_, _ = w.Write([]byte(`{"number":9,"title":"Mine","labels":[{"id":3},{"id":2}]}`))
	}))
	defer server.Close()
	form := &recordingIssueForm{values: terminal.IssueFormValues{Title: "Mine", Labels: []int64{3}, TemplateLabels: []string{"bug", "gone"}, TemplateRef: "main"}}
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--json=number", "--interactive"}))
	require.Equal(t, []any{float64(3), float64(2)}, createPayload["labels"])
	require.Equal(t, "main", createPayload["ref"])
	require.Equal(t, "! label \"gone\" of the issue template does not exist in alice/project; it was left out\n", withoutANSI(diagnostics.String()))
}

func TestIssueCreateFormPrefersRepositoryLabelsOfTemplateAndKeepsTheBody(t *testing.T) {
	var payloads []map[string]any
	fail := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/acme/project/assignees", "/api/v1/repos/acme/project/issue_templates":
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/acme/project/milestones":
			writeFirstPage(w, r, `[]`)
		case "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":5,"username":"acme"}`))
		case "/api/v1/orgs/acme/labels":
			writeFirstPage(w, r, `[{"id":7,"name":"bug"}]`)
		case "/api/v1/repos/acme/project/labels":
			writeFirstPage(w, r, `[{"id":2,"name":"bug"},{"id":10,"name":"Dup"},{"id":11,"name":"dup"}]`)
		case "/api/v1/repos/acme/project/issues":
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			payloads = append(payloads, payload)
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
				return
			}
			_, _ = w.Write([]byte(`{"number":9,"title":"Mine","labels":[{"id":2}]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	form := &recordingIssueForm{values: terminal.IssueFormValues{Title: "Mine", Body: "typed in the form", TemplateLabels: []string{"bug", "DUP"}}}
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	run := func() error {
		return Execute(context.Background(), deps, []string{"issue", "create", "-R", "acme/project", "--json=number", "--interactive"})
	}
	require.NoError(t, run())
	require.Equal(t, []any{float64(2)}, payloads[0]["labels"], "the repository's label wins over the organization's")
	require.Equal(t, "! label \"DUP\" of the issue template matches several labels in acme/project; it was left out\n", withoutANSI(diagnostics.String()))
	fail = true
	err := run()
	require.ErrorContains(t, err, "boom")
	path := DraftPath(err)
	require.NotEmpty(t, path, "the typed body is kept when creating fails")
	t.Cleanup(func() { _ = os.Remove(path) })
	saved, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "typed in the form", string(saved))
}

func TestIssueCreateUploadsAttachmentsAndKeepsIssueOnFailure(t *testing.T) {
	var requests, uploaded []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues":
			_, _ = w.Write([]byte(`{"number":5,"title":"Crash"}`))
		case "/api/v1/repos/alice/project/issues/5/assets":
			file, header, err := r.FormFile("attachment")
			require.NoError(t, err)
			content, err := io.ReadAll(file)
			require.NoError(t, err)
			uploaded = append(uploaded, header.Filename+"="+string(content))
			if header.Filename == "second.txt" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": header.Filename})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.txt"), filepath.Join(dir, "second.txt")
	require.NoError(t, os.WriteFile(first, []byte("one"), 0o600))
	args := []string{"issue", "create", "-R", "alice/project", "--title", "Crash", "--attachment", first, "--attachment", second, "--json=number,title"}
	require.Error(t, Execute(context.Background(), deps, args))
	require.Empty(t, requests)
	require.NoError(t, os.WriteFile(second, []byte("two"), 0o600))
	err := Execute(context.Background(), deps, args)
	require.ErrorContains(t, err, "issue #5 was created, but follow-up steps failed: attachment second.txt")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.JSONEq(t, `{"number":5,"title":"Crash"}`, out.String())
	require.Equal(t, []string{"first.txt=one", "second.txt=two"}, uploaded)
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/issues", "POST /api/v1/repos/alice/project/issues/5/assets", "POST /api/v1/repos/alice/project/issues/5/assets"}, requests)
}

type recordingIssueForm struct {
	calls   int
	values  terminal.IssueFormValues
	choices terminal.IssueFormChoices
}

func (f *recordingIssueForm) CreateIssue(_ context.Context, choices terminal.IssueFormChoices) (terminal.IssueFormValues, error) {
	f.calls++
	f.choices = choices
	return f.values, nil
}

func TestIssueCreateImplicitInteractiveForm(t *testing.T) {
	deadline := time.Date(2028, 1, 15, 0, 0, 0, 0, time.UTC)
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/alice/project/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
		_, _ = w.Write([]byte(`{"number":9,"title":"Interactive","html_url":"https://example.org/alice/project/issues/9","state":"open",` +
			`"assignees":[{"login":"alice"}],"milestone":{"id":4},"labels":[{"id":2}],"due_date":"2028-01-15T23:59:59Z"}`))
	}))
	defer server.Close()
	form := &recordingIssueForm{values: terminal.IssueFormValues{
		Title: "Interactive", Body: "Body text", Assignees: []string{"alice"}, Milestone: 4, Labels: []int64{2}, Deadline: &deadline,
	}}
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--json=number,url", "--interactive"}))
	require.Equal(t, 1, form.calls)
	require.Nil(t, form.choices.EditBody, "without an editor the form asks for the body")
	require.Equal(t, []string{"alice", "bob"}, form.choices.Assignees)
	require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, form.choices.Milestones)
	require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, form.choices.Labels)
	require.Equal(t, []terminal.IssueTemplateChoice{{Name: "Bug", About: "Report", Title: "bug: ", Body: "## Steps"}}, form.choices.Templates)
	require.Empty(t, prompt.confirmations)
	require.Equal(t, "Interactive", createPayload["title"])
	require.Equal(t, "Body text", createPayload["body"])
	require.Equal(t, []any{"alice"}, createPayload["assignees"])
	require.InDelta(t, float64(4), createPayload["milestone"], 0)
	require.Equal(t, []any{float64(2)}, createPayload["labels"])
	require.Equal(t, "2028-01-15T23:59:59Z", createPayload["due_date"])
	require.JSONEq(t, `{"number":9,"url":"https://example.org/alice/project/issues/9"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestIssueCreateFormOffersMetadataWithoutPushAndNamesWhatGiteaLeftOut(t *testing.T) {
	var created []map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/alice/project/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		created = append(created, payload)
		if len(created) == 1 {
			// Someone who can write to the issues without pushing gets the metadata.
			_, _ = w.Write([]byte(`{"number":9,"labels":[{"id":2},{"id":3}],"milestone":{"id":4},"assignees":[{"login":"alice"}]}`))
			return
		}
		// Gitea drops the metadata of people who cannot write to the issues.
		_, _ = w.Write([]byte(`{"number":10}`))
	}))
	defer server.Close()
	form := &recordingIssueForm{values: terminal.IssueFormValues{
		Title: "Triaged", Assignees: []string{"alice"}, Milestone: 4, Labels: []int64{2}, TemplateLabels: []string{"docs"},
	}}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	run := func() error {
		return Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--json=number", "--interactive"})
	}
	require.NoError(t, run())
	require.Equal(t, []string{"alice", "bob"}, form.choices.Assignees, "the form offers metadata without push access")
	require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, form.choices.Milestones)
	require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, form.choices.Labels)
	require.Equal(t, []any{float64(2), float64(3)}, created[0]["labels"], "with the labels of the template")
	require.Equal(t, []any{"alice"}, created[0]["assignees"])
	require.InDelta(t, float64(4), created[0]["milestone"], 0)
	require.Empty(t, diagnostics.String())

	out.Reset()
	err := run()
	require.EqualError(t, err, "issue #10 was created without labels, milestone, assignees: Gitea leaves them out for people who cannot write to the issues of alice/project")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.JSONEq(t, `{"number":10}`, out.String(), "the created issue is still reported")
	require.Equal(t, "! Gitea left out the labels of the issue template, as you cannot write to the issues of alice/project\n", withoutANSI(diagnostics.String()))
}

func TestIssueCreateInteractiveDraftSurvivesSubmissionFailure(t *testing.T) {
	server := newAuthenticatedServer(selectablesHandler(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/alice/project/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, path := newDraftEditor(t, "saved body")
	form := &recordingIssueForm{values: terminal.IssueFormValues{
		Title: "Interactive", Body: "saved body", Draft: terminal.Draft{Path: path, Body: "saved body"},
	}}
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	deps.IssueForm = form
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	err := Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--interactive"})
	require.ErrorContains(t, err, "draft saved at "+path)
	require.NotNil(t, form.choices.EditBody)
	requireDraft(t, path, true)
	require.Empty(t, out.String())
}

func TestIssueCreateSkipsInteractiveFormWithoutTTY(t *testing.T) {
	form := &recordingIssueForm{}
	deps, _, _ := testDeps(t)
	deps.IssueForm = form
	deps.Streams.InTTY, deps.Streams.ErrTTY = false, false
	err := Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--no-input"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "issue create requires --title")
	require.Zero(t, form.calls)
}

func TestIssueCreationMetadataAndReferenceClearing(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo":
			_, _ = w.Write([]byte(`{"full_name":"owner/repo","permissions":{"push":true}}`))
			return
		case "/api/v1/repos/owner/repo/milestones":
			writeFirstPage(w, r, `[{"id":4,"title":"Release"}]`)
			return
		}
		calls++
		if calls == 1 {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/v1/repos/owner/repo/issues", r.URL.Path)
			requirePayload(t, `{"title":"Title","ref":"release/v1","assignees":["alice","bob"],"milestone":4}`, r.Body)
			_, _ = w.Write([]byte(`{"number":1,"ref":"release/v1","assignees":[{"login":"alice","email":"excluded@example.test"},{"login":"bob"}],"labels":[{"id":2,"name":"bug","color":"ff0000","description":"Defect","exclusive":true}],"milestone":{"id":4,"title":"Release","description":"excluded"}}`))
			return
		}
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/repos/owner/repo/issues/1", r.URL.Path)
		requirePayload(t, `{"ref":""}`, r.Body)
		_, _ = w.Write([]byte(`{"number":1,"ref":"","assignees":null,"milestone":null}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	fields := "--json=number,ref,assignees,milestone,labels"
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "owner/repo", "--title", "Title", "--ref", "release/v1", "--assignee", "alice,bob", "--milestone", "4", fields, "--no-input"}))
	require.JSONEq(t, `{"number":1,"ref":"release/v1","assignees":["alice","bob"],"milestone":{"id":4,"title":"Release"},"labels":[{"id":2,"name":"bug","color":"ff0000","description":"Defect","exclusive":true,"is_archived":false}]}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "1", "-R", "owner/repo", "--ref=", fields}))
	require.JSONEq(t, `{"number":1,"ref":"","assignees":[],"milestone":null,"labels":[]}`, out.String())
	require.Equal(t, 2, calls)
	require.Empty(t, diagnostics.String())
}

func TestJSONFieldsThatCostRequestsAreDocumentedOnce(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"help", "formatting"}))
	require.Contains(t, out.String(), "An expression that reaches such a field without naming it, as\nto_entries does, sees it empty")
	for _, args := range [][]string{{"pr", "list"}, {"issue", "view"}, {"issue", "edit"}, {"org", "member", "list"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--help")))
		require.Contains(t, strings.Join(strings.Fields(out.String()), " "), "(see gt help formatting)", args)
	}
}

func TestIssueCreateNamesMetadataGiteaLeftOut(t *testing.T) {
	var created []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/owner/repo/issue_templates":
			_, _ = w.Write([]byte(`[{"name":"Bug","content":"Steps:","labels":["bug"]}]`))
		case "GET /api/v1/repos/owner/repo/labels":
			writeFirstPage(w, r, `[{"id":2,"name":"bug"}]`)
		case "GET /api/v1/orgs/owner":
			w.WriteHeader(http.StatusNotFound)
		case "GET /api/v1/repos/owner/repo/milestones":
			writeFirstPage(w, r, `[{"id":4,"title":"v1"}]`)
		case "GET /api/v1/repos/owner/repo/projects":
			writeFirstPage(w, r, `[{"id":3,"title":"Board"}]`)
		case "GET /api/v1/repos/owner/repo/projects/3/columns":
			writeFirstPage(w, r, `[{"id":11,"title":"Backlog","default":true}]`)
		case "POST /api/v1/repos/owner/repo/projects/3/columns/11/issues/70":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"user should have a permission to write to a repo"}`))
		case "POST /api/v1/repos/owner/repo/issues":
			payload, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			created = append(created, string(payload))
			// Gitea drops the metadata of people who cannot write to the issues.
			_, _ = w.Write([]byte(`{"id":70,"number":1}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append([]string{"issue", "create", "-R", "owner/repo", "--title", "x", "--no-input", "--json=number"}, args...))
	}
	err := run("--label", "bug", "--milestone", "v1", "--assignee", "alice", "--deadline", "2028-01-15")
	require.EqualError(t, err, "issue #1 was created without --label, --milestone, --assignee, --deadline: Gitea leaves them out for people who cannot write to the issues of owner/repo")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.JSONEq(t, `{"number":1}`, out.String(), "the created issue is still reported")
	requirePayload(t, `{"title":"x","body":"","assignees":["alice"],"labels":[2],"milestone":4,"due_date":"2028-01-15T23:59:59Z"}`, strings.NewReader(created[0]))
	require.NoError(t, run("--template", "bug"))
	require.Contains(t, created[1], `"labels":[2]`)
	require.Equal(t, "! Gitea left out the labels of the issue template, as you cannot write to the issues of owner/repo\n", diagnostics.String())
	err = run("--project", "board")
	require.EqualError(t, err, `issue #1 was created, but follow-up steps failed: add to project "Board": permission denied by the Gitea instance: user should have a permission to write to a repo; add it later with: gt issue edit 1 --add-project 3 -R owner/repo`)
	require.Len(t, created, 3)
}

func TestNameFlagsAreTrimmed(t *testing.T) {
	var routes, payloads []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/owner":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/v1/repos/owner/repo":
			_, _ = w.Write([]byte(`{"full_name":"owner/repo","permissions":{"push":true}}`))
		case r.URL.Path == "/api/v1/repos/owner/repo/labels" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(`[{"id":2,"name":"bug"}]`))
		case r.URL.Path == "/api/v1/repos/owner/repo/labels", r.URL.Path == "/api/v1/repos/owner/repo/pulls/1/reviews":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"number":1,"assignees":[{"login":"carol"}]}`))
		default:
			payload, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			routes, payloads = append(routes, r.Method+" "+r.URL.Path), append(payloads, string(payload))
			if strings.HasSuffix(r.URL.Path, "/requested_reviewers") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"number":1,"labels":[{"id":2}],"assignees":[{"login":"Alice"},{"login":"bob"}]}`))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	for _, args := range [][]string{
		{"issue", "create", "--title", "Title", "--assignee", " alice, bob ", "--label", " bug", "--no-input"},
		{"issue", "edit", "1", "--add-assignee", " alice"},
		{"pr", "review", "request", "1", "--reviewer", " alice"},
		{"pr", "edit", "1", "--add-reviewer", "bob "},
		{"pr", "create", "--title", "Title", "--head", "feature", "--base", "main", "--reviewer", " dave", "--no-input"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "owner/repo", "--json=number")), args)
	}
	require.Equal(t, []string{
		"POST /api/v1/repos/owner/repo/issues",
		"PATCH /api/v1/repos/owner/repo/issues/1",
		"POST /api/v1/repos/owner/repo/pulls/1/requested_reviewers",
		"POST /api/v1/repos/owner/repo/pulls/1/requested_reviewers",
		"POST /api/v1/repos/owner/repo/pulls",
		"POST /api/v1/repos/owner/repo/pulls/1/requested_reviewers",
	}, routes)
	for i, want := range []string{
		`{"title":"Title","assignees":["alice","bob"],"labels":[2]}`,
		`{"assignees":["carol","alice"]}`,
		`{"reviewers":["alice"]}`,
		`{"reviewers":["bob"]}`,
		`{"title":"Title","head":"feature","base":"main","allow_maintainer_edit":true}`,
		`{"reviewers":["dave"]}`,
	} {
		requirePayload(t, want, strings.NewReader(payloads[i]))
	}
	require.Empty(t, diagnostics.String())
}

func TestIssueListAuthorMentionsAndTimeBounds(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		query := r.URL.Query()
		require.Equal(t, "alice", query.Get("created_by"))
		require.Equal(t, "bob", query.Get("mentioned_by"))
		require.Equal(t, "2026-10-03T12:00:00Z", query.Get("since"))
		require.Equal(t, "2026-10-04T12:00:00Z", query.Get("before"))
		require.Equal(t, "issues", query.Get("type"))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--author", "alice", "--mentions", "bob", "--since", "24h", "--until", "0h", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	require.Empty(t, diagnostics.String())
	for _, bounds := range [][]string{{"--since", "2026-10-05", "--until", "2026-10-04"}, {"--since", "invalid"}, {"--until", "-1h"}, {"--since="}} {
		out.Reset()
		args := append([]string{"issue", "list", "-R", "alice/project"}, bounds...)
		err := Execute(context.Background(), deps, args)
		require.Error(t, err)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.Empty(t, out.String())
	}
	require.Equal(t, 1, calls)
}

func TestIssueLabelFiltersRejectUnknownRepositoryLabels(t *testing.T) {
	var listed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/labels":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":1,"name":"bug"},{"id":2,"name":"help wanted"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/alice/project/issues", "/api/v1/repos/issues/search":
			listed = append(listed, r.URL.Path+"?labels="+r.URL.Query().Get("labels"))
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{
		{"issue", "list", "-R", "alice/project", "--label", "Typo"},
		{"issue", "list", "-R", "alice/project", "--label", "bug,Typo"},
		{"search", "issue", "-R", "alice/project", "--label", "Typo"},
		{"search", "pr", "-R", "alice/project", "--label", "Typo"},
	} {
		err := Execute(context.Background(), deps, args)
		require.Equal(t, clierr.Failure, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, `label "Typo" does not exist`)
	}
	require.Empty(t, listed)
	require.Empty(t, out.String())
	for _, args := range [][]string{
		{"issue", "list", "-R", "alice/project", "--label", " bug, help wanted"},
		{"search", "issue", "-R", "alice/project", "--label", "bug "},
		{"search", "issue", "--label", "Typo"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "--json")))
	}
	err := Execute(context.Background(), deps, []string{"search", "pr", "--owner", "alice", "--label", "bug", "--label", "help wanted", "--json"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "--label with several names requires --repo")
	require.Equal(t, []string{
		"/api/v1/repos/alice/project/issues?labels=bug,help wanted",
		"/api/v1/repos/alice/project/issues?labels=bug",
		"/api/v1/repos/issues/search?labels=Typo",
	}, listed)
}

func TestIssueListTables(t *testing.T) {
	var queries []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("created_by") == "carol" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.Header().Set("X-Total-Count", "12")
		_, _ = w.Write([]byte(`[{"number":7,"title":"Short","state":"open","user":{"login":"alice"},"html_url":"https://example.org/i/7",` +
			`"labels":[{"name":"bug"},{"name":"priority/high"}],"updated_at":"2026-10-11T18:00:00Z"},` +
			`{"number":12,"title":"Evil\u001b]52;c;aGk=\u0007\r\nTitle\u202e","state":"closed","user":{"login":"bob"},"html_url":"https://example.org/i/12","updated_at":"2026-10-09T20:00:00Z"},` +
			`{"number":14,"title":"Merged","state":"closed","pull_request":{"merged":true},"user":{"login":"bob"},"html_url":"https://example.org/p/14","updated_at":"2026-10-11T19:59:00Z"}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return testNow }
	args := []string{"issue", "list", "-R", "alice/project", "-L", "3"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "7\tShort\tbug, priority/high\topen\tissue\talice\t2026-10-11T18:00:00Z\n"+
		"12\tEvil]52;c;aGk= Title\t\tclosed\tissue\tbob\t2026-10-09T20:00:00Z\n"+
		"14\tMerged\t\tmerged\tpull\tbob\t2026-10-11T19:59:00Z\n", out.String())
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "Showing 3 of 12 open issues in alice/project · next: --page 2\n\n" +
			"ID   TITLE                 LABELS              AUTHOR  UPDATED\n" +
			"#7   Short                 bug, priority/high  alice   about 2 hours ago\n" +
			"#12  Evil]52;c;aGk= Title                      bob     about 2 days ago\n" +
			"#14  Merged                                    bob     about 1 minute ago\n"},
		{[]string{"--state", "all", "--kind", "all"}, "Showing 3 of 12 issues and pull requests in alice/project · next: --page 2\n\n" +
			"ID   TITLE                 LABELS              STATE   KIND   AUTHOR  UPDATED\n" +
			"#7   Short                 bug, priority/high  open    issue  alice   about 2 hours ago\n" +
			"#12  Evil]52;c;aGk= Title                      closed  issue  bob     about 2 days ago\n" +
			"#14  Merged                                    merged  pull   bob     about 1 minute ago\n"},
	} {
		out.Reset()
		deps.Streams.OutTTY = true
		require.NoError(t, Execute(context.Background(), deps, append(append(args, test.args...), "--no-input")))
		require.Equal(t, test.want, stripLinks(withoutANSI(out.String())))
	}
	require.Contains(t, out.String(), "\x1b]8;;https://example.org/i/7\x1b\\\x1b[32m#7\x1b[0m\x1b]8;;\x1b\\")
	require.Contains(t, out.String(), "\x1b[35mmerged\x1b[0m")
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--author", "carol")))
	require.Empty(t, out.String())
	require.Equal(t, "no open issues match your filters in alice/project\n", diagnostics.String())
	queries = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "-A", "@me", "-a", "@me", "--json")))
	require.Contains(t, queries[0], "assigned_by=alice")
	require.Contains(t, queries[0], "created_by=alice")
}

func TestIssueListWebOpensTheFilteredPage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "-w", "-s", "closed", "-S", "login bug"}))
	require.Equal(t, server.URL+"/alice/project/issues?q=login+bug&state=closed", browser.resource)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "-w", "--no-assignee"}))
	require.Equal(t, server.URL+"/alice/project/issues?assignee=%28none%29&state=open", browser.resource)
}

func TestIssueListKindFilter(t *testing.T) {
	var seen string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		seen = r.URL.Query().Get("type")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--kind", "pulls", "--json"}))
	require.Equal(t, "pulls", seen)
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"issue", "list", "--kind", "bugs"})))
}

func TestIssueListWithoutRepoPointsToSearch(t *testing.T) {
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{}
	err := Execute(context.Background(), deps, []string{"issue", "list", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "gt search issue")
	require.Empty(t, out.String())
}

func TestIssueMilestoneFiltersRejectUnknownMilestones(t *testing.T) {
	var listed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/milestones":
			require.Equal(t, "all", r.URL.Query().Get("state"))
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":1,"title":"v1.0"},{"id":2,"title":"2026","state":"closed"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/alice/project/issues", "/api/v1/repos/issues/search":
			listed = append(listed, r.URL.Path+"?milestones="+r.URL.Query().Get("milestones"))
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "list", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
		{[]string{"issue", "list", "--milestone", "999"}, "milestone 999 not found in alice/project"},
		{[]string{"issue", "list", "--milestone", "v1.0,nope"}, `milestone "nope" not found`},
		{[]string{"search", "issue", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
		{[]string{"search", "pr", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
	} {
		err := Execute(context.Background(), deps, append(test.args, "-R", "alice/project", "--json"))
		require.Equal(t, clierr.Failure, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, test.want)
	}
	require.Empty(t, listed)
	require.Empty(t, out.String())
	for _, args := range [][]string{
		{"issue", "list", "-R", "alice/project", "--milestone", "v1.0,2026"},
		{"issue", "list", "-R", "alice/project", "--milestone", "1"},
		{"search", "issue", "-R", "alice/project", "--milestone", "V1.0"},
		{"search", "issue", "--milestone", "nope"},
		{"issue", "list", "-R", "alice/project", "-m", "v1.0", "-m", "2026"},
		{"search", "issue", "-m", "v1.0", "-m", "nope"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "--json")))
	}
	err := Execute(context.Background(), deps, []string{"search", "pr", "--owner", "alice", "--label", "bug", "--label", "help wanted", "--json"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "--label with several names requires --repo")
	require.Equal(t, []string{
		"/api/v1/repos/alice/project/issues?milestones=v1.0,2026",
		"/api/v1/repos/alice/project/issues?milestones=v1.0",
		"/api/v1/repos/alice/project/issues?milestones=v1.0",
		"/api/v1/repos/issues/search?milestones=nope",
		"/api/v1/repos/alice/project/issues?milestones=v1.0,2026",
		"/api/v1/repos/issues/search?milestones=v1.0,nope",
	}, listed)
	require.Contains(t, diagnostics.String(), "pass --repo to check them")
}

func TestIssueCommandsComplete(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues":
			_, _ = w.Write([]byte(`[{"number":4,"title":"Crash","state":` + strconv.Quote(r.URL.Query().Get("state")) + `}]`))
		case "/api/v1/repos/alice/project/labels":
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"}]`))
		case "/api/v1/repos/alice/project/milestones":
			_, _ = w.Write([]byte(`[{"id":3,"title":"v1.0"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"issue", "list", "--state", ""}, []string{"open", "closed", "all"}},
		{[]string{"issue", "list", "-s", "c"}, []string{"closed"}},
		{[]string{"issue", "list", "--kind", ""}, []string{"issues", "pulls", "all"}},
		{[]string{"issue", "list", "--label", ""}, []string{"bug"}},
		{[]string{"issue", "list", "--milestone", "v1.0,"}, []string{}},
		{[]string{"issue", "create", "-m", ""}, []string{"v1.0"}},
		{[]string{"issue", "edit", "4", "--add-label", ""}, []string{"bug"}},
		{[]string{"issue", "view", ""}, []string{"4\tCrash"}},
		{[]string{"issue", "view", "4", ""}, []string{}},
		{[]string{"issue", "close", "5", ""}, []string{"4\tCrash"}},
		{[]string{"issue", "reopen", ""}, []string{"4\tCrash"}},
	} {
		values, directive := complete(t, deps, out, test.args...)
		require.Equal(t, test.want, values, test.args)
		require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive&cobra.ShellCompDirectiveNoFileComp, test.args)
	}
}

func TestIssueListNoAssigneeFiltersEveryPageRead(t *testing.T) {
	var pages []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		require.Empty(t, query.Get("assigned_by"))
		require.Equal(t, "50", query.Get("limit"))
		pages = append(pages, query.Get("page"))
		start, _ := strconv.Atoi(query.Get("page"))
		issues := []string{}
		for n := (start-1)*50 + 1; n <= min(start*50, 60); n++ {
			assignees := `[{"login":"bob"}]`
			if n%20 == 0 {
				assignees = `[]`
			}
			issues = append(issues, `{"number":`+strconv.Itoa(n)+`,"state":"open","assignees":`+assignees+`}`)
		}
		_, _ = w.Write([]byte("[" + strings.Join(issues, ",") + "]"))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--no-assignee", "--json", "number"}))
	require.JSONEq(t, `[{"number":20},{"number":40},{"number":60}]`, out.String())
	require.Equal(t, []string{"1", "2"}, pages)
	err := Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--no-assignee", "--assignee", "bob"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Len(t, pages, 2)
}

func TestIssueListWebRejectsFiltersTheBrowserCannotReceive(t *testing.T) {
	for _, flag := range []string{"--label=bug", "--assignee=alice", "--milestone=v1", "--author=bob"} {
		requireUsageBeforeRequests(t, "cannot be combined with --web", "issue", "list", "--web", flag)
		requireUsageBeforeRequests(t, "cannot be combined with --web; the browser only receives --state", "pr", "list", "--web", flag)
	}
	requireUsageBeforeRequests(t, "--kind all cannot be combined with --web", "issue", "list", "--web", "--kind=all")
}

func TestIssueCloseAndReopenHelpFitTheVerb(t *testing.T) {
	for verb, want := range map[string][]string{
		"close":  {`--comment "Done in #14"`, "Leave a comment before closing the issue"},
		"reopen": {`--comment "Still happens on v1.2"`, "Leave a comment before reopening the issue"},
	} {
		deps, out, _ := testDeps(t)
		require.NoError(t, Execute(context.Background(), deps, []string{"issue", verb, "--help"}))
		for _, text := range want {
			require.Contains(t, out.String(), text, verb)
		}
	}
}

func TestMissingIssuesBranchesAndOrganizationsAreNamed(t *testing.T) {
	server := fakeGitea(t, map[string][2]string{
		"GET /api/v1/user":                                   {"200", `{"login":"alice"}`},
		"GET /api/v1/repos/alice/project":                    {"200", `{"full_name":"alice/project","has_issues":true}`},
		"GET /api/v1/repos/alice/project/issues/99/timeline": {"500", `{"message":"issue does not exist [id: 0, repo_id: 81, index: 99]"}`},
		"GET /api/v1/repos/alice/project/issues/99/assets":   {"404", `{"message":"issue does not exist [id: 0, repo_id: 81, index: 99]"}`},
		"PATCH /api/v1/repos/alice/project/branches/nope":    {"404", `{"message":"Branch doesn't exist."}`},
	})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "timeline", "99", "-R", "alice/project"}, "issue #99 not found in alice/project"},
		{[]string{"issue", "attachment", "list", "99", "-R", "alice/project"}, "issue #99 not found in alice/project"},
		{[]string{"branch", "rename", "nope", "x", "-R", "alice/project"}, `branch "nope" not found in alice/project`},
		{[]string{"team", "list", "nosuchorg"}, `organization "nosuchorg" not found`},
	} {
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append(tc.args, "--no-input"))
		require.EqualError(t, Explain(context.Background(), deps, err), tc.want, "%v", tc.args)
	}
}
