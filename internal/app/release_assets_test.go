// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestReleaseBulkAssetInputAndPartialFailures(t *testing.T) {
	uploads := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1"}`))
			return
		}
		uploads++
		if uploads == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"id":2,"name":"second.txt"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	first, second := filepath.Join(t.TempDir(), "first.txt"), filepath.Join(t.TempDir(), "second.txt")
	require.NoError(t, os.WriteFile(first, []byte("first"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("second"), 0o600))
	err := Execute(context.Background(), deps, []string{"release", "asset", "upload", "v1", first, second + "missing", "-R", "owner/repo", "--json"})
	require.Error(t, err)
	require.Zero(t, uploads)
	err = Execute(context.Background(), deps, []string{"release", "upload", "v1", first, second, "-R", "owner/repo", "--json"})
	require.ErrorContains(t, err, "first.txt")
	require.Equal(t, 2, uploads)
	require.JSONEq(t, `[{"id":2,"name":"second.txt","size":0,"url":"","created_at":"","download_count":0}]`, out.String())
}

func TestReleaseUploadRefusesDuplicateNamesWithoutClobber(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":7,"name":"hello.txt"}]}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"id":8,"name":"hello.txt","size":5}`))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "hello.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))
	err := Execute(context.Background(), deps, []string{"release", "asset", "upload", "v1", file, "-R", "owner/repo"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "asset hello.txt already exists in release v1; pass --clobber to replace it")
	require.Equal(t, []string{"GET /api/v1/repos/owner/repo/releases/tags/v1"}, requests)
	requests = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "upload", "v1", file, "--clobber", "-R", "owner/repo"}))
	require.Equal(t, []string{"GET /api/v1/repos/owner/repo/releases/tags/v1", "POST /api/v1/repos/owner/repo/releases/1/assets", "DELETE /api/v1/repos/owner/repo/releases/1/assets/7"}, requests)
	require.Equal(t, "✓ Uploaded hello.txt (5 B) to release v1\n", diagnostics.String())
}

func TestReleaseAssetNamesCannotClash(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":7,"name":"gt-linux"},{"id":8,"name":"gt-darwin"}]}`))
		default:
			_, _ = w.Write([]byte(`{"id":8,"name":"gt-darwin-arm64"}`))
		}
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "hello.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))
	// Gitea would store "../gt-linux" as gt-linux, beside the asset of that name.
	for _, name := range []string{"../gt-linux", `sub\gt-linux`} {
		err := Execute(context.Background(), deps, []string{"release", "upload", "v1", file, "--name", name, "-R", "owner/repo"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, "cannot contain / or \\")
	}
	// A rename is held to the same rule, and to names gt release download can save.
	for name, message := range map[string]string{
		"a/b":           `--name "a/b" cannot contain / or \`,
		"../x":          `--name "../x" cannot contain / or \`,
		"..":            `--name ".." is not a valid file name`,
		"bell\a":        `--name "bell\a" is not a valid file name`,
		"\u202egnp.exe": `--name "\u202egnp.exe" is not a valid file name`,
	} {
		err := Execute(context.Background(), deps, []string{"release", "asset", "edit", "v1", "gt-darwin", "--name", name, "-R", "owner/repo"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
		require.EqualError(t, err, message)
	}
	err := Execute(context.Background(), deps, []string{"release", "upload", "v1", file, "--name", "..", "-R", "owner/repo"})
	require.EqualError(t, err, `--name ".." is not a valid file name`)
	require.Empty(t, requests)
	err = Execute(context.Background(), deps, []string{"release", "asset", "edit", "v1", "gt-darwin", "--name", "gt-linux", "-R", "owner/repo"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "asset gt-linux already exists in release v1; delete it first")
	require.Equal(t, []string{"GET /api/v1/repos/owner/repo/releases/tags/v1"}, requests)
	// Renaming an asset to its own name, or to a free one, is fine.
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "asset", "edit", "v1", "gt-darwin", "--name", "gt-darwin", "-R", "owner/repo"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "asset", "edit", "v1", "gt-darwin", "--name", "gt-darwin-arm64", "-R", "owner/repo"}))
}

func TestReleaseAssetNameSelectionValidatedBeforeDeletion(t *testing.T) {
	var deleted []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":1,"name":"one.txt"},{"id":2,"name":"2"}]}`))
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", "one.txt", "missing", "--yes", "-R", "owner/repo"})
	require.EqualError(t, err, `asset "missing" not found in release v1`)
	require.Empty(t, deleted)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", "one.txt", "2", "--by-name", "--yes", "-R", "owner/repo"}))
	require.Equal(t, []string{"/api/v1/repos/owner/repo/releases/1/assets/1", "/api/v1/repos/owner/repo/releases/1/assets/2"}, deleted)
	require.Equal(t, "✓ Deleted asset one.txt (ID 1) from release v1\n✓ Deleted asset 2 (ID 2) from release v1\n", diagnostics.String())
}

func TestReleaseAssetNamesMatchOnlyInTheirOwnCase(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":7,"name":"GT-LINUX","uuid":"aaa"},{"id":8,"name":"Case.txt","uuid":"bbb"}]}`))
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	// Asset names are case-sensitive, so another case names no asset, and nothing is changed.
	for name, args := range map[string][]string{
		"gt-linux": {"release", "asset", "delete", "v1", "gt-linux", "--yes"},
		"case.txt": {"release", "asset", "edit", "v1", "case.txt", "--name", "renamed.txt"},
		"CASE.TXT": {"release", "download", "v1", "CASE.TXT", "-O", "-"},
	} {
		err := Execute(context.Background(), deps, append(args, "-R", "owner/repo"))
		require.EqualError(t, err, `asset "`+name+`" not found in release v1`, "%v", args)
	}
	require.Equal(t, []string{"GET /api/v1/repos/owner/repo/releases/tags/v1", "GET /api/v1/repos/owner/repo/releases/tags/v1", "GET /api/v1/repos/owner/repo/releases/tags/v1"}, requests)
}

