// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func TestPullEditPreservesOmittedFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		current string
		patch   string
		reads   int
	}{
		{"ready reads existing title", []string{"--ready"}, "[wIp] existing", `{"title":"existing"}`, 1},
		{"draft is idempotent", []string{"--draft", "--title", "wip: already"}, "", `{"title":"wip: already"}`, 0},
		{"title keeps a draft", []string{"--title", "renamed"}, "WIP: existing", `{"title":"WIP: renamed"}`, 1},
		{"title keeps a ready pull request", []string{"--title", "renamed"}, "existing", `{"title":"renamed"}`, 1},
		{"title with ready", []string{"--ready", "--title", "renamed"}, "", `{"title":"renamed"}`, 0},
		// An edit of several fields reads the pull request first, to tell what a failure saved.
		{"explicit empty and false", []string{"--body=", "--allow-maintainer-edit=false"}, "", `{"body":"","allow_maintainer_edit":false}`, 1},
		{"base only", []string{"--base", "stable"}, "", `{"base":"stable"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, writes := 0, 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/pulls/7", r.URL.Path)
				if r.Method == http.MethodGet {
					reads++
					draft := strings.HasPrefix(strings.ToLower(tc.current), "wip:") || strings.HasPrefix(strings.ToLower(tc.current), "[wip]")
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"number": 7, "title": tc.current, "draft": draft}))
					return
				}
				require.Equal(t, http.MethodPatch, r.Method)
				writes++
				requirePayload(t, tc.patch, r.Body)
				_, _ = w.Write([]byte(`{"number":7,"title":"result"}`))
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			args := append([]string{"pr", "edit", "7", "-R", "alice/project", "--json=number"}, tc.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.reads, reads)
			require.Equal(t, 1, writes)
			require.JSONEq(t, `{"number":7}`, out.String())
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestPullEditAddsAssigneesWithoutExtraReads(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"}]}`))
			return
		}
		requirePayload(t, `{"assignees":["alice","bob"]}`, r.Body)
		_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"},{"login":"bob"}]}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "edit", "7", "-R", "alice/project", "--add-assignee", "bob,Alice", "--json=number"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/issues/7", "PATCH /api/v1/repos/alice/project/pulls/7"}, calls)
}

