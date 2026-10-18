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
	args := []string{"pr", "review", "1", "-R", "owner/repo", "--event", "REQUEST_CHANGES"}
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
		{[]string{"--event", "approve"}, "APPROVED", ""},
		{[]string{"--event", "request_changes", "-b", "x"}, "REQUEST_CHANGES", "x"},
		{[]string{"--event", "Comment", "-b", "x"}, "COMMENT", "x"},
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

func TestReviewUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "review", "13"}, "choose a review type: --approve, --request-changes, or --comment\nUsage: gt pr review [<number> | <url> | <branch>] [flags]\nRun 'gt pr review --help' for examples."},
		{[]string{"pr", "review", "create", "13"}, "Run 'gt pr review create --help' for examples."},
		{[]string{"pr", "review", "13", "--body", "x"}, "choose a review type"},
		{[]string{"pr", "review", "13", "-a", "-c"}, "choose one review type"},
		{[]string{"pr", "review", "13", "--approve", "--event", "comment"}, "choose one review type"},
		{[]string{"pr", "review", "13", "--event", "bogus"}, "--event must be"},
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
