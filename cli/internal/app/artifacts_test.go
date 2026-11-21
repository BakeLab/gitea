// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
			w.WriteHeader(http.StatusNotFound)
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
			require.Equal(t, strings.ReplaceAll(strings.ReplaceAll(tc.out, "{dir}/", dir+string(filepath.Separator)), "{dir}", dir), diagnostics.String())
			for name, content := range tc.files {
				data, err := os.ReadFile(filepath.Join(dir, name))
				require.NoError(t, err, name)
				if content != "" {
					require.Equal(t, content, string(data), name)
				}
			}
		})
	}
	t.Run("newest artifact of a rerun name", func(t *testing.T) {
		server, _ := actionsAPI(t, map[string]string{
			"GET actions/runs/29/artifacts": `{"total_count":3,"artifacts":[{"id":1,"name":"dist"},{"id":2,"name":"docs"},{"id":3,"name":"dist"}]}`,
			"GET actions/artifacts/2/zip":   zipOf(t, map[string]string{"index": "docs"}),
			"GET actions/artifacts/3/zip":   zipOf(t, map[string]string{"app": "new"}),
		})
		dir := t.TempDir()
		deps, _, _ := actionsDeps(t, server.URL, false)
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", dir}))
		data, err := os.ReadFile(filepath.Join(dir, "dist", "app"))
		require.NoError(t, err)
		require.Equal(t, "new", string(data))
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", dir, "-n", "dist", "--clobber"}))
	})
	t.Run("unknown name lists artifacts without control characters", func(t *testing.T) {
		server, _ := actionsAPI(t, map[string]string{"GET actions/runs/29/artifacts": `{"total_count":1,"artifacts":[{"id":4,"name":"build\u001b]0;pwned\u0007"}]}`})
		deps, _, _ := actionsDeps(t, server.URL, false)
		err := Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", t.TempDir(), "-n", "docs"})
		require.EqualError(t, err, `artifact "docs" not found in run 29 (artifacts: build]0;pwned)`)
	})
	t.Run("unsafe artifact names", func(t *testing.T) {
		for _, name := range []string{"../evil", "..", "a/b", `a\b`, "/abs"} {
			server, requests := actionsAPI(t, map[string]string{"GET actions/runs/29/artifacts": `{"total_count":1,"artifacts":[{"id":4,"name":` + strconv.Quote(name) + `}]}`})
			dir := t.TempDir()
			deps, _, _ := actionsDeps(t, server.URL, false)
			err := Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", filepath.Join(dir, "out")})
			require.ErrorContains(t, err, fmt.Sprintf("cannot use %q as a directory name", name))
			require.Equal(t, []string{"GET actions/runs/29/artifacts"}, *requests)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries)
		}
	})
	t.Run("names that differ only in case", func(t *testing.T) {
		server, requests := actionsAPI(t, map[string]string{"GET actions/runs/29/artifacts": `{"total_count":2,"artifacts":[{"id":1,"name":"OUT"},{"id":2,"name":"out"}]}`})
		for _, args := range [][]string{nil, {"-n", "OUT", "-n", "out", "--clobber"}, {"--zip"}} {
			*requests = nil
			dir := t.TempDir()
			deps, _, _ := actionsDeps(t, server.URL, false)
			err := Execute(context.Background(), deps, append([]string{"run", "download", "29", "-R", "alice/project", "-D", dir}, args...))
			require.Equal(t, clierr.Usage, clierr.ExitCode(err), args)
			require.EqualError(t, err, "artifacts OUT and out differ only in case and would overwrite each other; download one at a time with --name and --dir")
			require.Equal(t, []string{"GET actions/runs/29/artifacts"}, *requests, "nothing is downloaded before the clash is reported")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries)
		}
	})
	t.Run("a name given twice", func(t *testing.T) {
		dir := t.TempDir()
		deps, _, diagnostics := actionsDeps(t, server.URL, true)
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "download", "29", "-R", "alice/project", "-D", dir, "--zip", "-n", "build", "-n", "build"}))
		require.Equal(t, "✓ Downloaded artifact build to "+filepath.Join(dir, "build.zip")+"\n", diagnostics.String())
	})
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

func TestExtractZipStaysInsideTheDestinationThroughLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs privileges on Windows")
	}
	outside, dir := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "victim"), []byte("mine"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "linked")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "victim"), filepath.Join(dir, "file")))
	for _, name := range []string{"linked/victim", "linked/new", "file"} {
		archive := bytes.NewReader([]byte(zipOf(t, map[string]string{name: "x"})))
		require.Error(t, extractZip(context.Background(), archive, dir, true, false), name)
	}
	data, err := os.ReadFile(filepath.Join(outside, "victim"))
	require.NoError(t, err)
	require.Equal(t, "mine", string(data))
	_, err = os.Stat(filepath.Join(outside, "new"))
	require.ErrorIs(t, err, os.ErrNotExist)
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
		"GET actions/runs/29/artifacts": `{"total_count":2,"artifacts":[{"id":1,"name":"build","size_in_bytes":3145728,"expires_at":"2028-03-01T14:00:00+02:00"},{"id":3,"name":"old","size_in_bytes":13,"expired":true,"expires_at":"2027-12-01T12:00:00Z"}]}`,
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

func TestExtractZipClobberDoesNotModifyHardLinkTarget(t *testing.T) {
	outside, dir := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("keep outside"), 0o600))
	require.NoError(t, os.Link(victim, filepath.Join(dir, "file")))
	archive := strings.NewReader(zipOf(t, map[string]string{"file": "replacement"}))
	require.NoError(t, extractZip(context.Background(), archive, dir, true, false))
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "keep outside", string(data))
	data, err = os.ReadFile(filepath.Join(dir, "file"))
	require.NoError(t, err)
	require.Equal(t, "replacement", string(data))
}

func TestExtractZipClobberPreservesFileOnChecksumFailure(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: "file", Method: zip.Store})
	require.NoError(t, err)
	_, err = file.Write([]byte("contents"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	damaged := bytes.Replace(archive.Bytes(), []byte("contents"), []byte("tampered"), 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	require.ErrorIs(t, extractZip(context.Background(), bytes.NewReader(damaged), dir, true, false), zip.ErrChecksum)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestExtractZipClobberPreservesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "file"}
	header.SetMode(0o644)
	file, err := writer.CreateHeader(header)
	require.NoError(t, err)
	_, err = file.Write([]byte("replacement"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(path, []byte("private"), 0o600))
	require.NoError(t, extractZip(context.Background(), &archive, dir, true, false))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
