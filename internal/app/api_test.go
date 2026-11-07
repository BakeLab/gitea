// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/transport"

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
			require.IsType(t, float64(0), fields["number"])
			require.InDelta(t, float64(2), fields["number"], 0)
			_, _ = w.Write([]byte(`{"number":2}`))
			return
		}
		require.Equal(t, "open", r.URL.Query().Get("state"))
		require.Equal(t, "2", r.URL.Query().Get("limit"))
		w.Header().Set("X-Gt-Test", "header-on-stderr")
		if r.URL.Query().Get("page") == "2" {
			if failSecond {
				w.WriteHeader(http.StatusForbidden)
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
	require.Contains(t, diagnostics.String(), "X-Gt-Test: header-on-stderr")
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "[{\"number\":1}]\n[{\"number\":2}]\n", string(data))
	info, err := os.Stat(output)
	require.NoError(t, err)
	if runtime.GOOS != "windows" { // Windows has no permission bits to keep private
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
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
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feature/x"}}
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
	const body = `{"message":"invalid","errors":["title"]}`
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Gt-Test", "error-header")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"api", "repos/alice/project/issues", "-X", "POST", "-f", "title=", "--include"})
	var apiErr *transport.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusUnprocessableEntity, apiErr.Status)
	if out.String() != body {
		t.Fatalf("output = %q, want %q", out.String(), body)
	}
	require.Contains(t, diagnostics.String(), "422 Unprocessable Entity")
	require.Contains(t, diagnostics.String(), "X-Gt-Test: error-header")
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
		require.Equal(t, []string{"one", "two"}, r.Header.Values("X-Gt-Test"))
		require.Equal(t, "token secret", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte{0, 1, 2, 255})
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL, "GITEA_TOKEN": "secret"})
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user/data", "-H", "Accept: application/octet-stream", "-H", "X-Gt-Test: one", "-H", "X-Gt-Test: two"}))
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

func TestAPIStripsTerminalControlsOnlyOnATerminalStdout(t *testing.T) {
	const body = "plain \x1b]0;PWNED\x07\x1b[31mtext\x00\n"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	for tty, want := range map[bool]string{false: body, true: "plain ]0;PWNED[31mtext\n"} {
		deps, out, _ := serverDeps(t, server.URL)
		deps.Streams.OutTTY = tty
		require.NoError(t, Execute(context.Background(), deps, []string{"api", "user/data"}))
		require.Equal(t, want, out.String(), "tty=%t", tty)
	}
	deps, out, _ := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	path := filepath.Join(t.TempDir(), "response")
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user/data", "--output-file", path}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, string(data))
	require.Empty(t, out.String())
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

func TestAPIFiltersAndFormatsJSONResponses(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=utf-8")
		switch r.URL.Path {
		case "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"28.1.0","b":1}`))
		case "/api/v1/repos/alice/nope":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "/api/v1/repos/alice/gone":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/repos":
			if r.URL.Query().Get("page") != "2" {
				w.Header().Set("Link", `<?page=2>; rel="next"`)
				_, _ = w.Write([]byte(`[{"name":"one"}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"name":"two"}]`))
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("plain"))
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name string
		tty  bool
		args []string
		want string
		err  string
	}{
		{name: "jq without --json", args: []string{"version", "--jq", ".version"}, want: "28.1.0\n"},
		{name: "jq shorthand on a terminal", tty: true, args: []string{"version", "-q", "."}, want: "{\n  \"b\": 1,\n  \"version\": \"28.1.0\"\n}\n"},
		{name: "jq on every page", args: []string{"repos", "--paginate", "--jq", ".[].name"}, want: "one\ntwo\n"},
		{name: "slurp the pages into one array", args: []string{"repos", "--paginate", "--slurp"}, want: "[[{\"name\":\"one\"}],[{\"name\":\"two\"}]]\n"},
		{name: "slurp on a terminal", tty: true, args: []string{"repos", "--paginate", "--slurp"}, want: "[\n  [\n    {\n      \"name\": \"one\"\n    }\n  ],\n  [\n    {\n      \"name\": \"two\"\n    }\n  ]\n]\n"},
		{name: "slurp needs paginate", args: []string{"repos", "--slurp"}, err: "--slurp requires --paginate"},
		{name: "slurp needs JSON", args: []string{"text", "--paginate", "--slurp"}, err: "--slurp needs JSON pages"},
		{name: "terminal keeps key order", tty: true, args: []string{"version"}, want: "{\n  \"version\": \"28.1.0\",\n  \"b\": 1\n}\n"},
		{name: "piped JSON ends with a newline", args: []string{"version"}, want: "{\"version\":\"28.1.0\",\"b\":1}\n"},
		{name: "error body ends with a newline", args: []string{"repos/alice/nope"}, want: "{\"message\":\"not found\"}\n", err: "HTTP 404"},
		{name: "error body is not filtered", args: []string{"repos/alice/nope", "--jq", ".message"}, want: "{\"message\":\"not found\"}\n", err: "HTTP 404"},
		{name: "jq needs JSON", args: []string{"text", "--jq", "."}, err: "--jq needs a JSON response, but the response is text/plain"},
		{name: "jq on 204 No Content", args: []string{"repos/alice/gone", "-X", "DELETE", "--jq", "."}},
		{name: "silent", args: []string{"version", "--silent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := serverDeps(t, server.URL)
			deps.Streams.OutTTY = tc.tty
			err := Execute(context.Background(), deps, append([]string{"api"}, tc.args...))
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, withoutANSI(out.String()))
		})
	}
	requireUsageBeforeRequests(t, "--silent cannot be combined with --jq", "api", "version", "--silent", "--jq", ".")
	requireUsageBeforeRequests(t, "--silent cannot be combined with --output-file", "api", "version", "--silent", "--output-file", filepath.Join(t.TempDir(), "out"))
	missing := filepath.Join(t.TempDir(), "missing")
	requireUsageBeforeRequests(t, missing+" is not an existing directory", "api", "version", "--output-file", filepath.Join(missing, "out"))
	requireUsageBeforeRequests(t, "api returns raw responses; --json is not supported", "api", "version", "--json")
	requireUsageBeforeRequests(t, `invalid HTTP method "FOO"`, "api", "version", "-X", "foo")
	requireUsageBeforeRequests(t, `invalid HTTP method ""`, "api", "version", "-X", "")
}

