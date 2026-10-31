// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package extensions

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRepositoryName(t *testing.T) {
	for repo, want := range map[string][2]string{"gt-hello": {"gt", "hello"}, "gh-dash": {"gh", "dash"}, "GT-Multi-Word": {"gt", "multi-word"}} {
		prefix, name, err := ParseRepositoryName(repo)
		require.NoError(t, err, repo)
		assert.Equal(t, want, [2]string{prefix, name}, repo)
	}
	for _, repo := range []string{"hello", "gt-", "gt-a_b", "gt-../x", "gt-trailing-", "gt-" + strings.Repeat("a", 40)} {
		_, _, err := ParseRepositoryName(repo)
		require.Error(t, err, repo)
	}
}

func TestParseMetadata(t *testing.T) {
	metadata, err := ParseMetadata(nil)
	require.NoError(t, err)
	assert.Equal(t, Metadata{}, metadata)
	metadata, err = ParseMetadata([]byte("description: Say hello\nplatforms: [linux/amd64, darwin]\ncredentials: true\ncompletion: cobra\n"))
	require.NoError(t, err)
	assert.True(t, metadata.Credentials)
	assert.True(t, metadata.Supports("darwin", "arm64"))
	assert.True(t, metadata.Supports("linux", "amd64"))
	assert.False(t, metadata.Supports("linux", "arm64"))
	_, err = ParseMetadata([]byte("credential: true\n"))
	require.ErrorContains(t, err, "credential")
	_, err = ParseMetadata([]byte("completion: bash\n"))
	require.ErrorContains(t, err, "completion must be cobra")

	metadata, err = ParseMetadata([]byte("aliases:\n  user-group: user-group\n  ls: \"list --all 'a b'\"\n"))
	require.NoError(t, err)
	args, err := metadata.Alias("ls")
	require.NoError(t, err)
	assert.Equal(t, []string{"list", "--all", "a b"}, args)
	// An unusable alias is ignored, never a reason to refuse the extension.
	metadata, err = ParseMetadata([]byte("aliases: {Bad: x, a_b: x, empty: \"\", open: \"'x\"}"))
	require.NoError(t, err)
	for name := range metadata.Aliases {
		_, err = metadata.Alias(name)
		assert.Error(t, err, name)
	}
	_, err = metadata.Alias("open")
	assert.ErrorContains(t, err, "cannot be parsed")
}

func TestStorePublishesAtomically(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "extensions")}
	_, err := store.Lookup("hello")
	require.ErrorIs(t, err, ErrNotInstalled)
	manifest := Manifest{Name: "hello", Prefix: PrefixGT, Kind: KindBinary, Tag: "v1", SHA256: strings.Repeat("a", 64)}
	publish := func(m Manifest, replace bool) error {
		stage, err := store.Stage()
		require.NoError(t, err)
		defer os.RemoveAll(stage)
		require.NoError(t, os.WriteFile(filepath.Join(stage, m.Executable()), []byte(m.Tag), 0o755))
		return store.Publish(stage, m, replace)
	}
	require.NoError(t, publish(manifest, false))
	installed, err := store.Lookup("hello")
	require.NoError(t, err)
	assert.Equal(t, manifest, installed.Manifest)
	data, err := os.ReadFile(installed.Path())
	require.NoError(t, err)
	assert.Equal(t, "v1", string(data))

	manifest.Tag = "v2"
	require.ErrorContains(t, publish(manifest, false), "already installed")
	require.NoError(t, publish(manifest, true))
	installed, err = store.Lookup("hello")
	require.NoError(t, err)
	assert.Equal(t, "v2", installed.Tag)

	stage, err := store.Stage()
	require.NoError(t, err)
	require.Error(t, store.Publish(stage, Manifest{Name: "../escape", Prefix: PrefixGT}, false))
	require.NoError(t, os.RemoveAll(stage))
	listed, err := store.List()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	entries, err := os.ReadDir(store.Dir)
	require.NoError(t, err)
	for _, entry := range entries {
		// Nothing staged or replaced is left behind.
		assert.Contains(t, []string{"hello", ".lock"}, entry.Name())
	}

	require.NoError(t, store.Remove("hello"))
	_, err = store.Lookup("hello")
	require.ErrorIs(t, err, ErrNotInstalled)
	require.ErrorIs(t, store.Remove("hello"), ErrNotInstalled)
}

