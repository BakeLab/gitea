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

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestAPIFieldsPlaceholdersAndAtomicPagination(t *testing.T) {
	calls := 0
	failSecond := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		if r.Method == http.MethodPost {
			var fields map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&fields))
			require.Equal(t, "123", fields["title"])
			require.Equal(t, true, fields["closed"])
			require.Equal(t, float64(2), fields["number"])
			_, _ = w.Write([]byte(`{"number":2}`))
			return
		}
		require.Equal(t, "open", r.URL.Query().Get("state"))
		require.Equal(t, "2", r.URL.Query().Get("limit"))
		w.Header().Set("X-Tea-Test", "header-on-stderr")
		if r.URL.Query().Get("page") == "2" {
			if failSecond {
				w.WriteHeader(403)
				return
			}
			_, _ = w.Write([]byte(`[{"number":2}]`))
			return
		}
		w.Header().Set("Link", `<?page=2&state=open&limit=2>; type="application/json"; rel="next"`)
		_, _ = w.Write([]byte(`[{"number":1}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/{owner}/{repo}/issues", "-R", "alice/project", "-f", "title=123", "-F", "closed=true", "-F", "number=2"}))
	require.JSONEq(t, `{"number":2}`, out.String())
	out.Reset()
	input := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(input, []byte(`{"title":"123","closed":true,"number":2}`), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/{owner}/{repo}/issues", "-R", "alice/project", "--input", input}))
	require.JSONEq(t, `{"number":2}`, out.String())
	out.Reset()
	output := filepath.Join(t.TempDir(), "pages.json")
	args := []string{"api", "repos/{owner}/{repo}/issues", "-R", "alice/project", "-X", "GET", "-f", "state=open", "-F", "limit=2", "--paginate", "--include", "--output-file", output}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Empty(t, out.String())
	require.Contains(t, diagnostics.String(), "X-Tea-Test: header-on-stderr")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "[{\"number\":1}]\n[{\"number\":2}]\n", string(data))
	info, err := os.Stat(output)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	failSecond = true
	require.NoError(t, os.Remove(output))
	require.Error(t, Execute(context.Background(), deps, args))
	_, err = os.Stat(output)
	require.True(t, os.IsNotExist(err))
	require.Equal(t, 6, calls)
}

func TestAPIBranchPlaceholderUsesEscapedCurrentBranch(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/alice/project/branches/feature%2Fx", r.URL.EscapedPath())
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "feature/x"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/{owner}/{repo}/branches/{branch}", "-R", "alice/project"}))
	require.Equal(t, 1, calls)
	deps.Git = &fakeGit{}
	err := Execute(context.Background(), deps, []string{"api", "repos/{owner}/{repo}/branches/{branch}", "-R", "alice/project"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "{branch} requires a checked-out Git branch")
	require.Equal(t, 1, calls)
}

func TestAPIDebugLogsRequestsToStderr(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"project"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos/alice/project"}))
	require.Empty(t, diagnostics.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"--debug", "api", "repos/alice/project?access_token=query-secret"}))
	require.JSONEq(t, `{"name":"project"}`, out.String())
	require.Contains(t, diagnostics.String(), "[debug] GET "+server.URL+"/api/v1/user -> 200 OK")
	require.Contains(t, diagnostics.String(), "[debug] GET "+server.URL+"/api/v1/repos/alice/project?access_token=REDACTED -> 200 OK")
	require.NotContains(t, diagnostics.String(), "query-secret")
	require.NotContains(t, diagnostics.String(), "token ")
}

func TestAPIWritesErrorResponses(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Tea-Test", "error-header")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid","errors":["title"]}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"api", "repos/alice/project/issues", "-X", "POST", "-f", "title=", "--include"})
	var apiErr *transport.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusUnprocessableEntity, apiErr.Status)
	require.Equal(t, `{"message":"invalid","errors":["title"]}`, out.String())
	require.Contains(t, diagnostics.String(), "422 Unprocessable Entity")
	require.Contains(t, diagnostics.String(), "X-Tea-Test: error-header")
}

func TestAPITypedFieldsInferJSONValues(t *testing.T) {
	fields, err := apiFields([]string{"string=true"}, []string{"bool=true", "null=null", "integer=123", "array=[1,2]", "text=ordinary text"})
	require.NoError(t, err)
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	require.JSONEq(t, `{"string":"true","bool":true,"null":null,"integer":123,"array":[1,2],"text":"ordinary text"}`, string(data))
}

func TestAPIHeadersPreserveRawResponseAndRejectCredentialOverrides(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
		require.Equal(t, []string{"one", "two"}, r.Header.Values("X-Tea-Test"))
		require.Equal(t, "token secret", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte{0, 1, 2, 255})
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": server.URL, "GITEA_TOKEN": "secret"})
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user/data", "-H", "Accept: application/octet-stream", "-H", "X-Tea-Test: one", "-H", "X-Tea-Test: two"}))
	require.Equal(t, []byte{0, 1, 2, 255}, out.Bytes())
	for _, header := range []string{"Authorization: sensitive-value", "Host: unrelated.example.org", "Proxy-Authorization: sensitive-value", "Content-Length: 100", "Connection: upgrade", "Bad Name: value", "X-Test: value\r\nAuthorization: sensitive-value", "X-Test: value\x00", "missing-colon"} {
		out.Reset()
		err := Execute(context.Background(), deps, []string{"api", "user/data", "-H", header})
		require.Error(t, err)
		require.NotContains(t, err.Error(), "sensitive-value")
		require.Empty(t, out.String())
	}
	require.Equal(t, 1, calls)
}

type rejectAPIInput struct{ t *testing.T }

func (r rejectAPIInput) Read([]byte) (int, error) {
	r.t.Error("stdin was consumed before validation")
	return 0, io.EOF
}

func TestAPIFieldFilesPreserveTextAndValidateStdinConsumers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.txt")
	require.NoError(t, os.WriteFile(path, []byte("true\n"), 0o600))
	typed := []string{"body=@" + path, "comment=@-"}
	fields, err := apiFields(nil, typed)
	require.NoError(t, err)
	require.NoError(t, apiFieldFiles(fields, typed, strings.NewReader("123\n")))
	require.Equal(t, "true\n", fields["body"])
	require.Equal(t, "123\n", fields["comment"])
	deps, _, _ := testDeps(t)
	deps.Streams.In = rejectAPIInput{t}
	for _, args := range [][]string{
		{"api", "user", "-F", "one=@-", "-F", "two=@-"},
		{"api", "user", "--input", "-", "-F", "one=@-"},
		{"api", "user", "-F", "one=@-", "-F", "one=duplicate"},
	} {
		require.Error(t, Execute(context.Background(), deps, args))
	}
}
