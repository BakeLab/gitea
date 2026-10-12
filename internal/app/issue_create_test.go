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
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestIssueCreateSeedsFromTemplate(t *testing.T) {
	var created []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issue_templates":
			_, _ = w.Write([]byte(`[{"name":"Bug Report","title":"[bug] ","content":"Steps:"},{"name":"Feature","about":"Describe the idea","body":[{"type":"textarea"}]}]`))
		case "/api/v1/repos/alice/project/issues":
			require.Equal(t, http.MethodPost, r.Method)
			payload, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			created = append(created, string(payload))
			_, _ = w.Write([]byte(`{"number":1}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	editor := &draftEditor{draft: terminal.Draft{Body: "edited idea"}}
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append([]string{"issue", "create", "-R", "alice/project", "--no-input", "--json=number"}, args...))
	}
	require.NoError(t, run("--template", "bug report"))
	require.NoError(t, run("--template", "Bug Report", "--title", "Explicit", "--body", "Override"))
	require.NoError(t, run("--template", "feature", "--title", "Idea", "--editor"))
	require.Equal(t, "Describe the idea", editor.initial)
	err := run("--template", "missing", "--title", "x")
	require.ErrorContains(t, err, `issue template "missing" does not exist; available: Bug Report, Feature`)
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	err = run("--template", "feature")
	require.ErrorContains(t, err, "missing required input: supply --title")
	deps.Streams.In = strings.NewReader("From stdin")
	require.NoError(t, run("--template", "bug report", "--body-file", "-"))
	err = run("--template", "feature", "--body-file", "-")
	require.ErrorContains(t, err, "--body-file - requires --title")
	require.Len(t, created, 4)
	for i, want := range []string{
		`{"title":"[bug] ","body":"Steps:"}`,
		`{"title":"Explicit","body":"Override"}`,
		`{"title":"Idea","body":"edited idea"}`,
		`{"title":"[bug] ","body":"From stdin"}`,
	} {
		requirePayload(t, want, strings.NewReader(created[i]))
	}
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
	require.ErrorContains(t, err, "issue 5 created; attachment second.txt upload failed")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.JSONEq(t, `{"number":5,"title":"Crash"}`, out.String())
	require.Equal(t, []string{"first.txt=one", "second.txt=two"}, uploaded)
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/issues", "POST /api/v1/repos/alice/project/issues/5/assets", "POST /api/v1/repos/alice/project/issues/5/assets"}, requests)
}
