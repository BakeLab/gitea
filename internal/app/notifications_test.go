// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestNotificationBoundsShareClockAndReachServer(t *testing.T) {
	now := time.Date(2028, 2, 1, 12, 0, 0, 0, time.UTC)
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/notifications", r.URL.Path)
		require.Equal(t, "2028-01-31T12:00:00Z", r.URL.Query().Get("since"))
		require.Equal(t, "2028-02-01T11:00:00Z", r.URL.Query().Get("before"))
		require.Equal(t, []string{"unread", "pinned"}, r.URL.Query()["status-types"])
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	for _, test := range []struct{ flag, warning string }{
		{"--until=1h", ""},
		{"--before=1h", "! --before is deprecated; use --until\n"},
	} {
		calls = 0
		deps, out, diagnostics := serverDeps(t, server.URL)
		reads := 0
		deps.Now = func() time.Time { reads++; return now }
		err := Execute(context.Background(), deps, []string{"notification", "list", "--since=24h", test.flag, "--json"})
		require.NoError(t, err)
		require.Equal(t, 1, reads)
		require.Equal(t, 1, calls)
		require.JSONEq(t, `[]`, out.String())
		require.Equal(t, test.warning, withoutANSI(diagnostics.String()))
	}
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--help"}))
	require.Contains(t, out.String(), "--until string")
	require.NotContains(t, out.String(), "--before")
}

// notificationThreads is a page of two notification threads as Gitea sends them.
const notificationThreads = `[
	{"id":12,"unread":true,"repository":{"full_name":"alice/project"},"updated_at":"2026-10-11T18:00:00Z",
	 "subject":{"title":"Improve greeting","type":"Pull","state":"open","html_url":"https://gitea.test/alice/project/pulls/17","url":"https://gitea.test/api/v1/repos/alice/project/issues/17"}},
	{"id":3,"unread":false,"pinned":true,"repository":{"full_name":"alice/project"},"updated_at":"2026-10-11T17:00:00Z",
	 "subject":{"title":"Improve error messages","type":"Issue","state":"closed","html_url":"https://gitea.test/alice/project/issues/8","url":"https://gitea.test/api/v1/repos/alice/project/issues/8"}}
]`

func TestNotificationListNamesItsFiltersWhenEmpty(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--type", "issue", "-R", "alice/project"}))
	require.Contains(t, withoutANSI(out.String()+diagnostics.String()), "no unread or pinned notifications match your filters in alice/project")
}

