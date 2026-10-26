// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

// pullJSON answers GET /pulls/N for review tests: pull request 13 "Add login" by author.
func pullJSON(author string) string {
	return fmt.Sprintf(`{"number":13,"title":"Add login","user":{"login":%q},"head":{"ref":"feat/login"},"base":{"ref":"main"}}`, author)
}

func TestReviewReplyPostsBodyFromStdin(t *testing.T) {
	mutations := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations++
		require.Equal(t, "token token", r.Header.Get("Authorization"))
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/installation/api/v1/repos/owner/repo/pulls/1/comments/7/replies", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, map[string]string{"body": "Reply from stdin"}, body)
		_, _ = w.Write([]byte(`{"id":9,"body":"Reply from stdin","user":{"login":"alice"},"pull_request_review_id":3}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL+"/installation")
	deps.Streams.In = strings.NewReader("Reply from stdin")
	err := Execute(context.Background(), deps, []string{"pr", "review", "reply", "1", "7", "-R", "owner/repo", "--body-file=-", "--json=id,review_id,author"})
	require.NoError(t, err)
	require.Equal(t, 1, mutations)
	require.JSONEq(t, `{"id":9,"review_id":3,"author":"alice"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestReviewPromptAndFlagsReachIdenticalOptions(t *testing.T) {
	var payloads []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls/1" {
			_, _ = w.Write([]byte(`{"number":1,"title":"Add login","user":{"login":"bob"}}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls/1/reviews" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews", r.URL.Path)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"id":7,"state":"REQUEST_CHANGES","user":{"login":"alice"},"html_url":"https://example.test/review/7"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	args := []string{"pr", "review", "1", "-R", "owner/repo", "--request-changes"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []string{"Body (markdown): "}, prompt.texts)
	require.Empty(t, prompt.confirmations, "no confirmation right after typing the body")
	require.Equal(t, "https://example.test/review/7\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--body", "typed body", "--json=id,author,url")))
	require.Len(t, prompt.texts, 1)
	require.Equal(t, payloads[0], payloads[1])
	require.JSONEq(t, `{"id":7,"author":"alice","url":"https://example.test/review/7"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestReviewTypeFlags(t *testing.T) {
	for _, tc := range []struct {
		args        []string
		event, body string
	}{
		{[]string{"--approve"}, "APPROVED", ""},
		{[]string{"-a", "-b", "LGTM"}, "APPROVED", "LGTM"},
		{[]string{"-r", "--body", "Hash it"}, "REQUEST_CHANGES", "Hash it"},
		{[]string{"-c", "-b", "Nit"}, "COMMENT", "Nit"},
		{[]string{"--pending", "-b", "Draft"}, "PENDING", "Draft"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var payload map[string]any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews" {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				if r.Method == http.MethodGet {
					require.Equal(t, "/api/v1/repos/alice/x/pulls/13", r.URL.Path)
					_, _ = w.Write([]byte(pullJSON("bob")))
					return
				}
				require.Equal(t, "/api/v1/repos/alice/x/pulls/13/reviews", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				_, _ = fmt.Fprintf(w, `{"id":7,"state":%q,"html_url":"http://gitea/alice/x/pulls/13#pullrequestreview-7"}`, payload["event"])
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
			deps.Streams.OutTTY = true
			require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "review", "13", "-R", "alice/x"}, tc.args...)))
			require.Equal(t, tc.event, payload["event"])
			require.Equal(t, tc.body, payload["body"])
			require.Equal(t, "http://gitea/alice/x/pulls/13#pullrequestreview-7\n", out.String())
			require.Equal(t, "✓ "+reviewVerbs[tc.event]+" pull request alice/x#13 (Add login)\n", diagnostics.String())
		})
	}
}

func TestReviewCreateIsTheReviewCommand(t *testing.T) {
	var event any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(pullJSON("bob")))
			return
		}
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		event = payload["event"]
		_, _ = w.Write([]byte(`{"id":7,"state":"APPROVED"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "create", "#13", "-R", "alice/x", "-a"}))
	require.Equal(t, "APPROVED", event)
}

func TestReviewAsksBeforeFoldingAPendingReview(t *testing.T) {
	var bodies []any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews" && r.URL.Query().Get("page") > "1":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
			_, _ = w.Write([]byte(`[{"id":9,"state":"PENDING","body":"Draft notes","comments_count":1,"user":{"login":"alice"}}]`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(pullJSON("bob")))
		default:
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			bodies = append(bodies, payload["body"])
			_, _ = fmt.Fprintf(w, `{"id":9,"state":%q}`, payload["event"])
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "-a", "-b", "LGTM", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "publish your pending review 9 on alice/x#13 and its inline comment with this approval (or submit it with gt pr review submit 13)")
	require.Empty(t, bodies)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "-a", "-b", "LGTM", "--yes"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "-a", "--yes"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "--pending", "-b", "More", "--no-input"}))
	require.Equal(t, []any{"LGTM", "Draft notes", "More"}, bodies)
}

func TestReviewKeepsThePendingBodyWithoutANewOne(t *testing.T) {
	for _, mode := range []string{"editor", "form"} {
		t.Run(mode, func(t *testing.T) {
			var bodies []any
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews" && r.URL.Query().Get("page") > "1":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					_, _ = w.Write([]byte(`[{"id":9,"state":"PENDING","body":"Draft notes","user":{"login":"alice"}}]`))
				case r.Method == http.MethodGet:
					_, _ = w.Write([]byte(pullJSON("bob")))
				default:
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					bodies = append(bodies, payload["body"])
					_, _ = fmt.Fprintf(w, `{"id":9,"state":%q}`, payload["event"])
				}
			}))
			defer server.Close()
			deps, _, diagnostics := serverDeps(t, server.URL)
			args := []string{"pr", "review", "13", "-R", "alice/x", "--yes"}
			if mode == "editor" {
				editor, _ := newDraftEditor(t, "")
				deps.Editor = editor
				deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
				args = append(args, "-a", "-e")
			} else {
				deps.ReviewForm = reviewFormFunc(func(context.Context, terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
					return terminal.ReviewFormValues{Event: "APPROVED"}, nil
				})
				deps.Prompter = &scriptedPrompter{accept: true}
				deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
				args = []string{"pr", "review", "13", "-R", "alice/x", "--interactive"}
			}
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, []any{"Draft notes"}, bodies)
			require.NotContains(t, diagnostics.String(), "Replaced")
		})
	}
}

func TestCommentNeedsNoBodyToPublishAPendingReview(t *testing.T) {
	for _, mode := range []string{"flags", "editor", "form", "no pending review"} {
		t.Run(mode, func(t *testing.T) {
			var events, bodies []any
			pending := `[{"id":9,"state":"PENDING","body":"Draft notes","comments_count":1,"user":{"login":"alice"}}]`
			if mode == "no pending review" {
				pending = `[]`
			}
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					writeFirstPage(w, r, pending)
				case r.Method == http.MethodGet:
					_, _ = w.Write([]byte(pullJSON("bob")))
				default:
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					events, bodies = append(events, payload["event"]), append(bodies, payload["body"])
					_, _ = fmt.Fprintf(w, `{"id":9,"state":%q}`, payload["event"])
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			args := []string{"pr", "review", "13", "-R", "alice/x", "-c", "--yes"}
			var draft string
			switch mode {
			case "editor", "no pending review":
				var editor *draftEditor
				editor, draft = newDraftEditor(t, "")
				deps.Editor = editor
				deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
				args = append(args, "-e")
			case "form":
				deps.ReviewForm = reviewFormFunc(func(_ context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
					require.True(t, choices.Pending, "the pending review gives the comment its content")
					return terminal.ReviewFormValues{Event: "COMMENT"}, nil
				})
				deps.Prompter = &scriptedPrompter{accept: true}
				deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
				args = []string{"pr", "review", "13", "-R", "alice/x", "--interactive"}
			}
			err := Execute(context.Background(), deps, args)
			if mode == "no pending review" {
				require.EqualError(t, err, "a comment needs a body, and the editor left it empty")
				require.Empty(t, events)
				requireDraft(t, draft, false)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []any{"COMMENT"}, events)
			require.Equal(t, []any{"Draft notes"}, bodies, "the pending body is kept")
		})
	}
}

func TestCommentPublishesAPendingReviewThatHasOnlyInlineComments(t *testing.T) {
	for _, mode := range []string{"flags", "editor", "editor refused", "form"} {
		t.Run(mode, func(t *testing.T) {
			var posts []string
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					writeFirstPage(w, r, `[{"id":9,"state":"PENDING","body":"","comments_count":1,"user":{"login":"alice"}}]`)
				case r.Method == http.MethodGet:
					_, _ = w.Write([]byte(pullJSON("bob")))
				default:
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					posts = append(posts, fmt.Sprintf("%s %s %v %q", r.Method, r.URL.Path, payload["event"], payload["body"]))
					if mode == "editor refused" {
						w.WriteHeader(http.StatusUnprocessableEntity)
						_, _ = w.Write([]byte(`{"message":"refused"}`))
						return
					}
					_, _ = fmt.Fprintf(w, `{"id":9,"state":%q}`, payload["event"])
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			args := []string{"pr", "review", "13", "-R", "alice/x", "-c", "--yes"}
			var draft string
			switch mode {
			case "editor", "editor refused":
				var editor *draftEditor
				editor, draft = newDraftEditor(t, "")
				deps.Editor = editor
				deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
				args = append(args, "-e")
			case "form":
				deps.ReviewForm = reviewFormFunc(func(_ context.Context, choices terminal.ReviewFormChoices) (terminal.ReviewFormValues, error) {
					require.True(t, choices.Pending, "the pending review gives the comment its content")
					return terminal.ReviewFormValues{Event: "COMMENT"}, nil
				})
				deps.Prompter = &scriptedPrompter{accept: true}
				deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
				args = []string{"pr", "review", "13", "-R", "alice/x", "--interactive"}
			}
			err := Execute(context.Background(), deps, args)
			if mode == "editor refused" {
				require.ErrorContains(t, err, "refused")
				require.NotContains(t, err.Error(), "draft saved", "an empty draft is not kept")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, []string{`POST /api/v1/repos/alice/x/pulls/13/reviews/9 COMMENT ""`}, posts, "the pending review is submitted")
			if draft != "" {
				requireDraft(t, draft, false)
			}
		})
	}
}

func TestCommentWithAnEmptyBodyPublishesAPendingReviewWithInlineComments(t *testing.T) {
	for _, tc := range []struct {
		name, pending string
		want          string
	}{
		{"inline comments", `[{"id":9,"state":"PENDING","body":"Draft notes","comments_count":1,"user":{"login":"alice"}}]`, ""},
		// The empty body replaces the pending one, which leaves nothing to publish.
		{"only a body", `[{"id":9,"state":"PENDING","body":"Draft notes","user":{"login":"alice"}}]`, `-b "" would clear the body of pending review 9 and leave it empty; leave out --body to keep its body`},
		{"no pending review", `[]`, "a comment needs a body; pass --body, --body-file or --editor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts []string
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					writeFirstPage(w, r, tc.pending)
				case r.Method == http.MethodGet:
					_, _ = w.Write([]byte(pullJSON("bob")))
				default:
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					posts = append(posts, fmt.Sprintf("%s %s %v %q", r.Method, r.URL.Path, payload["event"], payload["body"]))
					_, _ = fmt.Fprintf(w, `{"id":9,"state":%q}`, payload["event"])
				}
			}))
			defer server.Close()
			deps, _, diagnostics := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "-c", "-b", "", "--yes"})
			if tc.want != "" {
				require.EqualError(t, err, tc.want)
				require.Equal(t, clierr.Usage, clierr.ExitCode(err))
				require.Empty(t, posts)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{`POST /api/v1/repos/alice/x/pulls/13/reviews/9 COMMENT ""`}, posts, "the pending review is submitted")
			require.Contains(t, diagnostics.String(), "Replaced the earlier body of your pending review 9")
		})
	}
}

func TestReviewWarnsWhenItReplacesThePendingBody(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews" && r.URL.Query().Get("page") > "1":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
			_, _ = w.Write([]byte(`[{"id":9,"state":"PENDING","body":"Draft notes","user":{"login":"alice"}}]`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(pullJSON("bob")))
		default:
			_, _ = w.Write([]byte(`{"id":9,"state":"PENDING"}`))
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		warn bool
	}{
		{[]string{"--pending", "-b", "More"}, true},
		{[]string{"-c", "-b", "More", "--yes"}, true},
		{[]string{"--pending", "-b", "Draft notes"}, false},
	} {
		deps, _, diagnostics := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "review", "13", "-R", "alice/x"}, tc.args...)))
		if tc.warn {
			require.Contains(t, diagnostics.String(), "Replaced the earlier body of your pending review 9", tc.args)
		} else {
			require.NotContains(t, diagnostics.String(), "Replaced", tc.args)
		}
	}
}

func TestReviewExplainsAnUnknownCommit(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(pullJSON("bob")))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":""}`))
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "-a", "--commit", "deadbeef"})
	require.EqualError(t, err, "commit deadbeef not found in pull request #13")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
}

