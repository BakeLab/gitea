// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gitea.dev/cli/internal/git/gittest"

	"github.com/stretchr/testify/require"
)

func readsRepository(t *testing.T) (string, System) {
	t.Helper()
	gittest.Isolate(t)
	dir := t.TempDir()
	plain := System{ErrOut: io.Discard}
	ctx := t.Context()
	_, err := plain.Output(ctx, dir, false, "init", "--quiet", "--initial-branch=main")
	require.NoError(t, err)
	included := filepath.Join(t.TempDir(), "included")
	require.NoError(t, os.WriteFile(included, []byte("[gt]\n\tincluded = yes\n"), 0o600))
	config, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = config.WriteString(`[Branch "Main.x"]
	Remote = origin
[gt]
	repeated = first
	repeated = last
	valueless
	empty =
	spaced = "  padded  "
	lines = "one\ntwo"
[include]
	path = ` + filepath.ToSlash(included) + "\n")
	require.NoError(t, err)
	require.NoError(t, config.Close())
	return dir, plain
}

func TestReadCacheAnswersLikeGit(t *testing.T) {
	dir, plain := readsRepository(t)
	ctx := t.Context()
	_, err := plain.Output(ctx, dir, false, "remote", "add", "origin", "https://example.org/alice/project.git")
	require.NoError(t, err)
	cached := System{ErrOut: io.Discard, Reads: &ReadCache{}}
	for _, args := range [][]string{
		{"config", "--get", "gt.repeated"},
		{"config", "--get", "GT.Repeated"},
		{"config", "--get", "gt.valueless"},
		{"config", "--get", "gt.empty"},
		{"config", "--get", "gt.spaced"},
		{"config", "--get", "gt.lines"},
		{"config", "--get", "gt.included"},
		{"config", "--get", "gt.missing"},
		{"config", "--get", "BRANCH.Main.x.REMOTE"},
		{"config", "--get", "branch.main.x.remote"},
		{"config", "--get", "nodot"},
		{"remote"},
		{"remote", "get-url", "origin"},
		{"remote", "get-url", "missing"},
		{"symbolic-ref", "--quiet", "HEAD"},
	} {
		want, wantErr := plain.Output(ctx, dir, false, args...)
		for range 2 {
			got, err := cached.Output(ctx, dir, false, args...)
			require.Equal(t, want, got, args)
			require.Equal(t, wantErr, err, args)
		}
	}
}

func TestReadCacheSeesWhatOtherGitCommandsChange(t *testing.T) {
	dir, _ := readsRepository(t)
	ctx := t.Context()
	cached := System{ErrOut: io.Discard, Reads: &ReadCache{}}
	names, err := cached.Output(ctx, dir, false, "remote")
	require.NoError(t, err)
	require.Empty(t, names)
	_, err = cached.Output(ctx, dir, false, "config", "--get", "gt.written")
	require.Error(t, err)

	_, err = cached.Output(ctx, dir, false, "remote", "add", "origin", "https://example.org/alice/project.git")
	require.NoError(t, err)
	_, err = cached.Output(ctx, dir, false, "config", "gt.written", "yes")
	require.NoError(t, err)

	names, err = cached.Output(ctx, dir, false, "remote")
	require.NoError(t, err)
	require.Equal(t, "origin", names)
	written, err := cached.Output(ctx, dir, false, "config", "--get", "gt.written")
	require.NoError(t, err)
	require.Equal(t, "yes", written)
}

func TestReadCacheLeavesDiagnosticReadsToGit(t *testing.T) {
	dir, _ := readsRepository(t)
	diagnostics := &strings.Builder{}
	cached := System{ErrOut: diagnostics, Reads: &ReadCache{}}
	for range 2 {
		_, err := cached.Output(t.Context(), dir, false, "remote", "get-url", "missing")
		require.Error(t, err)
	}
	require.Equal(t, 2, strings.Count(diagnostics.String(), "missing"))
}

func TestDiscoveryProbesSpawnGitOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the counting wrapper is a shell script")
	}
	dir, _ := readsRepository(t)
	ctx := t.Context()
	_, err := (System{}).Output(ctx, dir, false, "remote", "add", "origin", "https://example.org/alice/project.git")
	require.NoError(t, err)
	binary, err := exec.LookPath("git")
	require.NoError(t, err)
	wrapper, log := t.TempDir(), filepath.Join(t.TempDir(), "spawned")
	require.NoError(t, os.WriteFile(filepath.Join(wrapper, "git"), []byte("#!/bin/sh\necho \"$*\" >> '"+log+"'\nexec '"+binary+"' \"$@\"\n"), 0o700))
	t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))

	cached := System{ErrOut: io.Discard, Reads: &ReadCache{}}
	cached.Warm(ctx, dir)
	for range 2 {
		remote, err := DiscoverRemote(ctx, cached, dir, "", nil)
		require.NoError(t, err)
		require.Equal(t, "origin", remote)
		_, err = CurrentBranchConfig(ctx, cached, dir)
		require.NoError(t, err)
	}
	spawned, err := os.ReadFile(log)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"config --list -z", "remote", "symbolic-ref --quiet HEAD"}, strings.Split(strings.TrimSpace(string(spawned)), "\n"))
}