func TestStoreRejectsForeignManifest(t *testing.T) {
	store := Store{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Join(store.Dir, "hello"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store.Dir, "hello", "manifest.yml"), []byte("name: other\nprefix: gt\nkind: binary\n"), 0o600))
	_, err := store.Lookup("hello")
	require.ErrorContains(t, err, "does not describe extension hello")
	_, err = store.List()
	require.Error(t, err)

	// A manifest gt cannot read hides no other extension and can still be removed.
	require.NoError(t, os.MkdirAll(filepath.Join(store.Dir, "world"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(store.Dir, "world", "manifest.yml"), []byte("name: world\nprefix: gt\nkind: binary\nadded_later: true\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(store.Dir, "hello", "manifest.yml"), []byte("name: ["), 0o600))
	listed, err := store.List()
	require.ErrorContains(t, err, "parse ")
	require.Len(t, listed, 1)
	assert.Equal(t, "world", listed[0].Name)
	assert.Equal(t, map[string]bool{"world": true}, store.Names())
	require.NoError(t, store.Remove("hello"))
	require.NoDirExists(t, filepath.Join(store.Dir, "hello"))
}

func TestStoreUnderAFileFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	store := Store{Dir: filepath.Join(file, "extensions")}
	_, err := store.Stage()
	require.Error(t, err)
	require.Error(t, store.Remove("hello"))
	require.Empty(t, store.Names())
}

func TestExtensionPathByKind(t *testing.T) {
	for _, tc := range []struct {
		kind, want string
	}{
		{KindBinary, filepath.Join("store", "hello", "gt-hello")},
		{KindScript, filepath.Join("store", "hello", "src", "gt-hello")},
		{KindLocal, filepath.Join("work", "gt-hello")},
	} {
		extension := Extension{Manifest: Manifest{Name: "hello", Prefix: PrefixGT, Kind: tc.kind, Path: filepath.Join("work", "gt-hello")}, Dir: filepath.Join("store", "hello")}
		if tc.kind == KindBinary && runtime.GOOS == "windows" {
			tc.want += ".exe"
		}
		assert.Equal(t, tc.want, extension.Path(), tc.kind)
	}
	assert.Equal(t, "alice/gt-hello", Manifest{Owner: "alice", Repo: "gt-hello"}.Repository())
	assert.Empty(t, Manifest{Kind: KindLocal}.Repository(), "a local extension has no repository")
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gt-hello")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o600))
	digest, err := FileSHA256(path)
	require.NoError(t, err)
	assert.Equal(t, "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03", digest)
	require.NoError(t, os.WriteFile(path, []byte("hello!\n"), 0o600))
	changed, err := FileSHA256(path)
	require.NoError(t, err)
	assert.NotEqual(t, digest, changed)
	_, err = FileSHA256(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestChecksumFor(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	other := strings.Repeat("cd", 32)
	checksums := other + "  gt-hello-linux-arm64\n" + strings.ToUpper(digest) + " *gt-hello-linux-amd64\n\n"
	got, err := ChecksumFor(strings.NewReader(checksums), "gt-hello-linux-amd64")
	require.NoError(t, err)
	assert.Equal(t, digest, got)
	_, err = ChecksumFor(strings.NewReader(checksums), "gt-hello-darwin-arm64")
	require.ErrorContains(t, err, "does not list gt-hello-darwin-arm64")
	_, err = ChecksumFor(strings.NewReader("not a digest  gt-hello\n"), "gt-hello")
	require.ErrorContains(t, err, "line 1 is not in sha256sum format")
	_, err = ChecksumFor(strings.NewReader(digest+"  gt-hello\n"+other+"  gt-hello\n"), "gt-hello")
	require.ErrorContains(t, err, "twice")
}

func TestSelectAsset(t *testing.T) {
	assets := []string{"checksums.txt", "gt-hello-linux-amd64", "hello-windows-amd64.exe", "gt-hello-darwin-amd64"}
	assert.Equal(t, 1, SelectAsset(assets, "gt-hello", "hello", "linux", "amd64"))
	assert.Equal(t, 2, SelectAsset(assets, "gt-hello", "hello", "windows", "amd64"))
	assert.Equal(t, 3, SelectAsset(assets, "gt-hello", "hello", "darwin", "arm64"), "Rosetta runs amd64 builds")
	assert.Equal(t, -1, SelectAsset(assets, "gt-hello", "hello", "linux", "arm64"))
	// GitHub CLI extensions name their assets NAME_TAG_OS-ARCH or just OS-ARCH.
	gh := []string{"gh-dash_v4.26.2_linux-arm64", "gh-dash_v4.26.2_linux-arm", "gh-dash_v4.26.2_windows-amd64.exe", "darwin-amd64", "gh-dash-linux-arm"}
	assert.Equal(t, 0, SelectAsset(gh, "gh-dash", "dash", "linux", "arm64"))
	assert.Equal(t, 4, SelectAsset(gh, "gh-dash", "dash", "linux", "arm"), "an exact name wins")
	assert.Equal(t, 2, SelectAsset(gh, "gh-dash", "dash", "windows", "amd64"))
	assert.Equal(t, 3, SelectAsset(gh, "gh-dash", "dash", "darwin", "arm64"))
	assert.Equal(t, -1, SelectAsset(gh, "gh-dash", "dash", "linux", "amd64"))
}

func TestOlder(t *testing.T) {
	assert.True(t, Older("1.0.0", "1.2.0"))
	assert.True(t, Older("1.9.9", "2"))
	assert.False(t, Older("1.2.0", "1.2.0"))
	assert.False(t, Older("1.3.0+abc", "v1.2.1"))
	assert.False(t, Older("development", "9.0.0"))
	assert.False(t, Older("1.0.0", "next"))
}

func TestEnvironStripsSecrets(t *testing.T) {
	env := Environ([]string{"PATH=/bin", "GITEA_TOKEN=secret", "GH_TOKEN=secret", "GITHUB_TOKEN=secret", "GITEA_INSTANCE_URL=https://x", "HOME=/home/a", "GITEA_CLI_REPO=old/repo"}, map[string]string{"GITEA_CLI_REPO": "alice/project", "GITEA_CLI_EXTENSION": "hello"})
	assert.Equal(t, []string{"PATH=/bin", "HOME=/home/a", "GITEA_CLI_EXTENSION=hello", "GITEA_CLI_REPO=alice/project"}, env)
	env = Environ([]string{"GITEA_TOKEN=inherited"}, map[string]string{"GITEA_TOKEN": "granted"})
	assert.Equal(t, []string{"GITEA_TOKEN=granted"}, env)
}

// TestHelperProcess is the extension the Run tests start: it exits with GT_TEST_EXIT.
func TestHelperProcess(t *testing.T) {
	code := os.Getenv("GT_TEST_EXIT")
	if code == "" {
		t.Skip("run by the Run tests")
	}
	n, _ := strconv.Atoi(code)
	os.Stdout.WriteString(strings.Join(os.Args[len(os.Args)-1:], " "))
	os.Exit(n)
}

func helperCommand(t *testing.T, code string) (Command, *strings.Builder) {
	t.Helper()
	var out strings.Builder
	return Command{Path: os.Args[0], Args: []string{"-test.run=^TestHelperProcess$", "--", "hello"}, Env: Environ(os.Environ(), map[string]string{"GT_TEST_EXIT": code}), Stdout: &out}, &out
}

func TestRunPassesExitCode(t *testing.T) {
	command, out := helperCommand(t, "3")
	code, err := Run(command)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "hello", out.String())
	command, _ = helperCommand(t, "0")
	code, err = Run(command)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	_, err = Run(Command{Path: filepath.Join(t.TempDir(), "missing")})
	require.Error(t, err)

	command, out = helperCommand(t, "0")
	command.Interpreter, command.Path, command.Args = command.Path, command.Args[0], command.Args[1:]
	_, err = Run(command)
	require.NoError(t, err)
	assert.Equal(t, "hello", out.String(), "the interpreter runs the script with its arguments")
}

func TestInterpreterOnlyOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covers systems that execute scripts themselves")
	}
	interpreter, err := Interpreter("gt-hello", func(string) (string, error) {
		t.Fatal("no interpreter is looked up")
		return "", nil
	})
	require.NoError(t, err)
	assert.Empty(t, interpreter)
}