func TestPullEditImplicitInteractiveForm(t *testing.T) {
	var patches []string
	title := "Old"
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"` + title + `","draft":` + strconv.FormatBool(title != "Old") + `,"body":"Old body","state":"open","base":{"ref":"main"},"head":{"ref":"feature"},` +
				`"assignees":[{"login":"alice"}],"labels":[{"id":2,"name":"bug"}],"milestone":{"id":4,"title":"Release"},"due_date":"2028-01-15T23:59:59Z"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/pulls/7":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			patches = append(patches, string(body))
			_, _ = w.Write([]byte(`{"number":7,"title":"Edited","base":{"ref":"stable"},"assignees":[{"login":"alice"}],"milestone":{"id":4},"due_date":"2028-01-15T23:59:59Z"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		title   string
		edit    func(*terminal.IssueEditFormValues)
		want    string
		warning string
	}{
		{"Old", func(*terminal.IssueEditFormValues) {}, "", "No changes to pull request alice/project#7 (Old)"},
		{"Old", func(v *terminal.IssueEditFormValues) { v.Title, v.Base = "Edited", "stable" }, `{"title":"Edited","base":"stable"}`, ""},
		// A draft keeps its marker, as with --title.
		{"WIP: Old", func(v *terminal.IssueEditFormValues) { v.Title = "Edited" }, `{"title":"WIP: Edited"}`, ""},
	} {
		patches, title = nil, tc.title
		deps, out, diagnostics := serverDeps(t, server.URL)
		deps.IssueEditForm = issueEditFormFunc(func(_ context.Context, choices terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
			require.Equal(t, "Old", choices.Title)
			require.Equal(t, "main", choices.Base)
			require.Equal(t, "Old body", choices.Body)
			require.Equal(t, []string{"alice", "bob"}, choices.AvailableAssignees)
			due, err := time.Parse(time.DateOnly, choices.Deadline.Format(time.DateOnly))
			require.NoError(t, err)
			values := terminal.IssueEditFormValues{
				Title: choices.Title, Body: choices.Body, Base: choices.Base, Assignees: choices.Assignees,
				Milestone: choices.Milestone.ID, Labels: []int64{choices.Labels[0].ID}, Deadline: &due,
			}
			tc.edit(&values)
			return values, nil
		})
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		require.NoError(t, Execute(context.Background(), deps, []string{"pr", "edit", "7", "-R", "alice/project", "--json=number", "--interactive"}))
		require.JSONEq(t, `{"number":7}`, out.String())
		require.Contains(t, diagnostics.String(), tc.warning)
		if tc.want == "" {
			require.Empty(t, patches)
			continue
		}
		require.Len(t, patches, 1)
		requirePayload(t, tc.want, strings.NewReader(patches[0])) // only what changed is sent
	}
}

func TestPullEditNamesWhatGiteaLeftOutForAnAuthor(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--milestone", "Release"}, "--milestone"},
		{[]string{"--add-assignee", "bob"}, "--add-assignee"},
		{[]string{"--milestone", "Release", "--deadline", "2028-02-01"}, "--milestone, --deadline"},
		{[]string{"--remove-milestone", "--set-assignees", "bob"}, "--remove-milestone, --set-assignees"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var writes []string
			// Gitea answers an author who cannot write with the pull request as it was.
			const unchanged = `{"number":7,"title":"Edited","assignees":[{"login":"alice"}],"milestone":{"id":2,"title":"Old"},"due_date":"2027-01-01T23:59:59Z"}`
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/repos/alice/project/milestones":
					writeFirstPage(w, r, `[{"id":4,"title":"Release","state":"open"}]`)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/7"):
					_, _ = w.Write([]byte(unchanged))
				default:
					writes = append(writes, r.Method+" "+r.URL.Path)
					_, _ = w.Write([]byte(unchanged))
				}
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"pr", "edit", "7", "-R", "alice/project", "--json=number,title"}, tc.args...))
			them := "them"
			if !strings.Contains(tc.want, ",") {
				them = "it"
			}
			require.EqualError(t, err, "pull request #7 was edited without "+tc.want+": Gitea leaves "+them+" out for people who cannot write to the pull requests of alice/project")
			require.Equal(t, clierr.Failure, clierr.ExitCode(err))
			require.NotEmpty(t, writes)
			require.JSONEq(t, `{"number":7,"title":"Edited"}`, out.String(), "the edited pull request is still reported")
			require.Empty(t, diagnostics.String())
		})
	}
}

// pullEditServer serves pull request 12 of acme/app, with label bug, milestone Release and
// project Board, and edits it in Gitea's order: each endpoint saves the title and the body, then,
// for people who can write, the assignees and the milestone, and the pull request endpoint then
// the base branch and whether maintainers can edit. It stops at the first refusal. Gitea leaves
// the assignees and the milestone out, and refuses the labels, for people who cannot write. It
// refuses assignee "private" with a 500, as for a user without access to a private repository,
// and assignee "blocked" with a 403.
type pullEditServer struct {
	t      *testing.T
	writer bool
	// refuseAll refuses every edit, as Gitea does for people who neither wrote the pull request
	// nor can write to the pull requests.
	refuseAll bool
	// readStatus answers the reads of the pull request after the first with this status, when set.
	readStatus  int
	title, base string
	milestone   int64
	maintainers bool
	labels      []int64
	assignees   []string
	reads       int
	// writes are the requests that change the pull request, and mutations those that change
	// the project boards.
	writes    []string
	mutations []string
	projects  http.Handler
}

func newPullEditServer(t *testing.T, writer bool) *pullEditServer {
	f := &pullEditServer{t: t, writer: writer, title: "Old", base: "main", maintainers: true}
	f.projects = projectServer(t, &f.mutations)
	return f
}

func (f *pullEditServer) state() string {
	pull := map[string]any{
		"id": 70, "number": 12, "title": f.title, "base": map[string]any{"ref": f.base},
		"allow_maintainer_edit": f.maintainers, "labels": []any{}, "assignees": []any{}, "pull_request": map[string]any{},
	}
	if f.milestone != 0 {
		pull["milestone"] = map[string]any{"id": f.milestone, "title": "Release"}
	}
	for _, id := range f.labels {
		pull["labels"] = append(pull["labels"].([]any), map[string]any{"id": id, "name": "bug"})
	}
	for _, name := range f.assignees {
		pull["assignees"] = append(pull["assignees"].([]any), map[string]any{"login": name})
	}
	data, err := json.Marshal(pull)
	require.NoError(f.t, err)
	return string(data)
}

func (f *pullEditServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":` + strconv.Quote(message) + `}`))
	}
	if r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/repos/") {
		f.writes = append(f.writes, r.Method+" "+r.URL.Path)
	}
	switch {
	case r.URL.Path == "/api/v1/repos/acme/app" && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"full_name":"acme/app"}`))
	case r.URL.Path == "/api/v1/repos/acme/app/labels":
		writeFirstPage(w, r, `[{"id":1,"name":"bug"}]`)
	case r.URL.Path == "/api/v1/repos/acme/app/milestones":
		writeFirstPage(w, r, `[{"id":4,"title":"Release","state":"open"}]`)
	case r.URL.Path == "/api/v1/orgs/acme" && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/app/pulls/12":
		if f.reads++; f.readStatus != 0 && f.reads > 1 {
			fail(f.readStatus, "unavailable")
			return
		}
		_, _ = w.Write([]byte(f.state()))
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/app/issues/12":
		_, _ = w.Write([]byte(f.state()))
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/repos/acme/app/issues/12/labels":
		if !f.writer {
			fail(http.StatusForbidden, "write permission is required")
			return
		}
		var body struct {
			Labels []int64 `json:"labels"`
		}
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.labels = body.Labels
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug"}]`))
	case r.Method == http.MethodPatch:
		if f.refuseAll {
			fail(http.StatusForbidden, "user is not allowed to edit")
			return
		}
		var body struct {
			gitea.EditPullRequestOption
			Milestone *int64 `json:"milestone"`
		}
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.title = cmp.Or(body.Title, f.title)
		if body.Assignees != nil && f.writer {
			for _, name := range body.Assignees {
				switch name {
				case "private":
					fail(http.StatusInternalServerError, "user private has no access to the repository")
					return
				case "blocked":
					fail(http.StatusForbidden, "user is blocked")
					return
				}
			}
			f.assignees = body.Assignees
		}
		// The pull request endpoint takes no zero milestone, which the SDK sends for none.
		if body.Milestone != nil && f.writer && (*body.Milestone != 0 || strings.HasSuffix(r.URL.Path, "/issues/12")) {
			f.milestone = *body.Milestone
		}
		if strings.HasSuffix(r.URL.Path, "/pulls/12") {
			if body.Base != "" {
				if body.Base != "main" && body.Base != "dev" {
					fail(http.StatusNotFound, "new base '"+body.Base+"' not exist")
					return
				}
				f.base = body.Base
			}
			if body.AllowMaintainerEdit != nil {
				f.maintainers = *body.AllowMaintainerEdit
			}
		}
		_, _ = w.Write([]byte(f.state()))
	default:
		f.projects.ServeHTTP(w, r)
	}
}

