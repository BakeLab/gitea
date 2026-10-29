// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWikiEditRenamesAndKeepsTheContent(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch {
			requirePayload(t, `{"title":"New Title","content_base64":"S2VlcCBtZQ=="}`, r.Body)
			_, _ = w.Write([]byte(`{"title":"New Title","content_base64":"S2VlcCBtZQ=="}`))
			return
		}
		if r.URL.Path == "/api/v1/repos/alice/x/wiki/page/New Title" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"title":"Old Title","content_base64":"S2VlcCBtZQ=="}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "edit", "Old Title", "--title", "New Title", "-R", "alice/x", "--no-input"}))
	require.Equal(t, []string{
		"GET /api/v1/repos/alice/x/wiki/page/Old Title", "GET /api/v1/repos/alice/x/wiki/page/Old Title",
		"GET /api/v1/repos/alice/x/wiki/page/New Title", "PATCH /api/v1/repos/alice/x/wiki/page/Old Title",
	}, requests)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Renamed wiki page \"Old Title\" to \"New Title\" in alice/x\n", diagnostics.String())
	err := Execute(context.Background(), deps, []string{"wiki", "edit", "Home", "-R", "alice/x"})
	require.ErrorContains(t, err, "nothing to edit; pass a flag such as --title or --body (see --help)")
}

func TestWikiListLimitsPagesOnTheClient(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFirstPage(w, r, `[{"title":"Getting Started","last_commit":{"author":{"name":"alice","date":"2026-10-11T18:00:00Z"}}},{"title":"Home"}]`)
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "list", "--limit", "1", "-R", "alice/x"}))
	require.Equal(t, `Showing 1 of 2 wiki pages in alice/x · next: --page 2

TITLE            AUTHOR  UPDATED
Getting Started  alice   about 2 hours ago
`, out.String())
}

func TestWikiListOffersOnlyTheFieldsOfListedPages(t *testing.T) {
	requireUsageBeforeRequests(t, `unknown JSON field "body"`, "wiki", "list", "--json", "title,body")
	deps, _, _ := testDeps(t)
	list, _, err := NewRoot(deps).Find([]string{"wiki", "list"})
	require.NoError(t, err)
	require.Equal(t, []string{"author", "sha", "title", "updated_at", "url"}, commandOutputFields(list))
}

func TestWikiViewAndCreate(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requirePayload(t, `{"title":"Page Two","content_base64":"Ym9keQ=="}`, r.Body)
		}
		_, _ = w.Write([]byte(`{"title":"Page Two","html_url":"https://gitea.test/alice/x/wiki/Page-Two","content_base64":"Ym9keQ==","commit_count":3,
			"last_commit":{"author":{"name":"alice","date":"2026-10-11T19:00:00Z"}}}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "dumb"})
	rawMarkdown(t, deps)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "view", "Page Two", "-R", "alice/x"}))
	require.Equal(t, "Page Two\nalice · updated about 1 hour ago · 3 revisions\n\nbody\n\nView this wiki page on Gitea: https://gitea.test/alice/x/wiki/Page-Two\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "create", "Page Two", "-b", "body", "-R", "alice/x"}))
	require.Equal(t, "https://gitea.test/alice/x/wiki/Page-Two\n", out.String())
	require.Equal(t, "✓ Created wiki page \"Page Two\" in alice/x\n", diagnostics.String())
}

func TestWikiCreateSuggestsARunnableEditOfAnExistingPage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"wiki page already exists [title: Home.md]"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{" Home ", "-b", "body"}, `wiki page "Home" already exists in alice/x; edit it with: gt wiki edit 'Home' -b 'body' -R alice/x`},
		{[]string{"Bob's\tpage", "-b", "it's"}, `wiki page "Bob's\tpage" already exists in alice/x; edit it with: gt wiki edit $'Bob\'s\tpage' -b 'it'\''s' -R alice/x`},
		{[]string{"Home", "-F", "docs/home page.md", "-m", "Rewrite"}, `wiki page "Home" already exists in alice/x; edit it with: gt wiki edit 'Home' -F 'docs/home page.md' -m 'Rewrite' -R alice/x`},
		{[]string{"Home", "-t", "-r8dash", "-b", "body"}, ""},
	} {
		if tc.want == "" {
			// A title that starts with - goes after --, behind every flag.
			tc.args, tc.want = []string{"-t", "-dash", "-b", "body"}, `wiki page "-dash" already exists in alice/x; edit it with: gt wiki edit -b 'body' -R alice/x -- '-dash'`
		}
		if tc.args[1] == "-F" {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.args[2]), []byte("body"), 0o600))
			t.Chdir(dir)
		}
		err := Execute(context.Background(), deps, append(append([]string{"wiki", "create"}, tc.args...), "-R", "alice/x"))
		require.EqualError(t, Explain(context.Background(), deps, err), tc.want)
	}
	// Content written in the editor is in its draft.
	editor, _ := newDraftEditor(t, "body")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "editor"})
	err := Execute(context.Background(), deps, []string{"wiki", "create", "Home", "-e", "-R", "alice/x"})
	require.ErrorContains(t, Explain(context.Background(), deps, err), "edit it with: gt wiki edit 'Home' -F '"+editor.draft.Path+"' -R alice/x; draft saved at ")
}