func TestAPISlurpKeepsThePagesAsReceived(t *testing.T) {
	const first = `{"z":1,"a":12345678901234567890,"html":"<a&b>"}`
	serve := func(pages ...string) *httptest.Server {
		return newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(cmp.Or(r.URL.Query().Get("page"), "1"))
			if page < len(pages) {
				w.Header().Set("Link", fmt.Sprintf(`<?page=%d>; rel="next"`, page+1))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(pages[page-1]))
		}))
	}
	for _, tc := range []struct {
		name  string
		tty   bool
		pages []string
		want  string
	}{
		{name: "piped", pages: []string{first, "[2.50]\n"}, want: "[" + first + ",[2.50]]\n"},
		{name: "terminal", tty: true, pages: []string{first}, want: "[\n  {\n    \"z\": 1,\n    \"a\": 12345678901234567890,\n    \"html\": \"<a&b>\"\n  }\n]\n"},
		{name: "an empty page is skipped", pages: []string{"[1]", "", " \n", "[2]"}, want: "[[1],[2]]\n"},
		{name: "every page is empty", pages: []string{"", ""}, want: "[]\n"},
		{name: "every page is empty on a terminal", tty: true, pages: []string{""}, want: "[]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := serve(tc.pages...)
			defer server.Close()
			deps, out, _ := serverDeps(t, server.URL)
			deps.Streams.OutTTY = tc.tty
			require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos", "--paginate", "--slurp"}))
			require.Equal(t, tc.want, withoutANSI(out.String()))
		})
	}
}

func TestAPIMaxPagesStopsWithTheAllowedPages(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := cmp.Or(r.URL.Query().Get("page"), "1")
		next, _ := strconv.Atoi(page)
		w.Header().Set("Link", fmt.Sprintf(`<?page=%d>; rel="next"`, next+1))
		_, _ = fmt.Fprintf(w, `[{"page":%s}]`, page)
	}))
	defer server.Close()
	deps, out, errOut := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "repos", "--paginate", "--max-pages", "2"}))
	require.Equal(t, "[{\"page\":1}]\n[{\"page\":2}]\n", out.String())
	require.Contains(t, errOut.String(), "Stopped after 2 pages as --max-pages asks; more pages exist")

	// Only an explicit --max-pages makes a partial result complete; the default limit fails.
	deps, out, _ = serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"api", "repos", "--paginate", "--jq", "length"})
	require.EqualError(t, err, "--paginate stopped after 1000 pages and more pages exist; raise --max-pages")
	require.Equal(t, 1000, strings.Count(out.String(), "1\n"))
}

