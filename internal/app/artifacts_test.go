// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestRunDownloadZipToStdoutKeepsBinaryOutputClean(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/runs/29/artifacts":
			_, _ = w.Write([]byte(`{"total_count":1,"artifacts":[{"id":9,"name":"build"}]}`))
		case "/api/v1/repos/owner/repo/actions/artifacts/9/zip":
			require.Equal(t, "token token", r.Header.Get("Authorization"))
			http.Redirect(w, r, "/signed?signature=private", http.StatusFound)
		case "/signed":
			require.Empty(t, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte{0x50, 0x4b, 0, 0xff})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "owner/repo", "--zip", "-n", "build", "-O", "-"}))
	require.Equal(t, []byte{0x50, 0x4b, 0, 0xff}, out.Bytes())
	require.Empty(t, diagnostics.String())
	err := Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "owner/repo", "--zip", "-n", "build", "-O", "-", "--json"})
	require.ErrorContains(t, err, "run download writes files")
}

// zipOf returns a ZIP archive of files, by name.
func zipOf(t *testing.T, files map[string]string) string {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range files {
		file, err := writer.Create(name)
		require.NoError(t, err)
		_, err = file.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return archive.String()
}

func TestRunDownloadExtractsArtifactsByName(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/artifacts": `{"total_count":3,"artifacts":[{"id":1,"name":"build","size_in_bytes":3},{"id":2,"name":"coverage"},{"id":3,"name":"old","expired":true}]}`,
		"GET actions/artifacts/1/zip":   zipOf(t, map[string]string{"bin/gt": "gt", "README": "read me"}),
		"GET actions/artifacts/2/zip":   zipOf(t, map[string]string{"cover.out": "100%"}),
		"GET actions/artifacts/4/zip":   zipOf(t, map[string]string{"../escape": "x"}),
	})
	for _, tc := range []struct {
		name  string
		args  []string
		files map[string]string
		err   string
		out   string
	}{
		{
			"every artifact in its own directory", nil,
			map[string]string{"build/bin/gt": "gt", "build/README": "read me", "coverage/cover.out": "100%"},
			"",
			"✓ Downloaded artifact build to {dir}/build\n✓ Downloaded artifact coverage to {dir}/coverage\n! Artifact old has expired; skipped\n",
		},
		{"one artifact into the directory", []string{"-n", "coverage"}, map[string]string{"cover.out": "100%"}, "", "✓ Downloaded artifact coverage to {dir}\n"},
		{
			"archives",
			[]string{"--zip", "--name", "build,coverage"},
			map[string]string{"build.zip": "", "coverage.zip": ""},
			"",
			"✓ Downloaded artifact build to {dir}/build.zip\n✓ Downloaded artifact coverage to {dir}/coverage.zip\n",
		},
		{"unknown name", []string{"-n", "docs"}, nil, `artifact "docs" not found in run 29 (artifacts: build, coverage, old)`, ""},
		{"expired", []string{"-n", "old"}, nil, "the artifacts of run 29 have expired", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			deps, _, diagnostics := actionsDeps(t, server.URL, true)
			err := Execute(context.Background(), deps, append([]string{"run", "download", "29", "-R", "alice/project", "-D", dir}, tc.args...))
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, strings.ReplaceAll(tc.out, "{dir}", dir), diagnostics.String())
			for name, content := range tc.files {
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err, name)
				if content != "" {
					require.Equal(t, content, string(data), name)
				}
			}
		})
	}
	t.Run("existing files and unsafe paths", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "cover.out"), []byte("mine"), 0o600))
		deps, _, _ := actionsDeps(t, server.URL, false)
		err := Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", dir, "-n", "coverage"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.ErrorContains(t, err, "cover.out already exists; pass --clobber to replace it or --skip-existing to keep it")
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", dir, "-n", "coverage", "--skip-existing"}))
		data, err := os.ReadFile(filepath.Join(dir, "cover.out"))
		require.NoError(t, err)
		require.Equal(t, "mine", string(data))
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", dir, "-n", "coverage", "--clobber"}))
		data, err = os.ReadFile(filepath.Join(dir, "cover.out"))
		require.NoError(t, err)
		require.Equal(t, "100%", string(data))
		require.ErrorContains(t, extractZip(context.Background(), bytes.NewReader([]byte(zipOf(t, map[string]string{"../escape": "x"}))), dir, false, false), `refusing to extract "../escape"`)
		_, err = os.Stat(filepath.Join(filepath.Dir(dir), "escape"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestExtractZipKeepsPermissionBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits")
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, mode := range map[string]os.FileMode{"bin/tool": 0o755, "setuid": 0o755 | os.ModeSetuid, "data": 0o644} {
		header := &zip.FileHeader{Name: name}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = file.Write([]byte(name))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	dir := t.TempDir()
	require.NoError(t, extractZip(context.Background(), &archive, dir, false, false))
	for name, executable := range map[string]bool{"bin/tool": true, "setuid": true, "data": false} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err)
		require.Equal(t, executable, info.Mode().Perm()&0o100 != 0, name)
		require.Zero(t, info.Mode()&os.ModeSetuid, name)
	}
}

func TestArtifactListShowsSizesAndExpiry(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/artifacts": `{"total_count":2,"artifacts":[{"id":1,"name":"build","size_in_bytes":3145728,"expires_at":"2028-03-01T12:00:00Z"},{"id":3,"name":"old","size_in_bytes":13,"expired":true,"expires_at":"2027-12-01T12:00:00Z"}]}`,
	})
	for tty, want := range map[bool]string{
		true:  "Showing 2 of 2 artifacts of run 29\n\nID  NAME   SIZE     EXPIRES\n1   build  3.0 MiB  in about 2 months\n3   old    13 B     expired\n",
		false: "1\tbuild\t3145728\t2028-03-01T12:00:00Z\tfalse\n3\told\t13\t2027-12-01T12:00:00Z\ttrue\n",
	} {
		deps, out, _ := actionsDeps(t, server.URL, tty)
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "artifact", "list", "29", "-R", "alice/project"}))
		require.Equal(t, want, out.String(), "tty=%t", tty)
	}
}