func TestPullEditSendsTheOtherChangesWhenGiteaRefusesLabels(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		writes    []string
		mutations []string
		want      string
	}{
		{
			[]string{"--title", "Fix", "--add-label", "bug", "--allow-maintainer-edit=false", "--add-project", "Board"},
			[]string{"PATCH /api/v1/repos/acme/app/issues/12", "PATCH /api/v1/repos/acme/app/pulls/12", "PUT /api/v1/repos/acme/app/issues/12/labels"},
			[]string{"POST /api/v1/orgs/acme/projects/3/columns/11/issues/70"},
			"the title, whether maintainers can edit and the project boards were saved, but the labels were left unchanged: Gitea refuses label changes from people who cannot write to the pull requests of acme/app",
		},
		{
			// Nothing was saved: Gitea left the milestone out.
			[]string{"--milestone", "Release", "--add-label", "bug"},
			[]string{"PATCH /api/v1/repos/acme/app/issues/12", "PUT /api/v1/repos/acme/app/issues/12/labels"},
			nil,
			"Gitea refuses label changes from people who cannot write to the pull requests of acme/app; nothing was changed",
		},
		{
			// The milestone that a request which succeeded left out is named as not changed.
			[]string{"--title", "Fix", "--milestone", "Release", "--add-label", "bug"},
			[]string{"PATCH /api/v1/repos/acme/app/issues/12", "PUT /api/v1/repos/acme/app/issues/12/labels"},
			nil,
			"the title was saved, but the milestone was not changed; the labels were left unchanged: Gitea refuses label changes from people who cannot write to the pull requests of acme/app",
		},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			fake := newPullEditServer(t, false)
			server := newAuthenticatedServer(fake)
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"pr", "edit", "12", "-R", "acme/app", "--json=number"}, tc.args...))
			require.EqualError(t, err, tc.want)
			require.True(t, services.IsForbidden(err))
			require.Equal(t, tc.writes, fake.writes)
			require.Equal(t, tc.mutations, fake.mutations, "the boards change after the other changes")
			require.Empty(t, out.String())
		})
	}
}

func TestPullEditNamesWhatWasSavedWhenTheBaseIsRefused(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		mutations []string
		want      string
	}{
		{
			[]string{"--title", "Fix", "--add-label", "bug", "--base", "typo", "--add-project", "Board"},
			[]string{"POST /api/v1/orgs/acme/projects/3/columns/11/issues/70"},
			`the title, the labels and the project boards were saved, but changing the base branch failed: branch "typo" not found in acme/app`,
		},
		{
			[]string{"--base", "typo", "--add-project", "Board"},
			nil,
			`branch "typo" not found in acme/app; the project boards were not changed`,
		},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			fake := newPullEditServer(t, true)
			server := newAuthenticatedServer(fake)
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"pr", "edit", "12", "-R", "acme/app"}, tc.args...))
			require.EqualError(t, err, tc.want)
			require.Equal(t, tc.mutations, fake.mutations)
			require.Empty(t, out.String())
		})
	}
}