func TestWikiCreateKeepsContentTheEditHintCannotCarryInADraft(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"wiki page already exists [title: Home.md]"}`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, body string
		args       []string
	}{
		{"multi-line", "line one\nline two", []string{"-b", "line one\nline two"}},
		{"tab", "a\tb", []string{"-b", "a\tb"}},
		{"long", strings.Repeat("x", 81), []string{"-b", strings.Repeat("x", 81)}},
		{"stdin", "from stdin\n", []string{"-F", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := serverDeps(t, server.URL)
			deps.Streams.In = strings.NewReader(tc.body)
			err := Execute(context.Background(), deps, append([]string{"wiki", "create", "Home", "-R", "alice/x"}, tc.args...))
			match := regexp.MustCompile(`edit it with: gt wiki edit 'Home' -F '([^']+)' -R alice/x; draft saved at (\S+)$`).FindStringSubmatch(Explain(context.Background(), deps, err).Error())
			require.Len(t, match, 3, err.Error())
			require.Equal(t, match[1], match[2])
			data, readErr := os.ReadFile(match[1])
			require.NoError(t, readErr)
			require.Equal(t, tc.body, string(data))
			require.NoError(t, os.Remove(match[1]))
		})
	}
}

func TestWikiViewPipedNormalizesTheUpdateTime(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Home","content_base64":"Ym9keQ==","last_commit":{"author":{"name":"alice","date":"2026-10-11T21:00:00.123+02:00"}}}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "view", "Home", "-R", "alice/x"}))
	require.Equal(t, "title:\tHome\nauthor:\talice\nupdated:\t2026-10-11T19:00:00Z\n--\nbody\n", out.String())
}

func TestWikiViewPipedKeepsAnUnparseableUpdateTime(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Home","content_base64":"Ym9keQ==","last_commit":{"author":{"name":"alice","date":"yesterday"}}}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "view", "Home", "-R", "alice/x"}))
	require.Contains(t, out.String(), "\nupdated:\tyesterday\n")
}

func TestWikiEditRefusesToOverwriteOrCreate(t *testing.T) {
	patched := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			patched++
			_, _ = w.Write([]byte(`{"title":"Getting Started","content_base64":"eA=="}`))
		case r.URL.Path == "/api/v1/repos/alice/x":
			_, _ = w.Write([]byte(`{"full_name":"alice/x","has_wiki":true}`))
		case r.URL.Path == "/api/v1/repos/alice/x/wiki/page/Draft":
			_, _ = w.Write([]byte(`{"title":"Draft","content_base64":"ZHJhZnQ="}`))
		case r.URL.Path == "/api/v1/repos/alice/x/wiki/page/Page B":
			_, _ = w.Write([]byte(`{"title":"Page B","content_base64":"Yg=="}`))
		case r.URL.Path == "/api/v1/repos/alice/x/wiki/page/Getting-Started", r.URL.Path == "/api/v1/repos/alice/x/wiki/page/Getting Started":
			_, _ = w.Write([]byte(`{"title":"Getting Started","content_base64":"Zw=="}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"wiki", "edit", "Draft", "--title", "Page B", "-R", "alice/x", "--no-input"})
	require.ErrorContains(t, err, `wiki page "Page B" already exists in alice/x`)
	err = Execute(context.Background(), deps, []string{"wiki", "edit", "Drfat", "--body", "x", "-R", "alice/x", "--no-input"})
	require.ErrorContains(t, err, `wiki page "Drfat" not found`)
	require.Zero(t, patched, "neither refused edit may reach the PATCH")
	// The page path of a page URL may name another page than the title it reads as.
	err = Execute(context.Background(), deps, []string{"wiki", "edit", "Getting-Started", "--body", "x", "-R", "alice/x", "--no-input"})
	require.EqualError(t, err, `wiki page "Getting-Started" not found in alice/x; did you mean "Getting Started"?`)
	require.Zero(t, patched)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "edit", "Getting Started", "--body", "x", "-R", "alice/x", "--no-input"}))
	require.Equal(t, "✓ Edited wiki page \"Getting Started\" in alice/x\n", diagnostics.String())
	require.Equal(t, 1, patched)
}

func TestWikiRevisionsCountsTheWholeHistory(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"commits":[{"sha":"abc","message":"Edit","author":{"name":"alice","date":"2026-10-11T18:00:00Z"}}],"count":56}`))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "revisions", "Home", "-R", "alice/x"}))
	require.Contains(t, out.String(), "Showing 1 of 56 revisions of wiki page \"Home\" · next: --page 2")
}