func TestReviewViewNamesTheMissingReviewOrPullRequest(t *testing.T) {
	for _, tc := range []struct {
		message    string
		repoExists bool
		want       string
	}{
		{"review does not exist [id: 1]", false, "review 1 not found in pull request #20"},
		{"pull request does not exist [id: 0, issue_id: 0, head_repo_id: 0, base_repo_id: 83, head_branch: , base_branch: ]", false, "pull request #20 not found in alice/x"},
		// Gitea answers a missing repository and a review of another pull request alike.
		{"not found", false, "repository alice/x not found"},
		{"not found", true, "review 1 not found in pull request #20"},
	} {
		server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/repos/alice/x" && tc.repoExists {
				_, _ = w.Write([]byte(`{"full_name":"alice/x"}`))
				return
			}
			if r.URL.Path == "/api/v1/repos/alice/x/issues/20" && tc.repoExists {
				_, _ = w.Write([]byte(`{"number":20,"pull_request":{"merged":false}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
		}))
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, []string{"pr", "review", "view", "20", "1", "-R", "alice/x"})
		server.Close()
		require.EqualError(t, err, tc.want)
	}
}

func TestReviewCommentCommandsNameTheMissingCommentOrRepository(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		message    string
		repoExists bool
		want       string
	}{
		{[]string{"pr", "review", "resolve", "9"}, "comment does not exist [id: 9, issue_id: 0]", true, "review comment 9 not found in alice/x"},
		{[]string{"pr", "review", "unresolve", "9"}, "comment does not exist [id: 9, issue_id: 0]", true, "review comment 9 not found in alice/x"},
		{[]string{"pr", "review", "resolve", "9"}, "not found", false, "repository alice/x not found"},
	} {
		t.Run(strings.Join(tc.args, " ")+" "+tc.message, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/repos/alice/x" && tc.repoExists {
					_, _ = w.Write([]byte(`{"full_name":"alice/x"}`))
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append(tc.args, "-R", "alice/x"))
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestReviewReplyNamesTheMissingPullRequestOrComment(t *testing.T) {
	for _, tc := range []struct {
		repoExists, pullExists bool
		want                   string
	}{
		{true, true, "review comment 9 not found in pull request #20 of alice/x"},
		{true, false, "pull request #20 not found in alice/x"},
		{false, false, "repository alice/x not found"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/repos/alice/x" && tc.repoExists:
					_, _ = w.Write([]byte(`{"full_name":"alice/x","has_pull_requests":true}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/20" && tc.pullExists:
					_, _ = w.Write([]byte(pullJSON("bob")))
				default:
					// Gitea answers a reply to a missing comment, to a comment on another pull
					// request and on a missing pull request alike.
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "review", "reply", "20", "9", "-b", "x", "-R", "alice/x"})
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestReviewUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "review", "13"}, "choose a review type: --approve, --request-changes, or --comment\nUsage: gt pr review [<number> | <url> | <branch>] [flags]\nRun 'gt pr review --help' for examples."},
		{[]string{"pr", "review", "create", "13"}, "Run 'gt pr review create --help' for examples."},
		{[]string{"pr", "review", "13", "--body", "x"}, "choose a review type"},
		{[]string{"pr", "review", "13", "-a", "-c"}, "choose one review type"},
		{[]string{"pr", "review", "13", "--approve", "--comment"}, "choose one review type"},
		{[]string{"pr", "review", "approve", "13"}, `unknown review command "approve"; to approve run: gt pr review 13 --approve (see gt pr review --help)`},
		{[]string{"pr", "review", "reject"}, `to request changes run: gt pr review <number> --request-changes`},
		{[]string{"pr", "review", "lst", "13"}, `unknown command "lst" for "gt pr review"; did you mean list?`},
		{[]string{"pr", "review", "13", "14", "-a"}, "too many arguments"},
		{[]string{"pr", "review", "submit", "13"}, "choose a review type"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			requireUsageBeforeRequests(t, tc.want, tc.args...)
		})
	}
}

