// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

type reviewFormFunc func(context.Context, terminal.ReviewFormChoices) (terminal.ReviewFormValues, error)

func (f reviewFormFunc) CreateReview(ctx context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
	return f(ctx, choices)
}

func TestPullReviewImplicitInteractiveForm(t *testing.T) {
	var createPayload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3" {
			_, _ = w.Write([]byte(`{"number":3,"title":"Add one","user":{"login":"bob"},"head":{"ref":"feat/one"},"base":{"ref":"main"}}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/reviews" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/pulls/3/reviews", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
		_, _ = w.Write([]byte(`{"id":11,"state":"APPROVED","body":"Looks good","html_url":"https://example.org/reviews/11","user":{"login":"alice"}}`))
	}))
	defer server.Close()
	calls := 0
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "vi"})
	deps.ReviewForm = reviewFormFunc(func(_ context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
		calls++
		require.Equal(t, `Reviewing #3 "Add one" (feat/one → main) in alice/project`, choices.Header)
		require.False(t, choices.Author)
		require.Equal(t, "vi", choices.Editor)
		require.NotNil(t, choices.ReviewDiff)
		return terminal.ReviewFormValues{
			Event: "APPROVED", Body: "Looks good",
			Comments: []terminal.ReviewCommentDraft{{Path: "sample.go", Body: "nice", NewLine: 2}},
		}, nil
	})
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "3", "-R", "alice/project", "--json=id,state", "--interactive"}))
	require.Equal(t, 1, calls)
	require.Equal(t, []string{"Submit approval for #3?"}, prompt.confirmations)
	require.Equal(t, []bool{true}, prompt.defaults)
	require.Equal(t, "APPROVED", createPayload["event"])
	require.Equal(t, "Looks good", createPayload["body"])
	comments, ok := createPayload["comments"].([]any)
	require.True(t, ok)
	require.Len(t, comments, 1)
	require.JSONEq(t, `{"id":11,"state":"APPROVED"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestPullReviewFormKeepsTheBodyWhenSubmissionIsDeclined(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "nothing may be submitted")
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			writeFirstPage(w, r, `[]`)
			return
		}
		_, _ = w.Write([]byte(`{"number":3,"title":"Add one","user":{"login":"alice"},"head":{"ref":"feat/one"},"base":{"ref":"main"}}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.ReviewForm = reviewFormFunc(func(_ context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
		require.True(t, choices.Author)
		require.Nil(t, choices.ReviewDiff, "annotating the diff needs an editor")
		return terminal.ReviewFormValues{Event: "COMMENT", Body: "Self note"}, nil
	})
	deps.Prompter = &scriptedPrompter{}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	err := Execute(context.Background(), deps, []string{"pr", "review", "3", "-R", "alice/project"})
	require.ErrorIs(t, err, terminal.ErrCanceled)
	path := regexp.MustCompile(`draft saved at (\S+)`).FindStringSubmatch(err.Error())
	require.Len(t, path, 2, err.Error())
	data, readErr := os.ReadFile(path[1])
	require.NoError(t, readErr)
	require.Equal(t, "Self note", string(data))
	require.NoError(t, os.Remove(path[1]))
}

func TestPullReviewInteractiveDiffRoundTrip(t *testing.T) {
	var createPayload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"title":"Add","user":{"login":"bob"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/4.diff":
			_, _ = w.Write([]byte(`diff --git a/sample.go b/sample.go
--- a/sample.go
+++ b/sample.go
@@ -1 +1,2 @@
 package sample
+func added() {}
`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/4/reviews":
			writeFirstPage(w, r, `[]`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/4/reviews":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
			_, _ = w.Write([]byte(`{"id":12,"state":"COMMENT","body":"nits","user":{"login":"alice"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	prompt := &scriptedPrompter{accept: true}
	deps, out, _ := testDeps(t)
	deps.ReviewForm = reviewFormFunc(func(ctx context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
		comments, err := choices.ReviewDiff(ctx)
		require.NoError(t, err)
		require.Len(t, comments, 1)
		require.Equal(t, "sample.go", comments[0].Path)
		require.Equal(t, "annotate added line", comments[0].Body)
		require.EqualValues(t, 2, comments[0].NewLine)
		return terminal.ReviewFormValues{Event: "COMMENT", Body: "nits", Comments: comments}, nil
	})
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Editor = pathAnnotatingEditor{}
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "true"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "4", "-R", "alice/project", "--json=id", "--interactive"}))
	require.Equal(t, "COMMENT", createPayload["event"])
	require.JSONEq(t, `{"id":12}`, out.String())
}

func TestPullReviewDiffFailureKeepsAnnotatedDiff(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/pulls/4" {
			_, _ = w.Write([]byte(`{"number":4,"title":"Add","user":{"login":"bob"}}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/4/reviews" {
			writeFirstPage(w, r, `[]`)
			return
		}
		require.Equal(t, "/api/v1/repos/alice/project/pulls/4.diff", r.URL.Path, "no review may be submitted")
		_, _ = w.Write([]byte("diff --git a/sample.go b/sample.go\n"))
	}))
	defer server.Close()
	editor := &failingPathEditor{}
	deps, _, _ := testDeps(t)
	deps.ReviewForm = reviewFormFunc(func(ctx context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
		_, err := choices.ReviewDiff(ctx)
		return terminal.ReviewFormValues{}, err
	})
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "true"})
	err := Execute(context.Background(), deps, []string{"pr", "review", "4", "-R", "alice/project", "--interactive"})
	require.ErrorContains(t, err, "annotated diff is kept at "+editor.path)
	require.FileExists(t, editor.path)
	require.NoError(t, os.Remove(editor.path))
}

