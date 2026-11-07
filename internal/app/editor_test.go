// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"

	"github.com/stretchr/testify/require"
)

func TestCommentEditorDraftLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "editor failure", "submission failure"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"number":3,"title":"Crash"}`))
					return
				}
				requests++
				var options struct{ Body string }
				require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
				require.Equal(t, "edited comment", options.Body)
				if mode == "submission failure" {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte(`{"id":7,"body":"edited comment"}`))
			}))
			defer server.Close()
			deps, out, _ := testDeps(t)
			editor, path := newDraftEditor(t, "edited comment")
			if mode == "editor failure" {
				editor.err = errors.New("editor failed")
			}
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
			err := Execute(context.Background(), deps, []string{"issue", "comment", "create", "3", "--repo", "alice/project", "--editor", "--no-input", "--json=body"})
			require.Equal(t, "chosen-editor", editor.command)
			requireDraft(t, path, mode != "success")
			if mode == "success" {
				require.NoError(t, err)
				require.JSONEq(t, `{"body":"edited comment"}`, out.String())
			} else {
				require.ErrorContains(t, err, "draft saved at "+path)
				require.Empty(t, out.String())
			}
			if mode == "editor failure" {
				require.Zero(t, requests)
			} else {
				require.Equal(t, 1, requests)
			}
		})
	}
}

func TestCommentEditEditorSeedsCurrentBody(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/issues/comments/7":
			_, _ = w.Write([]byte(`{"id":7,"body":"current comment","user":{"login":"alice"}}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/issues/comments/7":
			var options struct{ Body string }
			require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
			require.Equal(t, "updated comment", options.Body)
			_, _ = w.Write([]byte(`{"id":7,"body":"updated comment"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	editor, path := newDraftEditor(t, "updated comment")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "comment", "edit", "7", "--repo", "alice/project", "--editor", "--no-input", "--json=body"}))
	require.Equal(t, "current comment", editor.initial)
	require.JSONEq(t, `{"body":"updated comment"}`, out.String())
	requireDraft(t, path, false)
}

func TestBodySourcesRejectInvalidInputBeforeAuthentication(t *testing.T) {
	commands := [][]string{
		{"issue", "create"},
		{"pr", "create"},
		{"issue", "edit", "3"},
		{"pr", "edit", "3"},
		{"release", "create", "v1"},
		{"release", "edit", "v1"},
		{"wiki", "create"},
		{"wiki", "edit", "Home"},
		{"issue", "comment", "create", "3"},
		{"pr", "comment", "create", "3"},
		{"pr", "comment", "edit", "3"},
		{"pr", "review", "3"},
		{"pr", "review", "reply", "3", "7"},
		{"pr", "review", "submit", "3", "7"},
		{"pr", "review", "dismiss", "3", "7"},
	}
	for _, command := range commands {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			for _, input := range [][]string{
				{"--body-file="},
				{"--body", "text", "--body-file", "missing.md"},
				{"--editor", "--body", "text"},
				{"--editor", "--body-file", "missing.md"},
			} {
				if command[0] == "release" {
					input = slices.Clone(input)
					for i, arg := range input {
						input[i] = strings.Replace(arg, "--body", "--notes", 1)
					}
				}
				want := "cannot be combined with"
				if len(input) == 1 {
					want = "--body-file must name a file or -"
					if command[0] == "release" {
						want = "--notes-file must name a file or -"
					}
				}
				requireUsageBeforeRequests(t, want, append(append(append([]string{}, command...), input...), "--no-input")...)
			}
		})
	}
}

func TestCreationEditorSubmitsTypedBodyAndCleansDraft(t *testing.T) {
	for _, tc := range []struct {
		name, path, event string
		args              []string
	}{
		{"issue", "/api/v1/repos/alice/project/issues", "", []string{"issue", "create", "--title", "Issue"}},
		{"pr", "/api/v1/repos/alice/project/pulls", "", []string{"pr", "create", "--title", "PR", "--head", "feature", "--base", "main"}},
		{"review", "/api/v1/repos/alice/project/pulls/3/reviews", "COMMENT", []string{"pr", "review", "3", "--comment"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" {
					_, _ = w.Write([]byte(`{"number":3}`))
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/reviews" {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				require.Equal(t, tc.path, r.URL.Path)
				var options struct{ Event, Body string }
				require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
				require.Equal(t, tc.event, options.Event)
				require.Equal(t, "edited body", options.Body)
				_, _ = w.Write([]byte(`{"id":7,"body":"edited body"}`))
			}))
			defer server.Close()
			deps, out, _ := testDeps(t)
			editor, path := newDraftEditor(t, "edited body")
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
			require.NoError(t, Execute(context.Background(), deps, append(tc.args, "--repo", "alice/project", "--editor", "--no-input", "--json=body")))
			require.JSONEq(t, `{"body":"edited body"}`, out.String())
			requireDraft(t, path, false)
		})
	}
}

func TestBodyEditorStartsWithCurrentBody(t *testing.T) {
	for _, group := range []string{"issue", "pr"} {
		t.Run(group, func(t *testing.T) {
			mutations := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"number":3,"body":"existing body"}`))
					return
				}
				require.Equal(t, http.MethodPatch, r.Method)
				var options services.IssueEdit
				require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
				require.NotNil(t, options.Body)
				require.Equal(t, "edited body", *options.Body)
				mutations++
				_, _ = w.Write([]byte(`{"number":3,"body":"edited body"}`))
			}))
			defer server.Close()
			deps, out, _ := testDeps(t)
			editor, path := newDraftEditor(t, "edited body")
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
			require.NoError(t, Execute(context.Background(), deps, []string{group, "edit", "3", "--repo", "alice/project", "--editor", "--no-input", "--json=body"}))
			require.Equal(t, "existing body", editor.initial)
			require.Equal(t, 1, mutations)
			require.JSONEq(t, `{"body":"edited body"}`, out.String())
			requireDraft(t, path, false)
		})
	}
}