func TestPullEditNamesNoBoardThatAlreadyHeldThePullRequestAsSaved(t *testing.T) {
	fake := newPullEditServer(t, true)
	server := newAuthenticatedServer(fake)
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "edit", "12", "-R", "acme/app", "--add-project", "Board"}))
	err := Execute(context.Background(), deps, []string{"pr", "edit", "12", "-R", "acme/app", "--title", "C4", "--base", "typo", "--add-project", "Board"})
	require.EqualError(t, err, `the title was saved, but the base branch was not changed; the edit failed: branch "typo" not found in acme/app`)
	require.Equal(t, []string{"POST /api/v1/orgs/acme/projects/3/columns/11/issues/70"}, fake.mutations, "the board already holds the pull request")
}

func TestPullReadyFailsWhenGiteaStillSeesADraft(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":7,"title":"Draft: x","draft":true}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "ready", "7", "-R", "alice/project"})
	require.ErrorContains(t, err, "still treats the pull request as a draft")
	require.NotContains(t, diagnostics.String(), "Marked")
}

func TestPullEditNamesALeftOutMilestoneAsNotChangedWhenARequestFails(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{
			[]string{"--title", "Fix", "--milestone", "Release", "--base", "typo"},
			`the title was saved, but the milestone was not changed; changing the base branch failed: branch "typo" not found in acme/app`,
		},
		{[]string{"--milestone", "Release", "--base", "typo"}, `branch "typo" not found in acme/app`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			// Gitea saves the title of an author who cannot write, leaves the milestone out and
			// refuses the missing base branch.
			server := newAuthenticatedServer(newPullEditServer(t, false))
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"pr", "edit", "12", "-R", "acme/app"}, tc.args...))
			require.EqualError(t, err, tc.want)
			require.NotContains(t, err.Error(), "cannot write", "a failed request tells nothing of write access")
			require.Empty(t, out.String())
		})
	}
}

func TestPullEditBlamesNoFieldNorWriteAccessForAFailedRequest(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{
			[]string{"--title", "P2", "--add-assignee", "private"},
			"the title was saved, but the assignees were not changed; the edit failed: ",
		},
		{
			[]string{"--title", "P2", "--add-assignee", "private", "--milestone", "Release"},
			"the title was saved, but the assignees and the milestone were not changed; the edit failed: ",
		},
		{
			[]string{"--title", "P2", "--add-assignee", "blocked", "--base", "dev"},
			"the title was saved, but the assignees and the base branch were not changed; the edit failed: permission denied by the Gitea instance: user is blocked",
		},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			// Gitea saves the title of a user who can write, then fails on the assignee.
			fake := newPullEditServer(t, true)
			server := newAuthenticatedServer(fake)
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"pr", "edit", "12", "-R", "acme/app"}, tc.args...))
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "cannot write")
			require.NotContains(t, err.Error(), "changing the base branch")
			require.Equal(t, "P2", fake.title)
			require.Equal(t, "main", fake.base)
			require.Empty(t, out.String())
		})
	}
}

func TestPullEditNamesWhatWasSavedWhenItCannotReadThePullRequestBack(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusNotFound} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fake := newPullEditServer(t, true)
			fake.readStatus = status
			server := newAuthenticatedServer(fake)
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "edit", "12", "-R", "acme/app", "--title", "New", "--milestone", "Release", "--add-label", "bug", "--base", "typo"})
			require.ErrorContains(t, err, `the title, the milestone and the labels were saved, but the edit failed: branch "typo" not found in acme/app; the other changes may or may not have been saved, as reading the pull request back failed: `)
			require.ErrorContains(t, err, "; gt pr view 12 shows the current state")
			require.Equal(t, []int64{1}, fake.labels, "the labels go after the issue fields were taken")
			require.Empty(t, out.String())
		})
	}
}

func TestPullEditNamesNoTitleItHadAsSaved(t *testing.T) {
	fake := newPullEditServer(t, false)
	fake.refuseAll = true
	server := newAuthenticatedServer(fake)
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{{"--title", "Old"}, {"--title", "Old", "--body", "B"}} {
		err := Execute(context.Background(), deps, append([]string{"pr", "edit", "12", "-R", "acme/app"}, args...))
		require.EqualError(t, err, "permission denied by the Gitea instance: user is not allowed to edit")
		require.NotContains(t, err.Error(), "saved")
	}
}