func TestReleaseDownloadSelectsAssetsOfTheLatestRelease(t *testing.T) {
	var downloads []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/owner/repo/releases/latest", r.URL.Path == "/api/v1/repos/owner/repo/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":1,"name":"gt-linux","size":5,"uuid":"aaa"},{"id":2,"name":"gt-darwin","size":5,"uuid":"bbb"},{"id":3,"name":"notes.txt","size":5,"uuid":"ccc"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/owner/repo/releases/1/assets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/releases/1/assets/")
			uuid := map[string]string{"1": "aaa", "2": "bbb", "3": "ccc"}[id]
			_, _ = w.Write([]byte(`{"id":` + id + `,"uuid":"` + uuid + `"}`))
		case strings.HasPrefix(r.URL.Path, "/attachments/"):
			downloads = append(downloads, r.URL.Path)
			_, _ = w.Write([]byte("bytes"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	dir := t.TempDir()
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "download", "--pattern", "gt-*", "--dir", dir, "-R", "owner/repo"}))
	require.Equal(t, []string{"/attachments/aaa", "/attachments/bbb"}, downloads)
	content, err := os.ReadFile(filepath.Join(dir, "gt-darwin"))
	require.NoError(t, err)
	require.Equal(t, "bytes", string(content))
	require.Equal(t, "✓ Downloaded gt-linux (5 B) to "+filepath.Join(dir, "gt-linux")+"\n✓ Downloaded gt-darwin (5 B) to "+filepath.Join(dir, "gt-darwin")+"\n", diagnostics.String())
	err = Execute(context.Background(), deps, []string{"release", "asset", "download", "v1", "--pattern", "*.exe", "--dir", dir, "-R", "owner/repo"})
	require.Error(t, err)
	err = Execute(context.Background(), deps, []string{"release", "download", "v1", "notes.txt", "gt-linux", "-O", filepath.Join(dir, "one"), "-R", "owner/repo"})
	require.ErrorContains(t, err, "--output names one file, but 2 assets match; pass --dir")
	for _, flag := range []string{"--clobber", "--skip-existing"} {
		err = Execute(context.Background(), deps, []string{"release", "download", "v1", "notes.txt", "-O", "-", flag, "-R", "owner/repo"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.EqualError(t, err, flag+" cannot be combined with --output -")
	}
	// Skipping is worded like the other downloads.
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "download", "v1", "gt-linux", "--dir", dir, "--skip-existing", "-R", "owner/repo"}))
	require.Equal(t, "! Skipped gt-linux: "+filepath.Join(dir, "gt-linux")+" exists\n", diagnostics.String())
}

func TestReleaseDownloadArchiveOfADraftWithoutItsTag(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/releases/tags/v2":
			_, _ = w.Write([]byte(`{"id":2,"tag_name":"v2","draft":true}`))
		case "/api/v1/repos/owner/repo/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1"}`))
		case "/api/v1/repos/owner/repo/archive/v2.zip", "/api/v1/repos/owner/repo/archive/v1.zip":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unrecognized repository reference"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	dir := t.TempDir()
	err := Execute(context.Background(), deps, []string{"release", "download", "v2", "--archive", "zip", "--dir", dir, "-R", "owner/repo"})
	require.EqualError(t, err, "release v2 is a draft whose tag does not exist yet; publish it to create the tag")
	err = Execute(context.Background(), deps, []string{"release", "download", "v1", "--archive", "zip", "--dir", dir, "-R", "owner/repo"})
	require.EqualError(t, err, "ref v1 not found in owner/repo")
}

func TestReleaseDownloadCreatesDirAndRefusesDuplicateNames(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/owner/repo/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":1,"name":"app","size":5,"uuid":"aaa"},{"id":2,"name":"app","size":5,"uuid":"bbb"},{"id":3,"name":"other","size":5,"uuid":"ccc"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/owner/repo/releases/1/assets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/releases/1/assets/")
			_, _ = w.Write([]byte(`{"id":` + id + `,"uuid":"ccc"}`))
		case strings.HasPrefix(r.URL.Path, "/attachments/"):
			_, _ = w.Write([]byte("bytes"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	dir := filepath.Join(t.TempDir(), "new", "dist")
	err := Execute(context.Background(), deps, []string{"release", "download", "v1", "--dir", dir, "-R", "owner/repo"})
	require.ErrorContains(t, err, "assets 1 and 2 are both named app")
	_, statErr := os.Stat(filepath.Join(dir, "other"))
	require.Error(t, statErr, "nothing is downloaded before the duplicate is reported")
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "download", "v1", "other", "--dir", dir, "-R", "owner/repo"}))
	_, statErr = os.Stat(filepath.Join(dir, "other"))
	require.NoError(t, statErr)
}

func TestReleaseDownloadRefusesNamesThatDifferOnlyInCase(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":1,"name":"GT-LINUX","size":5,"uuid":"aaa"},{"id":2,"name":"gt-linux","size":5,"uuid":"bbb"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	dir := t.TempDir()
	for _, extra := range [][]string{nil, {"--clobber"}} {
		err := Execute(context.Background(), deps, append([]string{"release", "download", "v1", "GT-LINUX", "gt-linux", "--dir", dir, "-R", "owner/repo"}, extra...))
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.EqualError(t, err, "assets GT-LINUX (ID 1) and gt-linux (ID 2) differ only in case and would overwrite each other; download one at a time with --output")
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, entries, "nothing is downloaded before the clash is reported")
	}
}