func TestReleaseEditorNotes(t *testing.T) {
	for _, verb := range []string{"create", "edit"} {
		t.Run(verb, func(t *testing.T) {
			mutations := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","body":"existing notes"}`))
					return
				}
				var options services.ReleaseOptions
				require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
				require.NotNil(t, options.Body)
				require.Equal(t, "edited notes", *options.Body)
				mutations++
				_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","body":"edited notes"}`))
			}))
			defer server.Close()
			deps, out, _ := testDeps(t)
			editor, path := newDraftEditor(t, "edited notes")
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
			args := []string{"release", verb, "v1", "--repo", "alice/project", "--editor", "--no-input", "--json=body"}
			require.NoError(t, Execute(context.Background(), deps, args))
			initial := ""
			if verb == "edit" {
				initial = "existing notes"
			}
			require.Equal(t, initial, editor.initial)
			require.JSONEq(t, `{"body":"edited notes"}`, out.String())
			requireDraft(t, path, false)
			if verb == "edit" {
				require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, append(args, "v2"))))
			}
			require.Equal(t, 1, mutations)
		})
	}
}

func TestWikiEditorRejectsEmptyContentAndKeepsDraft(t *testing.T) {
	deps, out, _ := testDeps(t)
	server := newAuthenticatedServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("empty wiki draft must not be submitted")
	}))
	defer server.Close()
	editor, path := newDraftEditor(t, "")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
	args := []string{"wiki", "create", "--title", "Home", "--repo", "alice/project", "--editor", "--no-input"}
	err := Execute(context.Background(), deps, args)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "wiki content cannot be empty")
	require.ErrorContains(t, err, "draft saved at "+path)
	requireDraft(t, path, true)
	require.Empty(t, out.String())
}

func TestReviewReplyEditorDraftLifecycle(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "accepted"}[accepted], func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/v1/repos/alice/project/pulls/3/comments/7/replies", r.URL.Path)
				var options map[string]string
				require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
				require.Equal(t, "Edited reply", options["body"])
				if !accepted {
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				_, _ = w.Write([]byte(`{"id":9,"body":"Edited reply"}`))
			}))
			defer server.Close()
			deps, out, _ := testDeps(t)
			editor, path := newDraftEditor(t, "Edited reply")
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
			err := Execute(context.Background(), deps, []string{"pr", "review", "reply", "3", "7", "--repo", "alice/project", "--editor", "--no-input", "--json=body"})
			requireDraft(t, path, !accepted)
			if accepted {
				require.NoError(t, err)
				require.JSONEq(t, `{"body":"Edited reply"}`, out.String())
				return
			}
			var apiErr *transport.APIError
			require.ErrorAs(t, err, &apiErr)
			require.ErrorContains(t, err, "draft saved at "+path)
			require.Empty(t, out.String())
		})
	}
}

func TestReviewLifecycleEditorRejectsEmptyRequiredText(t *testing.T) {
	for _, verb := range []string{"submit", "dismiss"} {
		t.Run(verb, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodGet, r.Method)
				if verb == "submit" && strings.HasSuffix(r.URL.Path, "/reviews/7") {
					_, _ = w.Write([]byte(`{"id":7,"body":"Saved draft","state":"PENDING","user":{"login":"alice"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":7,"body":"Saved draft","state":"APPROVED","user":{"login":"bob"}}`))
			}))
			defer server.Close()
			deps, out, _ := testDeps(t)
			editor, path := newDraftEditor(t, "")
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
			args := []string{"pr", "review", verb, "3", "7", "--repo", "alice/project", "--editor", "--no-input", "--yes"}
			if verb == "submit" {
				args = append(args, "--request-changes")
			}
			err := Execute(context.Background(), deps, args)
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			require.ErrorContains(t, err, "requires a")
			require.NotContains(t, err.Error(), "draft saved", "an empty draft holds nothing to keep")
			if verb == "submit" {
				require.Equal(t, "Saved draft", editor.initial)
			} else {
				require.Empty(t, editor.initial)
			}
			requireDraft(t, path, false)
			require.Empty(t, out.String())
		})
	}
}

func TestBodyEditorTypesInlineWithoutAnEditor(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	deps, _, _ := testDeps(t)
	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	a, cmd := outputApplication(deps, "")
	draft, err := a.bodyEditor(cmd, terminal.BodyTitle)(context.Background(), "## Steps")
	require.NoError(t, err)
	require.Equal(t, "typed body", draft.Body)
	require.Equal(t, []string{terminal.BodyTitle + " ## Steps"}, prompt.texts)
	// The typed body is kept like an editor draft: reported when the command fails, removed when it succeeds.
	requireDraft(t, draft.Path, true)
	failed := errors.New("create failed")
	finishDraft(&draft, &failed)
	require.ErrorContains(t, failed, "draft saved at "+draft.Path)
	requireDraft(t, draft.Path, true)
	var succeeded error
	finishDraft(&draft, &succeeded)
	require.NoError(t, succeeded)
	requireDraft(t, draft.Path, false)

	editor, path := newDraftEditor(t, "edited body")
	deps.Editor = editor
	deps.Getenv = envFunc(map[string]string{"EDITOR": "vi"})
	a, cmd = outputApplication(deps, "")
	draft, err = a.bodyEditor(cmd, terminal.BodyTitle)(context.Background(), "## Steps")
	require.NoError(t, err)
	require.Equal(t, terminal.Draft{Path: path, Body: "edited body"}, draft)
	require.Equal(t, "## Steps", editor.initial)
	require.Len(t, prompt.texts, 1)
}
