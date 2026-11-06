// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	"gitea.dev/cli/internal/version"
	"github.com/stretchr/testify/require"
)

func TestDetectInstallation(t *testing.T) {
	for _, tc := range []struct {
		path, stamped, goos string
		want                installKind
	}{
		{"/home/alice/.local/bin/gt", "1.0.0", "linux", installScript},
		{"/usr/local/bin/gt", "1.0.0-rc1", "darwin", installScript},
		{"/opt/homebrew/Cellar/gitea-cli/1.0.0/bin/gt", "1.0.0", "darwin", installHomebrew},
		{"/usr/bin/gt", "1.0.0", "linux", installPackage},
		{"/usr/bin/gt", "1.0.0", "darwin", installScript},
		{"/nix/store/abc-gitea-cli-1.0.0/bin/gt", "1.0.0", "linux", installNix},
		{"/home/alice/src/cli/gt", "1.0.0+3-gabcdef0", "linux", installSource},
		{"/home/alice/src/cli/gt", "development", "linux", installSource},
	} {
		require.Equal(t, tc.want, detectInstallation(tc.path, tc.stamped, tc.goos).kind, tc.path)
	}
}

// releaseServer serves gt's latest release v0.3.0, counting the requests for it, and its build
// for this system, whose checksum file lists digest, or the build's own digest when digest is
// empty. other serves the remaining requests.
func releaseServer(t *testing.T, build []byte, digest string, requests *atomic.Int32, other http.HandlerFunc) *httptest.Server {
	t.Helper()
	platform, err := releasePlatform(runtime.GOOS, runtime.GOARCH)
	require.NoError(t, err)
	file := "/gitea-cli/0.3.0/gitea-cli-0.3.0-" + platform
	if digest == "" {
		sum := sha256.Sum256(build)
		digest = hex.EncodeToString(sum[:])
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/gitea/cli/releases/latest":
			requests.Add(1)
			_, _ = fmt.Fprintf(w, `{"tag_name":"v0.3.0","html_url":"https://gitea.com/gitea/cli/releases/tag/v0.3.0","published_at":%q}`, time.Now().Add(-48*time.Hour).Format(time.RFC3339))
		case file:
			_, _ = w.Write(build)
		case file + ".sha256":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, filepath.Base(file))
		default:
			if other == nil {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				return
			}
			other(w, r)
		}
	}))
}

// releaseDeps runs gt 0.2.0, installed by the install script at the returned path, against server.
func releaseDeps(t *testing.T, server *httptest.Server, env map[string]string) (Dependencies, string) {
	t.Helper()
	stamped := version.Version
	version.Version = "0.2.0"
	t.Cleanup(func() { version.Version = stamped })
	executable := filepath.Join(t.TempDir(), "gt")
	require.NoError(t, os.WriteFile(executable, []byte("gt 0.2.0"), 0o755))
	deps, _, _ := testDeps(t)
	deps.Executable = func() (string, error) { return executable, nil }
	deps.Getenv = envFunc(env)
	deps.Client = func(instance, token string, insecure bool) *transport.Client {
		if instance == releaseInstance {
			instance = server.URL
		}
		return transport.New(instance, token, insecure)
	}
	return deps, executable
}