func TestReviewRefusesOwnApprovalBeforeTheEditor(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "no review may be submitted")
		_, _ = w.Write([]byte(pullJSON("alice")))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "vi"})
	editor := &failingPathEditor{}
	deps.Editor = editor
	err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "--approve", "--editor"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "you can't approve or request changes on your own pull request #13; use --comment")
}

func TestReviewNeedsABodyWithoutPrompts(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "no review may be submitted")
		_, _ = w.Write([]byte(pullJSON("bob")))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "--request-changes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "a change request needs a body; pass --body, --body-file or --editor")
}

func TestReviewSubmitFindsThePendingReview(t *testing.T) {
	for _, tc := range []struct {
		name, reviews, want string
	}{
		{"one", `[{"id":5,"state":"APPROVED","user":{"login":"alice"}},{"id":6,"state":"PENDING","user":{"login":"bob"}},{"id":7,"state":"PENDING","user":{"login":"alice"}}]`, ""},
		{"none", `[{"id":6,"state":"PENDING","user":{"login":"bob"}}]`, "no pending review by alice on alice/x#13; to post a new review run: gt pr review 13 --approve"},
		{"several", `[{"id":7,"state":"PENDING","user":{"login":"alice"}},{"id":8,"state":"PENDING","user":{"login":"alice"}}]`, "alice has several pending reviews on alice/x#13 (7, 8); pass a review ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			submitted := ""
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13":
					_, _ = w.Write([]byte(pullJSON("bob")))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews" && r.URL.Query().Get("page") == "1":
					_, _ = w.Write([]byte(tc.reviews))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					_, _ = w.Write([]byte(`[]`))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews/7" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(`{"id":7,"state":"PENDING","body":"First pass"}`))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews/7":
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					submitted = fmt.Sprint(payload["event"], " ", payload["body"])
					_, _ = w.Write([]byte(`{"id":7,"state":"APPROVED"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "review", "submit", "13", "-R", "alice/x", "--approve"})
			if tc.want != "" {
				require.EqualError(t, err, tc.want)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "APPROVED First pass", submitted)
		})
	}
}

func TestReviewSubmitCommentWithAnEmptyBody(t *testing.T) {
	for _, tc := range []struct {
		name, review, want string
	}{
		{"only a body", `{"id":9,"state":"PENDING","body":"Draft notes"}`, `-b "" would clear the body of pending review 9 and leave it empty; leave out --body to keep its body`},
		{"inline comments", `{"id":9,"state":"PENDING","body":"Draft notes","comments_count":1}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts []string
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13":
					_, _ = w.Write([]byte(pullJSON("bob")))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews/9" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(tc.review))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews/9":
					var payload map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
					posts = append(posts, fmt.Sprintf("%v %q", payload["event"], payload["body"]))
					_, _ = w.Write([]byte(`{"id":9,"state":"COMMENT"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
			}))
			defer server.Close()
			deps, _, diagnostics := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "review", "submit", "13", "9", "-R", "alice/x", "--comment", "-b", ""})
			if tc.want != "" {
				require.EqualError(t, err, tc.want)
				require.Equal(t, clierr.Usage, clierr.ExitCode(err))
				require.Empty(t, posts, "the empty review is refused before the request")
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{`COMMENT ""`}, posts)
			require.Contains(t, diagnostics.String(), "Replaced the earlier body of your pending review 9", "as gt pr review warns")
		})
	}
}

func TestReviewSubmitCommentRefusesABlankBodyThatClearsAPendingReview(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		editor string
		want   string
	}{
		{"blank body", []string{"-b", "   "}, "", `a blank --body would clear the body of pending review 9 and leave it empty; leave out --body to keep its body`},
		{"emptied in the editor", []string{"-e"}, "", "the editor left the body empty, which would clear the body of pending review 9 and leave it empty; keep its body to submit it"},
		{"blanked in the editor", []string{"-e"}, " \n", "the editor left the body empty, which would clear the body of pending review 9 and leave it empty; keep its body to submit it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13":
					_, _ = w.Write([]byte(pullJSON("bob")))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews/9":
					_, _ = w.Write([]byte(`{"id":9,"state":"PENDING","body":"Draft notes"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
			}))
			defer server.Close()
			deps, _, _ := serverDeps(t, server.URL)
			editor, path := newDraftEditor(t, tc.editor)
			deps.Editor = editor
			deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
			err := Execute(context.Background(), deps, append([]string{"pr", "review", "submit", "13", "9", "-R", "alice/x", "--comment"}, tc.args...))
			require.EqualError(t, err, tc.want, "the empty review is refused before the request")
			if tc.args[0] == "-e" {
				requireDraft(t, path, false)
			}
		})
	}
}

func TestCommentWithABlankBodyNamesItBlank(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
			writeFirstPage(w, r, `[{"id":9,"state":"PENDING","body":"Draft notes","user":{"login":"alice"}}]`)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(pullJSON("bob")))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "-c", "-b", "  ", "--yes"})
	require.EqualError(t, err, `a blank --body would clear the body of pending review 9 and leave it empty; leave out --body to keep its body`)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
}