func TestAPIVerboseShowsRequestAndResponseWithoutCredentials(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Gt-Test", "response")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "version?access_token=secret", "--verbose", "-H", "X-Request: one"}))
	require.Equal(t, "{}", out.String())
	head := diagnostics.String()
	for _, want := range []string{"> GET " + server.URL + "/api/v1/version\n", "> Authorization: (hidden)\n", "> X-Request: one\n", "< HTTP/1.1 200 OK\n", "< X-Gt-Test: response\n"} {
		require.Contains(t, head, want)
	}
	require.NotContains(t, head, "secret")
	require.NotContains(t, head, "token token")
}

func TestAPIIncludeRedactsSignedURLsInResponseHeaders(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<http://storage.test/a?page=2&sig=link-secret>; rel="next"`)
		w.Header().Set("Location", "http://storage.test/lfs/blob?X-Amz-Credential=key-secret&X-Amz-Signature=sig-secret&X-Amz-Expires=300")
		w.WriteHeader(http.StatusSeeOther)
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.Error(t, Execute(context.Background(), deps, []string{"api", "repos/alice/project/media/big.bin", "--include", "--silent"}))
	head := diagnostics.String()
	require.Contains(t, head, "Location: http://storage.test/lfs/blob?X-Amz-Credential=REDACTED&X-Amz-Expires=300&X-Amz-Signature=REDACTED\n")
	require.Contains(t, head, `Link: <http://storage.test/a?page=2&sig=REDACTED>; rel="next"`+"\n")
	require.NotContains(t, head, "secret")
}

func TestAPIVerboseHidesConfiguredHeaders(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cf-Access-Client-Secret") != "proxy-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	deps, _, diagnostics := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"PROXY_SECRET": "proxy-secret"})
	configureHost(t, deps, server.URL, settings.Host{Headers: map[string]string{"Cf-Access-Client-Secret": "$PROXY_SECRET", "X-Tenant": "acme"}})
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "version", "--verbose", "-H", "X-Tenant: mine"}))
	head := diagnostics.String()
	require.Contains(t, head, "> Cf-Access-Client-Secret: (hidden)\n")
	require.Contains(t, head, "> X-Tenant: mine\n", "a header of -H replaces the configured one and is shown")
	require.NotContains(t, head, "proxy-secret")
	require.NotContains(t, head, "is not sent")

	// A header whose variable is unset is left out, and --verbose says so.
	diagnostics.Reset()
	deps.Getenv = serverEnv(server.URL, nil)
	err := Execute(context.Background(), deps, []string{"api", "version", "--verbose"})
	require.Error(t, err, "the server requires the header")
	require.Contains(t, diagnostics.String(), "! Cf-Access-Client-Secret is not sent: $PROXY_SECRET is not set\n")
}

func TestAPIHelpShowsJQ(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "--help"}))
	require.Contains(t, out.String(), "\n  -q, --jq expression ")
	require.NotContains(t, out.String(), "--json fields")
}

func TestAPIIncludeSanitizesTheStatusLine(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 OK\x1b]0;PWNED\x07\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
		_ = rw.Flush()
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "version", "--include"}))
	require.NotContains(t, diagnostics.String(), "\x1b")
	require.NotContains(t, diagnostics.String(), "\x07")
}

func TestAPIEndpointOutsideTheInstanceIsAUsageError(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, endpoint := range []string{"http://other.test/api/v1/version", server.URL + "/login", "repos/../user"} {
		err := Execute(context.Background(), deps, []string{"api", endpoint})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), endpoint)
	}
	err := Execute(context.Background(), deps, []string{"api", "http://other.test/api/v1/version"})
	require.EqualError(t, err, "API URL must belong to the selected instance and its API base path")
}

func TestAPIInputFileCanBeReadAgainForSigning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"title":"x"}`), 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	// A body that can be read again is hashed for an HTTP signature as it is read, instead of
	// being held in memory.
	body, ok := inputBody(file).(*transport.ReplayableBody)
	require.True(t, ok, "a regular --input file is replayable")
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.JSONEq(t, `{"title":"x"}`, string(data))

	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	require.Equal(t, io.Reader(reader), inputBody(reader), "a pipe is sent as it is read")
}