func TestUpgrade(t *testing.T) {
	t.Run("replaces the executable", func(t *testing.T) {
		var requests atomic.Int32
		server := releaseServer(t, []byte("gt 0.3.0"), "", &requests, nil)
		defer server.Close()
		deps, executable := releaseDeps(t, server, nil)
		deps.Getenv = envFunc(map[string]string{"GITEA_CLI_DOWNLOAD_URL": server.URL + "/gitea-cli"})
		require.NoError(t, Execute(context.Background(), deps, []string{"upgrade", "--yes"}))
		data, err := os.ReadFile(executable)
		require.NoError(t, err)
		require.Equal(t, "gt 0.3.0", string(data))
		entries, err := os.ReadDir(filepath.Dir(executable))
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})
	t.Run("takes a nightly build from the directory that the CDN does not cache for long", func(t *testing.T) {
		platform, err := releasePlatform(runtime.GOOS, runtime.GOARCH)
		require.NoError(t, err)
		file := "gitea-cli-main-nightly-" + platform
		build := []byte("gt nightly")
		digest := sha256.Sum256(build)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/gitea-cli/main-nightly/" + file:
				_, _ = w.Write(build)
			case "/gitea-cli/main-nightly/" + file + ".sha256":
				_, _ = fmt.Fprintf(w, "%x  %s\n", digest, file)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()
		deps, executable := releaseDeps(t, server, map[string]string{"GITEA_CLI_DOWNLOAD_URL": server.URL + "/gitea-cli"})
		require.NoError(t, Execute(context.Background(), deps, []string{"upgrade", "--nightly", "--yes"}))
		data, err := os.ReadFile(executable)
		require.NoError(t, err)
		require.Equal(t, build, data)
	})
	t.Run("removes the old executable on Windows when it no longer runs", func(t *testing.T) {
		dir := t.TempDir()
		executable, staged := filepath.Join(dir, "gt.exe"), filepath.Join(dir, "gt.new")
		require.NoError(t, os.WriteFile(executable, []byte("old"), 0o755))
		require.NoError(t, os.WriteFile(staged, []byte("new"), 0o755))
		require.NoError(t, replaceExecutable(staged, executable, "windows"))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})
	t.Run("keeps the executable on a checksum mismatch", func(t *testing.T) {
		var requests atomic.Int32
		server := releaseServer(t, []byte("gt 0.3.0"), hex.EncodeToString(make([]byte, 32)), &requests, nil)
		defer server.Close()
		deps, executable := releaseDeps(t, server, nil)
		deps.Getenv = envFunc(map[string]string{"GITEA_CLI_DOWNLOAD_URL": server.URL + "/gitea-cli"})
		err := Execute(context.Background(), deps, []string{"upgrade", "--yes"})
		require.ErrorContains(t, err, "but its checksum file lists 0000")
		data, err := os.ReadFile(executable)
		require.NoError(t, err)
		require.Equal(t, "gt 0.2.0", string(data))
		entries, err := os.ReadDir(filepath.Dir(executable))
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})
	t.Run("leaves Homebrew installations to brew", func(t *testing.T) {
		var requests atomic.Int32
		server := releaseServer(t, nil, "", &requests, nil)
		defer server.Close()
		deps, _ := releaseDeps(t, server, nil)
		deps.Executable = func() (string, error) { return "/opt/homebrew/Cellar/gitea-cli/0.2.0/bin/gt", nil }
		err := Execute(context.Background(), deps, []string{"upgrade", "--yes"})
		require.EqualError(t, err, "gt was installed with Homebrew; upgrade it with brew upgrade gitea-cli")
		require.Zero(t, requests.Load())
	})
}

func TestUpdateNotifier(t *testing.T) {
	var requests atomic.Int32
	server := releaseServer(t, nil, "", &requests, nil)
	defer server.Close()
	deps, _ := releaseDeps(t, server, nil)
	deps.Streams.OutTTY, deps.Streams.ErrTTY = true, true
	diagnostics := &bytes.Buffer{}
	deps.Streams.ErrOut = diagnostics

	_, a := newRoot(deps)
	state, err := a.latestRelease(context.Background(), deps.Client)
	require.NoError(t, err)
	require.Equal(t, "0.3.0", state.Latest)
	require.Equal(t, int32(1), requests.Load())
	require.NoError(t, a.saveUpdateState(state))

	require.NoError(t, Execute(context.Background(), deps, []string{"version"}))
	require.Equal(t, "\nA new release of gt is available: 0.2.0 → 0.3.0\nTo upgrade, run: gt upgrade\nhttps://gitea.com/gitea/cli/releases/tag/v0.3.0\n", withoutANSI(diagnostics.String()))

	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"version"}))
	require.Empty(t, diagnostics.String(), "told within a day")
	require.Equal(t, int32(1), requests.Load(), "checked within a day")
}