func TestReviewListTable(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/x/pulls/13/reviews", r.URL.Path)
		_, _ = w.Write([]byte(`[{"id":5,"state":"COMMENT","body":"Self note","user":{"login":"alice"},"submitted_at":"2026-10-11T10:00:00Z"},
			{"id":6,"state":"APPROVED","dismissed":true,"user":{"login":"bob"},"submitted_at":"2026-10-11T11:00:00Z","comments_count":2}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	deps.Streams.OutTTY = true
	deps.Now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "list", "13", "-R", "alice/x"}))
	require.Equal(t, `Showing 2 of 2 reviews on alice/x#13

ID  REVIEWER  STATE                 SUBMITTED          COMMENTS  BODY
5   alice     Commented             about 2 hours ago  0         Self note
6   bob       Approved (dismissed)  about 1 hour ago   2
`, out.String())
}

func TestReviewCommentsFileValidatedBeforeRequests(t *testing.T) {
	invalidPosition := "each inline comment requires path, body, and exactly one positive old_position or new_position"
	for _, tc := range []struct{ content, want string }{
		{`null`, "inline comments must be a JSON array"},
		{`[{}]`, invalidPosition},
		{`[{"path":"x","body":"b","new_position":1,"old_position":1}]`, invalidPosition},
		{`[] []`, "inline comments file must contain exactly one JSON array"},
		{`[{"unexpected":true}]`, `unknown field "unexpected"`},
	} {
		t.Run(tc.content, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "comments.json")
			require.NoError(t, os.WriteFile(file, []byte(tc.content), 0o600))
			requireUsageBeforeRequests(t, tc.want, "pr", "review", "1", "--comments-file", file, "--comment", "--body", "Supplied")
		})
	}
}

func TestReviewCommentsFileMustBeOnTheDiff(t *testing.T) {
	const diff = "diff --git a/e.txt b/e.txt\n--- a/e.txt\n+++ b/e.txt\n@@ -1,2 +1,2 @@\n same\n-old\n+new\n" +
		"diff --git a/gone.txt b/gone.txt\n--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n" +
		"diff --git \"a/\\303\\244.txt\" \"b/\\303\\244.txt\"\n--- \"a/\\303\\244.txt\"\n+++ \"b/\\303\\244.txt\"\n@@ -0,0 +1 @@\n+hi\n"
	for _, tc := range []struct{ comment, want string }{
		{`{"path":"e.txt","body":"x","new_position":2}`, ""},
		{`{"path":"e.txt","body":"x","old_position":1}`, ""},
		{`{"path":"gone.txt","body":"x","old_position":1}`, ""},
		{`{"path":"ä.txt","body":"x","new_position":1}`, ""},
		{`{"path":"nosuch.txt","body":"x","new_position":1}`, `inline comment on "nosuch.txt": pull request #13 does not change that file`},
		{`{"path":"e.txt","body":"x","new_position":99}`, `inline comment on "e.txt": new_position 99 is not a line of the diff of pull request #13`},
		{`{"path":"e.txt","body":"x","old_position":2,"new_position":0}`, ""},
		{`{"path":"e.txt","body":"x","old_position":3}`, `inline comment on "e.txt": old_position 3 is not a line of the diff of pull request #13`},
	} {
		t.Run(tc.comment, func(t *testing.T) {
			created := false
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13":
					_, _ = w.Write([]byte(`{"number":13,"user":{"login":"bob"}}`))
				case r.URL.Path == "/api/v1/repos/alice/x/pulls/13.diff":
					_, _ = w.Write([]byte(diff))
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/x/pulls/13/reviews":
					created = true
					_, _ = w.Write([]byte(`{"id":1,"state":"COMMENT"}`))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "comments.json")
			require.NoError(t, os.WriteFile(file, []byte("["+tc.comment+"]"), 0o600))
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "review", "13", "-R", "alice/x", "--comment", "--comments-file", file})
			if tc.want == "" {
				require.NoError(t, err)
				require.True(t, created)
				return
			}
			require.EqualError(t, err, tc.want)
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			require.False(t, created, "nothing is created")
		})
	}
}

