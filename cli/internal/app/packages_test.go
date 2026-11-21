// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

// cancelingPrompter cancels every input prompt and records it.
type cancelingPrompter struct{ scriptedPrompter }

func (p *cancelingPrompter) Ask(_ context.Context, field string, _ func(string) error) (string, error) {
	p.asked = append(p.asked, field)
	return "", terminal.ErrCanceled
}

func TestPackagesAskForTheAccountOnce(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "", "alice", "bob")
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/acme/tool.git"}}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompter := &cancelingPrompter{}
	deps.Prompter = prompter
	// Canceling the choice of account in a clone ends the command instead of asking again for
	// the packages of the account.
	err := Execute(context.Background(), deps, []string{"package", "list"})
	require.ErrorIs(t, err, terminal.ErrCanceled)
	require.Equal(t, []string{"account (alice, bob)"}, prompter.asked)
}

func TestPackagesListViewDeleteLinkAndUpload(t *testing.T) {
	var requests []string
	uploaded := map[string]string{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/packages/acme":
			require.Equal(t, "generic", r.URL.Query().Get("type"))
			writeFirstPage(w, r, `[{"id":1,"type":"generic","name":"tool","version":"1.0.0","owner":{"login":"acme"},"repository":{"full_name":"acme/tool"},"created_at":"2026-01-02T00:00:00Z"}]`)
		case "GET /api/v1/packages/acme/generic/tool/-/latest":
			_, _ = w.Write([]byte(`{"id":1,"type":"generic","name":"tool","version":"1.0.0"}`))
		case "GET /api/v1/packages/acme/generic/tool/1.0.0/files":
			_, _ = w.Write([]byte(`[{"id":7,"name":"tool-linux","size":2048,"sha256":"abc"}]`))
		case "DELETE /api/v1/packages/acme/generic/tool/1.0.0", "DELETE /api/v1/packages/acme/generic/tool",
			"POST /api/v1/packages/acme/generic/tool/-/link/tool-source", "POST /api/v1/packages/acme/generic/tool/-/unlink":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/packages/acme/generic/tool/2.0.0/files":
			w.WriteHeader(http.StatusNotFound)
		case "PUT /api/packages/acme/generic/tool/2.0.0/tool-linux":
			data, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			uploaded[r.URL.Path] = string(data)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "acme/tool")))
		return out.String()
	}
	require.Equal(t, "generic\ttool\t1.0.0\tacme/tool\t2026-01-02T00:00:00Z\n", run("package", "list", "--type", "generic"))
	require.JSONEq(t, `{"version":"1.0.0","files":[{"id":7,"name":"tool-linux","size":2048,"sha256":"abc"}]}`, run("package", "view", "generic", "tool", "--json", "version,files"))
	require.Contains(t, run("package", "view", "generic", "tool"), "title:\ttool\nversion:\t1.0.0\ntype:\tgeneric\n")
	run("package", "delete", "generic", "tool", "1.0.0", "--yes")
	run("package", "delete", "generic", "tool", "--all", "--yes")
	run("package", "link", "generic", "tool", "tool-source")
	run("package", "unlink", "generic", "tool")
	file := filepath.Join(t.TempDir(), "tool-linux")
	require.NoError(t, os.WriteFile(file, []byte("binary"), 0o600))
	run("package", "upload", "tool", "2.0.0", file)
	require.Equal(t, map[string]string{"/api/packages/acme/generic/tool/2.0.0/tool-linux": "binary"}, uploaded)
	require.Contains(t, requests, "DELETE /api/v1/packages/acme/generic/tool")
	requireUsageBeforeRequests(t, "package delete requires a version or --all", "package", "delete", "generic", "tool")
	requireUsageBeforeRequests(t, "--all cannot be combined with a version", "package", "delete", "generic", "tool", "1.0.0", "--all")
}

func TestPackageUploadRefusesExistingFilesAndReportsPartialUploads(t *testing.T) {
	var puts []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/packages/acme/generic/tool/1.0.0/files":
			_, _ = w.Write([]byte(`[{"id":7,"name":"B","size":1}]`))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/a"):
			puts = append(puts, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
		default:
			puts = append(puts, r.URL.Path)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("package file already exists"))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	upload := func(version string) error {
		return Execute(context.Background(), deps, []string{"package", "upload", "tool", version, filepath.Join(dir, "a"), filepath.Join(dir, "b"), "-R", "acme/tool"})
	}
	// Gitea refuses a name that differs only in case.
	require.EqualError(t, upload("1.0.0"), "file B already exists in tool@1.0.0")
	require.Empty(t, puts, "nothing is uploaded when a file exists already")
	// A file that appears while uploading still names what was uploaded.
	require.EqualError(t, upload("2.0.0"), "uploaded a to generic package tool@2.0.0, but not b: file b already exists in tool@2.0.0")
	require.Equal(t, []string{"/api/packages/acme/generic/tool/2.0.0/a", "/api/packages/acme/generic/tool/2.0.0/b"}, puts)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "A"), []byte("A"), 0o600))
	puts = nil
	err := Execute(context.Background(), deps, []string{"package", "upload", "tool", "3.0.0", filepath.Join(dir, "a"), filepath.Join(dir, "sub", "A"), "-R", "acme/tool"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "would both be uploaded as A, ignoring case")
	require.Empty(t, puts)
	require.Empty(t, diagnostics.String())
}