func TestPullReviewDiffRefusesCommentsOffTheDiff(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "nothing may be submitted")
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls/4":
			_, _ = w.Write([]byte(`{"number":4,"title":"Add","user":{"login":"bob"}}`))
		case "/api/v1/repos/alice/project/pulls/4/reviews":
			writeFirstPage(w, r, `[]`)
		default:
			_, _ = w.Write([]byte("diff --git a/sample.go b/sample.go\n--- a/sample.go\n+++ b/sample.go\n@@ -1 +1,2 @@\n package sample\n+func added() {}\n"))
		}
	}))
	defer server.Close()
	editor := &headerEditingEditor{}
	deps, _, _ := testDeps(t)
	deps.ReviewForm = reviewFormFunc(func(ctx context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
		_, err := choices.ReviewDiff(ctx)
		return terminal.ReviewFormValues{}, err
	})
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "true"})
	err := Execute(context.Background(), deps, []string{"pr", "review", "4", "-R", "alice/project", "--interactive"})
	require.ErrorContains(t, err, `line 20 of the annotated diff, "+++ b/other.go", is not the next line of the diff, "+++ b/sample.go"; keep the lines of the diff as they are and start every comment line with #; the annotated diff is kept at `+editor.path)
	require.NoError(t, os.Remove(editor.path))
}

// headerEditingEditor comments on a line after changing the file header above it.
type headerEditingEditor struct{ path string }

func (*headerEditingEditor) Edit(context.Context, string, string) (terminal.Draft, error) {
	return terminal.Draft{}, nil
}

func (e *headerEditingEditor) EditPath(_ context.Context, _, path string) error {
	e.path = path
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	annotated := strings.NewReplacer("+++ b/sample.go\n", "+++ b/other.go\n", "+func added() {}\n", "+func added() {}\n# off the diff\n").Replace(string(data))
	return os.WriteFile(path, []byte(annotated), 0o600)
}

type failingPathEditor struct{ path string }

func (*failingPathEditor) Edit(context.Context, string, string) (terminal.Draft, error) {
	return terminal.Draft{}, nil
}

func (e *failingPathEditor) EditPath(_ context.Context, _, path string) error {
	e.path = path
	return errors.New("editor exited with status 1")
}

type pathAnnotatingEditor struct{}

func (pathAnnotatingEditor) Edit(context.Context, string, string) (terminal.Draft, error) {
	return terminal.Draft{}, nil
}

func (pathAnnotatingEditor) EditPath(_ context.Context, _, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	annotated := strings.Replace(string(data), "+func added() {}\n", "+func added() {}\n# annotate added line\n", 1)
	return os.WriteFile(path, []byte(annotated), 0o600)
}

func TestPullReviewKeepsAnnotatedDiffWhenSubmissionIsDeclined(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "nothing may be submitted")
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			writeFirstPage(w, r, `[]`)
			return
		}
		if r.URL.Path == "/api/v1/repos/alice/project/pulls/4" {
			_, _ = w.Write([]byte(`{"number":4,"title":"Add","user":{"login":"bob"}}`))
			return
		}
		_, _ = w.Write([]byte("diff --git a/sample.go b/sample.go\n--- a/sample.go\n+++ b/sample.go\n@@ -1 +1,2 @@\n package sample\n+func added() {}\n"))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.ReviewForm = reviewFormFunc(func(ctx context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
		comments, err := choices.ReviewDiff(ctx)
		require.NoError(t, err)
		return terminal.ReviewFormValues{Event: "COMMENT", Comments: comments}, nil
	})
	deps.Prompter = &scriptedPrompter{}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Editor = pathAnnotatingEditor{}
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "true"})
	err := Execute(context.Background(), deps, []string{"pr", "review", "4", "-R", "alice/project", "--interactive"})
	require.ErrorIs(t, err, terminal.ErrCanceled)
	path := regexp.MustCompile(`the annotated diff is kept at (\S+)`).FindStringSubmatch(err.Error())
	require.Len(t, path, 2, err.Error())
	data, readErr := os.ReadFile(path[1])
	require.NoError(t, readErr)
	require.Contains(t, string(data), "# annotate added line")
	require.NoError(t, os.Remove(path[1]))
}