func TestReviewDismissChecksTheReviewStateFirst(t *testing.T) {
	mutations := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		_, _ = w.Write([]byte(`{"id":2,"state":"COMMENT","user":{"login":"bob"}}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "review", "dismiss", "1", "2", "-R", "owner/repo", "--body", "Reason", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "only approvals and change requests can be dismissed")
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "undismiss", "1", "2", "-R", "owner/repo"}))
	require.Contains(t, diagnostics.String(), "is not dismissed")
	require.Zero(t, mutations)
}

func TestReviewDismissOpensNoEditorForAnUndismissableReview(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"id":2,"state":"COMMENT","user":{"login":"bob"}}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	editor, _ := newDraftEditor(t, "Reason")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
	err := Execute(context.Background(), deps, []string{"pr", "review", "dismiss", "1", "2", "-R", "alice/project", "--editor", "--no-input", "--yes"})
	require.ErrorContains(t, err, "only approvals and change requests can be dismissed")
	require.Empty(t, editor.command, "the editor must not open")
}

func TestReviewDeleteAndDismissRequireYesWithoutInput(t *testing.T) {
	mutations := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		_, _ = w.Write([]byte(`{"id":2,"state":"APPROVED","user":{"login":"bob"}}`))
	}))
	defer server.Close()
	for _, tc := range []struct{ verb, want string }{
		{"delete", "confirmation required to delete review 2 by bob on owner/repo#1; rerun with --yes"},
		{"dismiss", "confirmation required to dismiss bob's approval on owner/repo#1; rerun with --yes"},
	} {
		deps, _, _ := serverDeps(t, server.URL)
		args := []string{"pr", "review", tc.verb, "1", "2", "-R", "owner/repo", "--no-input"}
		if tc.verb == "dismiss" {
			args = append(args, "--body", "Reason")
		}
		err := Execute(context.Background(), deps, args)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.EqualError(t, err, tc.want)
	}
	require.Zero(t, mutations)
}

func TestCommentFlagHelpNamesThePendingReview(t *testing.T) {
	for _, args := range [][]string{{"pr", "review"}, {"pr", "review", "create"}, {"pr", "review", "submit"}} {
		deps, out, _ := testDeps(t)
		require.NoError(t, Execute(context.Background(), deps, append(args, "--help")))
		require.Contains(t, out.String(), "Comment without approving (requires a body or inline comments, which can be those of the pending review it publishes; an empty --body clears that review's body when it has inline comments, and is refused otherwise)", args)
	}
}