func TestNotificationsListTheInboxByDefault(t *testing.T) {
	var path atomic.Value
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.Path)
		w.Header().Set("X-Total-Count", "2")
		_, _ = w.Write([]byte(notificationThreads))
	}))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		git  map[string]string
		path string
	}{
		{nil, nil, "/api/v1/notifications"},
		{nil, map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git"}, "/api/v1/notifications"},
		{[]string{"-R", "alice/project"}, nil, "/api/v1/repos/alice/project/notifications"},
		{[]string{"--this-repo"}, map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/bob/fork.git"}, "/api/v1/repos/bob/fork/notifications"},
	} {
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = &fakeGit{results: tc.git}
		require.NoError(t, Execute(context.Background(), deps, append([]string{"notification", "list"}, tc.args...)), tc.args)
		require.Equal(t, tc.path, path.Load(), tc.args)
	}
	deps, out, _ := serverDeps(t, server.URL)
	deps.Streams.OutTTY, deps.Streams.Width = true, 100
	deps.Now = func() time.Time { return testNow }
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list"}))
	require.Equal(t, `Showing 2 of 2 unread or pinned notifications in your inbox

ID  TYPE   REPOSITORY     SUBJECT                    STATE   UPDATED
12  pull   alice/project  #17 Improve greeting       open    about 2 hours ago
3   issue  alice/project  #8 Improve error messages  closed  about 3 hours ago
`, stripLinks(withoutANSI(out.String())))
	deps, out, _ = serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list"}))
	require.Equal(t, "12\tpull\talice/project\t#17 Improve greeting\topen\tunread\t2026-10-11T18:00:00Z\thttps://gitea.test/alice/project/pulls/17\n"+
		"3\tissue\talice/project\t#8 Improve error messages\tclosed\tpinned\t2026-10-11T17:00:00Z\thttps://gitea.test/alice/project/issues/8\n", out.String())
}

func TestNotificationWatchStreamsOneJSONObjectPerLine(t *testing.T) {
	var polls atomic.Int32
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if polls.Add(1) == 1 {
			_, _ = w.Write([]byte(`[{"id":1,"subject":{"title":"First"}}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"subject":{"title":"First"}},{"id":2,"subject":{"title":"Second"}}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "watch", "--json", "id,title", "--interval", "20ms", "--timeout", "500ms"}))
	require.Equal(t, "{\"id\":1,\"title\":\"First\"}\n{\"id\":2,\"title\":\"Second\"}\n", out.String())
	polls.Store(0)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "watch", "--csv", "id,title", "--interval", "20ms", "--timeout", "500ms"}))
	require.Equal(t, "id,title\n1,First\n2,Second\n", out.String())
}

func TestNotificationWatchFailsWhenTheInstanceDoesNotAnswer(t *testing.T) {
	release := make(chan struct{})
	server := newAuthenticatedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer server.Close()
	defer close(release)
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_HTTP_TIMEOUT": "50ms"})
	err := Execute(context.Background(), deps, []string{"notification", "watch", "--json", "id", "--interval", "20ms", "--timeout", "10s"})
	require.ErrorIs(t, err, transport.ErrTimeout, "a request that runs out of http_timeout is not the end of --timeout")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.Empty(t, out.String())
}

func TestNotificationMutationsReportWhatChanged(t *testing.T) {
	var marks []string
	pinned := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/notifications/threads/12":
			marks = append(marks, r.URL.Query().Get("to-status"))
			pinned = r.URL.Query().Get("to-status") == "pinned"
			_, _ = w.Write([]byte(`{"id":12,"repository":{"full_name":"alice/project"},"subject":{"type":"Pull","url":"https://gitea.test/api/v1/repos/alice/project/issues/17"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/notifications/threads/12":
			_, _ = fmt.Fprintf(w, `{"id":12,"unread":true,"pinned":%t,"repository":{"full_name":"alice/project"},"subject":{"title":"Improve greeting","type":"Pull","state":"open","html_url":"https://gitea.test/alice/project/pulls/17","url":"https://gitea.test/api/v1/repos/alice/project/issues/17","latest_comment_url":"https://gitea.test/api/v1/repos/alice/project/issues/comments/9"}}`, pinned)
		case r.URL.Path == "/api/v1/repos/alice/project/issues/comments/9":
			_, _ = w.Write([]byte(`{"id":9,"body":"Looks good to me","user":{"login":"bob"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/notifications":
			marks = append(marks, "all "+r.URL.Query().Get("to-status"))
			_, _ = w.Write([]byte(`[{"id":12},{"id":3}]`))
		case r.URL.Path == "/api/v1/notifications/threads/999":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pin", "12"}, "✓ Pinned notification 12 (alice/project#17)\n"},
		{[]string{"unpin", "#12"}, "✓ Unpinned notification 12 (alice/project#17)\n"},
		{[]string{"mark-read", "12"}, "✓ Marked notification 12 (alice/project#17) as read\n"},
		{[]string{"mark-read", "--all", "--yes"}, "✓ Marked 2 notifications as read\n"},
	} {
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"notification"}, tc.args...)), tc.args)
		require.Equal(t, tc.want, withoutANSI(diagnostics.String()), tc.args)
	}
	require.Equal(t, []string{"pinned", "read", "read", "all read"}, marks)
	require.EqualError(t, Execute(context.Background(), deps, []string{"notification", "unpin", "12"}), "notification 12: not pinned")
	require.Len(t, marks, 4, "unpinning a thread that is not pinned leaves it unread")
	require.EqualError(t, Execute(context.Background(), deps, []string{"notification", "mark-read", "12", "-R", "bob/other"}), "notification 12: belongs to alice/project, not selected repository bob/other")
	require.EqualError(t, Execute(context.Background(), deps, []string{"notification", "pin", "999"}), "notification 999: not found in your inbox")
	prompt := &scriptedPrompter{accept: true}
	deps.Prompter, deps.Streams.InTTY, deps.Streams.ErrTTY = prompt, true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "pin", "--all"}))
	require.Equal(t, []string{"Pin all unread notifications in your inbox?"}, prompt.confirmations)
	deps.Streams.InTTY, deps.Streams.ErrTTY = false, false
	marks = nil
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "view", "12"}))
	require.Contains(t, withoutANSI(out.String()), "Latest comment: bob\n\n  Looks good to me\n\nView this pull request on Gitea: https://gitea.test/alice/project/pulls/17\n")
	require.Equal(t, []string{"read"}, marks, "viewing marks an unread notification as read")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "view", "12", "--json=unread"}))
	require.JSONEq(t, `{"unread":false}`, withoutANSI(out.String()), "the view shows the state after marking")
}

func TestNotificationMarkAllNamesTheBeforeCutoff(t *testing.T) {
	now := time.Date(2028, 2, 1, 12, 0, 0, 0, time.UTC)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return now }
	for _, flag := range []string{"--until=1h", "--before=1h"} {
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "mark-read", "--all", flag}))
		require.Contains(t, withoutANSI(diagnostics.String()), "No unread notifications updated before 2028-02-01 11:00 in your inbox")
	}
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "unpin", "--help"}))
	require.Contains(t, out.String(), "(default [pinned])")
}

func TestNotificationMarkAllNamesAMissingRepository(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.EqualError(t, Execute(context.Background(), deps, []string{"notification", "mark-read", "--all", "-R", "alice/missing"}), "repository alice/missing not found")
}

func TestNotificationViewSurvivesARefusedMark(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"token does not have write:notification scope"}`))
		default:
			_, _ = w.Write([]byte(`{"id":12,"unread":true,"repository":{"full_name":"alice/project"},"subject":{"title":"Improve greeting","type":"Pull","state":"open","url":"https://gitea.test/api/v1/repos/alice/project/issues/17"}}`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"notification", "view", "12"}))
	require.Contains(t, out.String(), "title:\tImprove greeting\n")
	require.Contains(t, out.String(), "state:\topen\n")
	require.Contains(t, out.String(), "status:\tunread\n")
	require.NotContains(t, out.String(), "_No comments yet._")
	require.Contains(t, diagnostics.String(), "Could not mark notification 12 as read")
}

func TestNotificationListAllAccounts(t *testing.T) {
	inbox := func(id int, updated string) string {
		return fmt.Sprintf(`[{"id":%d,"unread":true,"updated_at":%q,"repository":{"full_name":"alice/project"},"subject":{"title":"Thread %d","type":"Issue","state":"open"}}]`, id, updated, id)
	}
	inboxes := func(answers map[string]string) *httptest.Server {
		return newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			for account, answer := range answers {
				if strings.HasSuffix(token, account) {
					if answer == "" {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(answer))
					return
				}
			}
			t.Errorf("unexpected credential %q", token)
		}))
	}
	first := inboxes(map[string]string{"alice": inbox(1, "2026-10-11T10:00:00Z"), "bob": inbox(2, "2026-10-11T12:00:00Z")})
	defer first.Close()
	second := inboxes(map[string]string{"carol": inbox(3, "2026-10-11T11:00:00Z")})
	defer second.Close()
	broken := inboxes(map[string]string{"dave": ""})
	defer broken.Close()
	setup := func(t *testing.T, accounts map[string][]string) (Dependencies, *bytes.Buffer, *bytes.Buffer) {
		t.Helper()
		deps, out, diagnostics := testDeps(t)
		for host, users := range accounts {
			for _, user := range users {
				require.NoError(t, testManager(t, deps).Register(host, user, "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: user}))
			}
		}
		return deps, out, diagnostics
	}
	all := map[string][]string{first.URL: {"alice", "bob"}, second.URL: {"carol"}}
	ids := func(t *testing.T, data []byte) (ids []string) {
		t.Helper()
		var items []struct {
			ID   int    `json:"id"`
			Host string `json:"host"`
			User string `json:"user"`
		}
		require.NoError(t, json.Unmarshal(data, &items))
		for _, item := range items {
			ids = append(ids, fmt.Sprintf("%d %s %s", item.ID, item.User, item.Host))
		}
		return ids
	}

	t.Run("merged with the most recent first", func(t *testing.T) {
		deps, out, _ := setup(t, all)
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--json", "id,host,user"}))
		require.Equal(t, []string{"2 bob " + first.URL, "3 carol " + second.URL, "1 alice " + first.URL}, ids(t, out.Bytes()))
	})
	t.Run("table", func(t *testing.T) {
		deps, out, _ := setup(t, all)
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts"}))
		require.True(t, strings.HasPrefix(out.String(), first.URL+"\tbob\t2\tissue\t"), out.String())
	})
	t.Run("host and user narrow the accounts", func(t *testing.T) {
		deps, out, _ := setup(t, all)
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--host", first.URL, "--json", "id,host,user"}))
		require.Equal(t, []string{"2 bob " + first.URL, "1 alice " + first.URL}, ids(t, out.Bytes()))
		deps, out, _ = setup(t, all)
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--user", "CAROL", "--json", "id,host,user"}))
		require.Equal(t, []string{"3 carol " + second.URL}, ids(t, out.Bytes()))
	})
	t.Run("GITEA_CLI_REPO names no inbox", func(t *testing.T) {
		deps, out, _ := setup(t, map[string][]string{second.URL: {"carol"}})
		deps.Getenv = envFunc(map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--json", "id,host,user"}))
		require.Equal(t, []string{"3 carol " + second.URL}, ids(t, out.Bytes()))
	})
	t.Run("limit applies to the merged list", func(t *testing.T) {
		deps, out, diagnostics := setup(t, all)
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--limit", "1", "--json", "id,host,user"}))
		require.Equal(t, []string{"2 bob " + first.URL}, ids(t, out.Bytes()))
		require.Contains(t, diagnostics.String(), "raise --limit")
	})
	t.Run("a failing account does not hide the others", func(t *testing.T) {
		deps, out, _ := setup(t, map[string][]string{second.URL: {"carol"}, broken.URL: {"dave"}})
		err := Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--json", "id,host,user"})
		require.ErrorContains(t, err, "dave on "+broken.URL)
		require.Equal(t, []string{"3 carol " + second.URL}, ids(t, out.Bytes()))
	})
	t.Run("every account failing lists nothing", func(t *testing.T) {
		deps, out, _ := setup(t, map[string][]string{broken.URL: {"dave"}})
		require.ErrorContains(t, Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts"}), "dave on "+broken.URL)
		require.Empty(t, out.String())
	})
	t.Run("no account", func(t *testing.T) {
		deps, _, _ := setup(t, nil)
		err := Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts"})
		require.ErrorContains(t, err, "not logged in to any Gitea instance")
		require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
		deps, _, _ = setup(t, all)
		err = Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts", "--user", "nobody"})
		require.ErrorContains(t, err, `account "nobody" is not logged in on any instance`)
	})
	t.Run("GITEA_TOKEN selects one account", func(t *testing.T) {
		deps, _, _ := serverDeps(t, second.URL)
		err := Execute(context.Background(), deps, []string{"notification", "list", "--all-accounts"})
		require.ErrorContains(t, err, "--all-accounts cannot be combined with GITEA_TOKEN")
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	})
	t.Run("a single account names itself in JSON", func(t *testing.T) {
		deps, out, _ := setup(t, map[string][]string{second.URL: {"carol"}})
		deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": second.URL})
		require.NoError(t, Execute(context.Background(), deps, []string{"notification", "list", "--json", "id,host,user"}))
		require.Equal(t, []string{"3 carol " + second.URL}, ids(t, out.Bytes()))
	})
}