// Gitea's package registry answers a valid token that cannot write the owner's packages with 401.
func TestPackageUploadWithoutWriteAccessIsAPermissionError(t *testing.T) {
	valid := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/user" && valid:
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/packages/acme/generic/tool/1.0.0/files":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("reqPackageAccess"))
		}
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(file, []byte("tool"), 0o600))
	for _, valid = range []bool{true, false} {
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, []string{"package", "upload", "tool", "1.0.0", file, "-R", "acme/tool"})
		if valid {
			require.EqualError(t, err, "uploading packages to acme requires write access to its packages and a token with the write:package scope")
			require.Equal(t, clierr.Failure, clierr.ExitCode(err))
		} else {
			require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
		}
	}
}

func TestPackageUploadRefusesAnSSHKeyAccountUpFront(t *testing.T) {
	keyPath := writeAppTempSSHKey(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL, "--ssh-key", keyPath, "--no-input", "--yes"}))
	file := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(file, []byte("tool"), 0o600))
	err := Execute(context.Background(), deps, []string{"package", "upload", "tool", "1.0.0", file, "-R", "alice/tool", "--host", server.URL})
	require.EqualError(t, err, "Gitea's package registry does not accept SSH key accounts; log in with a token to upload packages: gt auth login --with-token")
}

func TestPackageLinkNamesWhatIsMissing(t *testing.T) {
	var links []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		links = append(links, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/packages/acme/generic/tool/-/link/source":
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/packages/acme/generic/tool/-/link/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"repository does not exist [id: 0, uid: 1, owner_name: , name: missing]"}`))
		case "/api/v1/packages/acme/generic/tool/-/unlink":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"invalid argument"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"GetPackageByName: package does not exist"}`))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append(args, "-R", "acme/tool"))
	}
	require.NoError(t, run("package", "link", "generic", "tool", "ACME/source"))
	require.EqualError(t, run("package", "link", "generic", "tool", "missing"), "repository acme/missing not found")
	require.EqualError(t, run("package", "link", "generic", "other", "source"), "package acme/generic/other not found")
	// Unlinking a package without a repository changes nothing, as linking it again does.
	diagnostics.Reset()
	require.NoError(t, run("package", "unlink", "generic", "tool"))
	require.Equal(t, "! generic package tool is not linked to a repository\n", diagnostics.String())
	err := run("package", "link", "generic", "tool", "bob/source")
	require.EqualError(t, err, `a package of acme links only to a repository of acme; got "bob/source"`)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Equal(t, []string{
		"/api/v1/packages/acme/generic/tool/-/link/source", "/api/v1/packages/acme/generic/tool/-/link/missing",
		"/api/v1/packages/acme/generic/other/-/link/source", "/api/v1/packages/acme/generic/tool/-/unlink",
	}, links)
}

func TestPackageLinkOfALinkedPackage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/packages/acme/generic/tool/-/link/source", "/api/v1/packages/acme/generic/tool/-/link/other":
			// Gitea refuses to link a linked package.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"invalid argument"}`))
		case "/api/v1/packages/acme/generic/tool/-/latest":
			_, _ = w.Write([]byte(`{"id":1,"type":"generic","name":"tool","version":"1.0.0","repository":{"full_name":"acme/source"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append(args, "-R", "acme/tool"))
	}
	// Linking it again to its repository changes nothing.
	require.NoError(t, run("package", "link", "generic", "tool", "source"))
	require.Equal(t, "! generic package tool is already linked to acme/source\n", diagnostics.String())
	require.EqualError(t, run("package", "link", "generic", "tool", "other"), "package acme/generic/tool is already linked to acme/source; unlink it first")
}

func TestPackagesDefaultToTheOwnerOfTheCloneElseTheAccount(t *testing.T) {
	var owners []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owners = append(owners, r.URL.Path)
		writeFirstPage(w, r, `[]`)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/acme/tool.git"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"package", "list"}))
	// Outside a clone, the packages are the account's own.
	deps.Git = &fakeGit{}
	require.NoError(t, Execute(context.Background(), deps, []string{"package", "list"}))
	// So are they in a clone of a repository on another instance.
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": "https://example.invalid/acme/tool.git"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"package", "list"}))
	require.Equal(t, []string{"/api/v1/packages/acme", "/api/v1/packages/alice", "/api/v1/packages/alice"}, owners)
	// A repository the user names must resolve.
	require.Error(t, Execute(context.Background(), deps, []string{"package", "list", "-R", "https://example.invalid/acme/tool"}))
	require.Len(t, owners, 3)
}
