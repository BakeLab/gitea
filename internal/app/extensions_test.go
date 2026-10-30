// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/extensions/github"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// fakeExtensionRepo serves the repository alice/gt-hello with a release of one asset built for
// this platform.
type fakeExtensionRepo struct {
	mu     sync.Mutex
	repoID int64
	tag    string
	commit string
	asset  []byte
	// digest is the SHA-256 of asset, which checksums.txt lists unless checksums is set.
	digest    string
	checksums string
	metadata  string
	// scriptOnly leaves the asset out of the release.
	scriptOnly bool
	// downloads counts the requests for the asset.
	downloads int
}

func newFakeExtensionRepo(content string) *fakeExtensionRepo {
	sum := sha256.Sum256([]byte(content))
	return &fakeExtensionRepo{repoID: 7, tag: "v1.0.0", commit: strings.Repeat("1", 40), asset: []byte(content), digest: hex.EncodeToString(sum[:])}
}

func (f *fakeExtensionRepo) assetName() string {
	name := "gt-hello-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func (f *fakeExtensionRepo) handler(t *testing.T, base func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		repo := "/api/v1/repos/alice/gt-hello"
		writeJSON := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(v))
		}
		attachment := func(id int64, name, uuid string, size int) map[string]any {
			return map[string]any{"id": id, "name": name, "uuid": uuid, "size": size, "browser_download_url": base() + "/alice/gt-hello/releases/download/" + f.tag + "/" + name}
		}
		checksums := f.checksums
		if checksums == "" && f.digest != "" {
			checksums = f.digest + "  " + f.assetName() + "\n"
		}
		release := func() map[string]any {
			assets := []any{}
			if !f.scriptOnly {
				assets = append(assets, attachment(1, f.assetName(), "u1", len(f.asset)))
			}
			if checksums != "" {
				assets = append(assets, attachment(2, "checksums.txt", "u2", len(checksums)))
			}
			return map[string]any{"id": 10, "tag_name": f.tag, "assets": assets}
		}
		switch path := r.URL.Path; {
		case path == repo:
			writeJSON(map[string]any{"id": f.repoID, "name": "gt-hello", "full_name": "alice/gt-hello", "default_branch": "main", "html_url": base() + "/alice/gt-hello", "clone_url": base() + "/alice/gt-hello.git"})
		case path == repo+"/releases/latest", path == repo+"/releases/tags/"+f.tag:
			writeJSON(release())
		case strings.HasPrefix(path, repo+"/releases/tags/"):
			w.WriteHeader(http.StatusNotFound)
		case path == repo+"/releases/10/assets/1":
			writeJSON(attachment(1, f.assetName(), "u1", len(f.asset)))
		case path == repo+"/releases/10/assets/2":
			writeJSON(attachment(2, "checksums.txt", "u2", len(checksums)))
		case path == "/attachments/u1":
			f.downloads++
			_, _ = w.Write(f.asset)
		case path == "/attachments/u2":
			_, _ = w.Write([]byte(checksums))
		case strings.HasPrefix(path, repo+"/git/commits/"):
			writeJSON(map[string]any{"sha": f.commit})
		case strings.HasPrefix(path, repo+"/raw/") && strings.HasSuffix(path, "/gt-extension.yml") && f.metadata != "":
			_, _ = w.Write([]byte(f.metadata))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// extensionRun records the extension runs of a test.
type extensionRun struct {
	commands []extensions.Command
	code     int
}

func (r *extensionRun) run(c extensions.Command) (int, error) {
	r.commands = append(r.commands, c)
	return r.code, nil
}

// env returns the value of key in the environment of the last run, and whether it is set.
func (r *extensionRun) env(key string) (string, bool) {
	env := r.commands[len(r.commands)-1].Env
	for _, entry := range slices.Backward(env) {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value, true
		}
	}
	return "", false
}

func extensionDeps(t *testing.T, repo *fakeExtensionRepo, env map[string]string) (Dependencies, *extensionRun, *httptest.Server) {
	t.Helper()
	var server *httptest.Server
	server = newAuthenticatedServer(repo.handler(t, func() string { return server.URL }))
	t.Cleanup(server.Close)
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, env)
	runs := &extensionRun{}
	deps.RunExtension = runs.run
	deps.Environ = func() []string { return []string{"PATH=/bin", "GITEA_TOKEN=inherited", "GH_TOKEN=inherited"} }
	return deps, runs, server
}

func installedExtension(t *testing.T, deps Dependencies, name string) extensions.Extension {
	t.Helper()
	extension, err := (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup(name)
	require.NoError(t, err)
	return extension
}

func TestExtensionInstallRunAndRemove(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	deps, runs, server := extensionDeps(t, repo, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	extension := installedExtension(t, deps, "hello")
	assert.Equal(t, extensions.KindBinary, extension.Kind)
	assert.Equal(t, server.URL, extension.Host)
	assert.Equal(t, "v1.0.0", extension.Tag)
	assert.Equal(t, repo.commit, extension.Commit)
	assert.Equal(t, repo.digest, extension.SHA256)
	assert.Equal(t, "checksums.txt", extension.DigestSource)
	assert.False(t, extension.Credentials)

	// Global flags before the name become variables; everything after it passes unchanged.
	require.NoError(t, Execute(context.Background(), deps, []string{"--repo", "alice/project", "hello", "--json", "a,b", "--repo", "x"}))
	require.Len(t, runs.commands, 1)
	assert.Equal(t, extension.Path(), runs.commands[0].Path)
	assert.Equal(t, []string{"--json", "a,b", "--repo", "x"}, runs.commands[0].Args)
	value, _ := runs.env("GITEA_CLI_REPO")
	assert.Equal(t, "alice/project", value)
	value, _ = runs.env("GITEA_CLI_EXTENSION")
	assert.Equal(t, "hello", value)
	for _, secret := range []string{"GITEA_TOKEN", "GH_TOKEN", "GITEA_INSTANCE_URL"} {
		_, set := runs.env(secret)
		assert.False(t, set, secret)
	}

	runs.code = 5
	err := Execute(context.Background(), deps, []string{"extension", "exec", "hello", "--flag"})
	assert.Equal(t, 5, clierr.ExitCode(err))
	assert.True(t, clierr.IsQuiet(err))
	assert.Equal(t, []string{"--flag"}, runs.commands[1].Args)

	err = Execute(context.Background(), deps, []string{"--json", "hello"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "extension hello cannot be combined with --json")
	runs.code = 0
	require.NoError(t, Execute(context.Background(), deps, []string{"--no-input", "--yes", "hello"}), "every command accepts --no-input and --yes")

	deps.Streams.Out.(interface{ Reset() }).Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "list", "--json", "name,tag,credentials,shadowed"}))
	assert.JSONEq(t, `[{"name":"hello","tag":"v1.0.0","credentials":false,"shadowed":false}]`, deps.Streams.Out.(fmt.Stringer).String())

	require.NoError(t, os.WriteFile(extension.Path(), []byte("replaced"), 0o755))
	err = Execute(context.Background(), deps, []string{"hello"})
	require.ErrorContains(t, err, "extension hello changed since it was installed")
	require.Len(t, runs.commands, 3, "a changed executable must not run")

	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "remove", "hello", "--yes"}))
	err = Execute(context.Background(), deps, []string{"hello"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `unknown command "hello"`)
}

func TestExtensionInstallVerifiesTheDownload(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	good := repo.digest
	repo.checksums = strings.Repeat(" ", extensions.MaxChecksumsSize+1)
	deps, _, _ := extensionDeps(t, repo, nil)
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}), "checksums.txt of v1.0.0 is larger than")
	repo.checksums = strings.Repeat("0", 64) + "  " + repo.assetName() + "\n"
	err := Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"})
	require.ErrorContains(t, err, "but checksums.txt lists")
	_, err = (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup("hello")
	require.ErrorIs(t, err, extensions.ErrNotInstalled)
	repo.checksums = good + "  " + repo.assetName() + "\n"
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	assert.Equal(t, "checksums.txt", installedExtension(t, deps, "hello").DigestSource)
}

func TestExtensionInstallWithoutDigestAsks(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	repo.digest = ""
	deps, _, _ := extensionDeps(t, repo, nil)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompter := &scriptedPrompter{}
	deps.Prompter = prompter
	err := Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello"})
	require.ErrorIs(t, err, terminal.ErrCanceled)
	assert.Equal(t, []string{"Install extension hello, which gt cannot verify?"}, prompter.confirmations)
	assert.Equal(t, []bool{false}, prompter.defaults)
	assert.Contains(t, deps.Streams.ErrOut.(fmt.Stringer).String(), "(not published; recorded now and checked before each run)")
	_, err = (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup("hello")
	require.ErrorIs(t, err, extensions.ErrNotInstalled)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	extension := installedExtension(t, deps, "hello")
	assert.Equal(t, "none", extension.DigestSource)
	assert.Equal(t, newFakeExtensionRepo("hello binary").digest, extension.SHA256)

	repo.tag, repo.commit = "v2.0.0", strings.Repeat("2", 40)
	err = Execute(context.Background(), deps, []string{"extension", "upgrade", "hello"})
	require.ErrorIs(t, err, terminal.ErrCanceled)
	assert.Equal(t, "v1.0.0", installedExtension(t, deps, "hello").Tag)
	deps.Streams.InTTY, deps.Streams.ErrTTY = false, false
	err = Execute(context.Background(), deps, []string{"extension", "upgrade", "hello"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	assert.Equal(t, "v1.0.0", installedExtension(t, deps, "hello").Tag)
}

func TestExtensionCredentialsOnlyWhenGranted(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	deps, runs, server := extensionDeps(t, repo, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes", "--allow-credentials"}))
	require.True(t, installedExtension(t, deps, "hello").Credentials)
	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	token, _ := runs.env("GITEA_TOKEN")
	assert.Equal(t, "token", token, "the token of the selected instance, not the inherited one")
	instance, _ := runs.env("GITEA_INSTANCE_URL")
	assert.Equal(t, server.URL, instance)
	_, set := runs.env("GH_TOKEN")
	assert.False(t, set, "only gh extensions get GH_TOKEN")

	policy := filepath.Join(t.TempDir(), "policy.yml")
	require.NoError(t, os.WriteFile(policy, []byte("extension_credentials: denied\n"), 0o600))
	deps.PolicyFile = policy
	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	_, set = runs.env("GITEA_TOKEN")
	assert.False(t, set, "the system policy denies credentials")
	err := Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes", "--force", "--allow-credentials"})
	require.ErrorContains(t, err, "the system policy "+policy+" denies them")
}

func TestExtensionsCanBeTurnedOff(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	deps, runs, server := extensionDeps(t, repo, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))

	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "extensions", "disabled"}))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"hello"}), "cannot run extension hello: extensions are disabled by extensions in config.yml")
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "extensions", "enabled"}))

	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_NO_EXTENSIONS": "1"})
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"hello"}), "disabled by GITEA_CLI_NO_EXTENSIONS")
	deps.Streams.Out.(interface{ Reset() }).Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"help"}))
	assert.NotContains(t, deps.Streams.Out.(fmt.Stringer).String(), "EXTENSION COMMANDS", "help offers no extension that cannot run")
	deps.Getenv = serverEnv(server.URL, nil)

	policy := filepath.Join(t.TempDir(), "policy.yml")
	deps.PolicyFile = policy
	require.NoError(t, os.WriteFile(policy, []byte("extension_owners: [https://gitea.com/gitea]\n"), 0o600))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"hello"}), "extensions from "+server.URL+"/alice are not allowed by extension_owners in the system policy")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes", "--force"}), "not allowed by extension_owners")
	// An owner entry admits that owner only on its own instance.
	require.NoError(t, os.WriteFile(policy, []byte("extension_owners: [https://other.example/alice]\n"), 0o600))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"hello"}), "extensions from "+server.URL+"/alice are not allowed by extension_owners")
	require.NoError(t, os.WriteFile(policy, []byte("extension_hosts: ["+server.URL+"]\n"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	// A local extension has no source an allowlist could admit.
	dir := filepath.Join(t.TempDir(), "gt-dev")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-dev"), []byte("#!/bin/sh\n"), 0o755))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", dir}), "local extensions are not allowed")
	require.NoError(t, os.Remove(policy))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}))
	require.NoError(t, os.WriteFile(policy, []byte("extension_hosts: ["+server.URL+"]\n"), 0o600))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"dev"}), "local extensions are not allowed")
	require.NoError(t, os.WriteFile(policy, []byte("extensions: sometimes\n"), 0o600))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"hello"}), "extensions must be enabled or disabled")
	assert.Len(t, runs.commands, 1)
}

func TestExtensionNamesYieldToCommands(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	deps, runs, _ := extensionDeps(t, repo, nil)
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-issue"}), "gt issue is a built-in command")
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "hello", "issue list"}))
	err := Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "the alias hello has that name")
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "hello"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}), "already installed")

	// An alias can expand to an extension, but cannot shadow one.
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "hi", "hello --loud"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"hi", "now"}))
	assert.Equal(t, []string{"--loud", "now"}, runs.commands[0].Args)
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"alias", "set", "hello", "issue list"}), "collides with a built-in command or an extension")
}

func TestExtensionUpgradeRefusesChangedSources(t *testing.T) {
	repo := newFakeExtensionRepo("hello v1")
	deps, _, server := extensionDeps(t, repo, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))

	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--no-input"}), "an extension that is up to date needs no --yes")
	assert.Equal(t, newFakeExtensionRepo("hello v1").digest, installedExtension(t, deps, "hello").SHA256)

	// The same tag now serves another asset.
	retagged := newFakeExtensionRepo("hello v1 again")
	repo.asset, repo.digest = retagged.asset, retagged.digest
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--yes"}), "release v1.0.0 of alice/gt-hello now holds a different")
	// The same tag now holds no asset, so it would install a script of the repository instead.
	repo.scriptOnly = true
	for _, args := range [][]string{{"--dry-run"}, {"--yes"}} {
		err := Execute(context.Background(), deps, append([]string{"extension", "upgrade", "hello"}, args...))
		require.ErrorContains(t, err, "release v1.0.0 of alice/gt-hello now installs the script gt-hello instead of the asset "+repo.assetName(), args)
	}
	repo.scriptOnly = false

	v2 := newFakeExtensionRepo("hello v2")
	repo.tag, repo.commit, repo.asset, repo.digest = "v2.0.0", strings.Repeat("2", 40), v2.asset, v2.digest
	repo.repoID = 8
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--yes"}), "is no longer the repository extension hello was installed from")
	repo.repoID = 7

	downloads := repo.downloads
	err := Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "confirmation required to upgrade extension hello; rerun with --yes")
	assert.Equal(t, downloads, repo.downloads, "nothing is downloaded that cannot be installed")

	deps.Streams.Out.(interface{ Reset() }).Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "--all", "--dry-run"}))
	assert.Equal(t, "hello\tv1.0.0\tv2.0.0\n", deps.Streams.Out.(fmt.Stringer).String())
	require.Equal(t, "v1.0.0", installedExtension(t, deps, "hello").Tag)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "--all", "--yes"}))
	upgraded := installedExtension(t, deps, "hello")
	assert.Equal(t, "v2.0.0", upgraded.Tag)
	assert.Equal(t, v2.digest, upgraded.SHA256)

	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes", "--force", "--pin", "v2.0.0"}))
	repo.tag = "v3.0.0"
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--yes"}))
	assert.Equal(t, "v2.0.0", installedExtension(t, deps, "hello").Tag, "a pinned extension stays")
	assert.Contains(t, deps.Streams.ErrOut.(fmt.Stringer).String(), "reinstall it with gt extension install --force "+server.URL+"/alice/gt-hello to change that")
}

func TestExtensionMetadataIsChecked(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	repo.metadata = "description: Say hello\nplatforms: [plan9/mips]\n"
	deps, _, server := extensionDeps(t, repo, nil)
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}), "does not support "+runtime.GOOS+"/"+runtime.GOARCH)
	repo.metadata = "description: Say hello\ncredentials: true\n"
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	assert.Equal(t, "Say hello", installedExtension(t, deps, "hello").Metadata.Description)
	assert.False(t, installedExtension(t, deps, "hello").Credentials, "gt cannot ask")
	assert.Contains(t, deps.Streams.ErrOut.(fmt.Stringer).String(), "Extension hello asks for your Gitea token; allow it with gt extension install --force --allow-credentials "+server.URL+"/alice/gt-hello")
	deps.Streams.Out.(interface{ Reset() }).Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"help"}))
	assert.Contains(t, deps.Streams.Out.(fmt.Stringer).String(), "EXTENSION COMMANDS\n  hello:")
}

func TestGitHubExtensionsAreReadAnonymously(t *testing.T) {
	content := []byte("gh hello")
	sum := sha256.Sum256(content)
	digest := "sha256:not-hex"
	name := "gh-hello-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"), r.URL.Path)
		switch r.URL.Path {
		case "/repos/owner/gh-hello":
			_, _ = fmt.Fprintf(w, `{"id":3,"name":"gh-hello","default_branch":"main","owner":{"login":"owner"}}`)
		case "/repos/owner/gh-hello/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":"v1","assets":[{"name":%q,"size":%d,"browser_download_url":%q,"digest":%q}]}`, name, len(content), server.URL+"/download/"+name, digest)
		case "/repos/owner/gh-hello/commits/v1":
			_, _ = w.Write([]byte(strings.Repeat("3", 40)))
		case "/download/" + name:
			_, _ = w.Write(content)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GH_TOKEN": "secret", "GITHUB_TOKEN": "secret"})
	deps.GitHub = func() github.Client { return github.Client{API: server.URL, HTTP: server.Client()} }
	runs := &extensionRun{}
	deps.RunExtension = runs.run
	install := []string{"extension", "install", "https://github.com/owner/gh-hello", "--yes"}
	require.ErrorContains(t, Execute(context.Background(), deps, install), "the source published an invalid digest")
	digest = "sha256:" + strings.Repeat("0", 64)
	require.ErrorContains(t, Execute(context.Background(), deps, install), "but the server lists "+strings.Repeat("0", 64))
	digest = fmt.Sprintf("sha256:%x", sum)
	require.NoError(t, Execute(context.Background(), deps, install))
	extension := installedExtension(t, deps, "hello")
	assert.Equal(t, extensions.PrefixGH, extension.Prefix)
	assert.Equal(t, github.Host, extension.Host)
	assert.Equal(t, "server", extension.DigestSource)
	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	assert.Equal(t, "gh-hello", filepath.Base(strings.TrimSuffix(runs.commands[0].Path, ".exe")))
}

func TestExtensionSearchReadsTheIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"), "gt has no account on the index's instance")
		switch {
		case r.URL.Path == "/api/v1/repos/gitea/cli-extensions":
			_, _ = w.Write([]byte(`{"id":1,"name":"cli-extensions","full_name":"gitea/cli-extensions","default_branch":"main"}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/gitea/cli-extensions/raw/"):
			_, _ = w.Write([]byte("extensions:\n  - name: hello\n    repository: https://gitea.com/alice/gt-hello\n    description: Say hello\n  - name: review\n    repository: https://gitea.com/bob/gt-review\n    description: Review faster\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "search", "review", "--index", server.URL + "/gitea/cli-extensions", "--json", "name,repository,platforms,installed"}))
	assert.JSONEq(t, `[{"name":"review","repository":"https://gitea.com/bob/gt-review","platforms":[],"installed":false}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "search", "nothing", "--index", server.URL + "/gitea/cli-extensions", "--json"}))
	assert.JSONEq(t, `[]`, out.String())
	err := Execute(context.Background(), deps, []string{"extension", "search", "--index", server.URL + "/gitea/missing"})
	require.EqualError(t, err, "extension index "+server.URL+"/gitea/missing not found; select another one with --index")
}

func TestLocalExtensionRunsInPlace(t *testing.T) {
	deps, out, _ := testDeps(t)
	runs := &extensionRun{}
	deps.RunExtension = runs.run
	dir := filepath.Join(t.TempDir(), "gt-dev")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", dir}), "has no executable gt-dev")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-dev"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}))
	// Changes to a local extension are expected while it is developed.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-dev"), []byte("#!/bin/sh\necho changed\n"), 0o755))
	require.NoError(t, Execute(context.Background(), deps, []string{"dev"}))
	assert.Equal(t, filepath.Join(dir, "gt-dev"), runs.commands[0].Path)
	// There is nothing to upgrade it to.
	require.EqualError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}), "extension dev is already installed from "+dir+"; reinstall it with --force, or remove it with gt extension remove dev")
	// gt-extension.yml applies as to a remote install.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-extension.yml"), []byte("platforms: [plan9/mips]\n"), 0o644))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", dir, "--force"}), "extension dev does not support "+runtime.GOOS+"/"+runtime.GOARCH)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-extension.yml"), []byte("credentials: true\n"), 0o644))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir, "--force"}))
	assert.False(t, installedExtension(t, deps, "dev").Credentials, "gt cannot ask")
	assert.Contains(t, deps.Streams.ErrOut.(fmt.Stringer).String(), "Extension dev asks for your Gitea token; allow it with gt extension install --force --allow-credentials "+dir)
	prompt := &scriptedPrompter{accept: true}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir, "--force"}))
	assert.True(t, installedExtension(t, deps, "dev").Credentials)
	assert.Equal(t, []string{"Give extension dev your Gitea token?"}, prompt.confirmations)
	deps.Streams.InTTY, deps.Streams.ErrTTY = false, false
	require.NoError(t, os.Remove(filepath.Join(dir, "gt-extension.yml")))

	// Help lists only the global flags an extension accepts.
	for _, args := range [][]string{{"help", "dev"}, {"extension", "exec", "--help"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args))
		assert.Contains(t, out.String(), "--cwd directory", args)
		assert.Contains(t, out.String(), "Global flags: --host, --user, --no-input, --yes; global flags go before the extension's name", args)
		assert.NotContains(t, out.String(), "--remote", args)
		assert.NotContains(t, out.String(), "--sudo", args)
	}
	// Managing extensions selects no repository.
	for _, args := range [][]string{{"extension", "--help"}, {"extension", "list", "--help"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args))
		assert.NotContains(t, out.String(), "-R, --repo", args)
		assert.NotContains(t, out.String(), "--remote", args)
	}
}

func TestBrokenExtensionManifestCanBeRemoved(t *testing.T) {
	deps, out, diagnostics := testDeps(t)
	for _, name := range []string{"gt-dev", "gt-other"} {
		dir := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.Mkdir(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755))
		require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}))
	}
	require.NoError(t, os.WriteFile(filepath.Join(deps.DataDir, "extensions", "dev", "manifest.yml"), []byte("name: ["), 0o600))

	err := Execute(context.Background(), deps, []string{"extension", "list", "--json", "name"})
	require.ErrorContains(t, err, "reinstall extension dev")
	assert.JSONEq(t, `[{"name":"other"}]`, out.String(), "the readable extensions are still listed")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "--all", "--yes"}), "reinstall extension dev")
	assert.Contains(t, diagnostics.String(), "Extension other runs from", "the other extensions are still upgraded")
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "remove", "dev", "--yes"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "list", "--json", "name"}))
	assert.JSONEq(t, `[{"name":"other"}]`, out.String())
}

func TestGHExtensionGetsGHCredentialsAndTheShim(t *testing.T) {
	server := newAuthenticatedServer(http.NotFoundHandler())
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	runs := &extensionRun{}
	deps.RunExtension = runs.run
	deps.Environ = func() []string { return []string{"PATH=/bin"} }
	dir := filepath.Join(t.TempDir(), "gh-dev")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gh-dev"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir, "--allow-credentials", "--compat-gh"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"dev"}))
	host, _ := runs.env("GH_HOST")
	assert.Equal(t, strings.TrimPrefix(server.URL, "http://"), host)
	for _, key := range []string{"GH_TOKEN", "GH_ENTERPRISE_TOKEN"} {
		token, _ := runs.env(key)
		assert.Equal(t, "token", token, key)
	}
	pathList, _ := runs.env("PATH")
	assert.Equal(t, filepath.Join(deps.DataDir, "shims")+string(os.PathListSeparator)+"/bin", pathList)
	executable, err := os.Executable()
	require.NoError(t, err)
	shim := filepath.Join(deps.DataDir, "shims", "gh")
	if runtime.GOOS == "windows" {
		shim += ".exe"
	}
	assert.True(t, same(shim, executable), "the shim runs gt")
}

func TestExtensionAliasesRunTheExtension(t *testing.T) {
	deps, _, diagnostics := testDeps(t)
	runs := &extensionRun{}
	deps.RunExtension = func(c extensions.Command) (int, error) {
		runs.commands = append(runs.commands, c)
		return 0, nil
	}
	install := func(name, metadata string) {
		dir := filepath.Join(t.TempDir(), "gt-"+name)
		require.NoError(t, os.Mkdir(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-"+name), []byte("#!/bin/sh\n"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-extension.yml"), []byte(metadata), 0o644))
		require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}))
	}
	install("ee", "aliases:\n  user-group: ee-user-group\n  issue: ee-issue\n  shared: ee-shared\n")
	install("zz", "aliases:\n  shared: zz-shared\n")
	// Aliases that cannot be used are warned about; the extension is installed regardless.
	diagnostics.Reset()
	install("yy", "aliases:\n  issue: x\n  user-group: x\n  shared: x\n  yy: x\n  Bad: x\n  ok: ok\n")
	for _, want := range []string{"alias gt issue of extension yy: gt issue is a built-in command", "gt user-group of extension yy: extension ee declares it too", "gt shared of extension yy: extension ee declares it too", "gt yy of extension yy: it is the name of the extension", "gt Bad of extension yy: its name must use"} {
		assert.Contains(t, diagnostics.String(), want)
	}
	assert.NotContains(t, diagnostics.String(), "gt ok of")
	run := func(args ...string) []string {
		runs.commands = nil
		require.NoError(t, Execute(context.Background(), deps, args))
		require.Len(t, runs.commands, 1)
		return runs.commands[0].Args
	}
	assert.Equal(t, []string{"ee-user-group", "list", "--json"}, run("user-group", "list", "--json"))
	assert.Equal(t, []string{"ee-user-group", "list"}, run("--host", "https://example.com", "user-group", "list"))
	assert.Equal(t, []string{"ee-shared"}, run("shared"), "the first extension by name wins")

	// Built-in commands and the user's own aliases win.
	runs.commands = nil
	require.Error(t, Execute(context.Background(), deps, []string{"issue", "bogus"}))
	assert.Empty(t, runs.commands)
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "user-group", "zz mine"}))
	assert.Equal(t, []string{"mine", "list"}, run("user-group", "list"))

	// An extension that takes over an alias says so.
	diagnostics.Reset()
	install("aa", "aliases:\n  shared: aa-shared\n")
	install("ok", "")
	assert.Contains(t, diagnostics.String(), "gt shared now runs extension aa instead of extension ee")
	assert.Contains(t, diagnostics.String(), "gt ok now runs extension ok instead of the alias extension yy declares")
	assert.Equal(t, []string{"aa-shared"}, run("shared"))
}

func TestExtensionCompletionAsksTheExtension(t *testing.T) {
	deps, out, _ := testDeps(t)
	runs := &extensionRun{}
	deps.RunExtension = func(c extensions.Command) (int, error) {
		runs.commands = append(runs.commands, c)
		_, _ = c.Stdout.Write([]byte("fast\tGo fast\nslow\n:4\n"))
		return 0, nil
	}
	dir := filepath.Join(t.TempDir(), "gt-dev")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-dev"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-extension.yml"), []byte("completion: cobra\n"), 0o644))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}))
	values, directive := complete(t, deps, out, "dev", "--speed", "f")
	assert.Equal(t, []string{"fast\tGo fast", "slow"}, values)
	assert.EqualValues(t, 4, directive)
	assert.Equal(t, []string{"__complete", "--speed", "f"}, runs.commands[0].Args)
	values, _ = complete(t, deps, out, "d")
	assert.Contains(t, values, "dev\tExtension from "+dir)
}

func TestChangedExtensionIsNotAskedToComplete(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	repo.metadata = "completion: cobra\n"
	deps, runs, _ := extensionDeps(t, repo, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	out := deps.Streams.Out.(*bytes.Buffer)
	complete(t, deps, out, "hello", "")
	require.Len(t, runs.commands, 1)
	require.NoError(t, os.WriteFile(installedExtension(t, deps, "hello").Path(), []byte("replaced"), 0o755))
	values, _ := complete(t, deps, out, "hello", "")
	assert.Empty(t, values)
	assert.Len(t, runs.commands, 1, "a changed executable must not run, not even to complete")
}

func TestGHShimAnswersOnlyKnownCommands(t *testing.T) {
	deps, _, _ := testDeps(t)
	err := ExecuteGH(context.Background(), deps, []string{"pr", "list"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "gh pr list is not supported by the gh shim of gt")
	err = ExecuteGH(context.Background(), deps, []string{"repo", "view", "--json", "nameWithOwner"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `unknown JSON field "nameWithOwner"`)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "token gh-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_INSTANCE_URL": server.URL, "GH_TOKEN": "gh-token"})
	require.NoError(t, ExecuteGH(context.Background(), deps, []string{"api", "repos/alice/project"}))
	assert.JSONEq(t, `{"ok":true}`, out.String())
}

func TestScriptExtensionIsClonedAtItsCommit(t *testing.T) {
	gittest.Isolate(t)
	source := filepath.Join(t.TempDir(), "gt-hello")
	runGit := func(args ...string) string {
		output, err := (git.System{}).Output(context.Background(), source, false, args...)
		require.NoError(t, err, args)
		return output
	}
	require.NoError(t, os.Mkdir(source, 0o755))
	runGit("init", "--quiet")
	require.NoError(t, os.WriteFile(filepath.Join(source, "gt-hello"), []byte("#!/bin/sh\necho v1\n"), 0o755))
	runGit("add", "gt-hello")
	runGit("commit", "--quiet", "-m", "v1")
	commit := runGit("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(source, "gt-hello"), []byte("#!/bin/sh\necho v2\n"), 0o755))
	runGit("commit", "--quiet", "-am", "v2")

	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/gt-hello":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "name": "gt-hello", "full_name": "alice/gt-hello", "default_branch": "main", "clone_url": source})
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/gt-hello/git/commits/"):
			_, _ = w.Write([]byte(`{"sha":"` + commit + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = git.System{}
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	extension := installedExtension(t, deps, "hello")
	assert.Equal(t, extensions.KindScript, extension.Kind)
	assert.Equal(t, commit, extension.Commit)
	content, err := os.ReadFile(extension.Path())
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\necho v1\n", string(content))
	digest, err := extensions.FileSHA256(extension.Path())
	require.NoError(t, err)
	assert.Equal(t, digest, extension.SHA256)

	runs := &extensionRun{}
	deps.RunExtension = runs.run
	require.NoError(t, os.WriteFile(extension.Path(), []byte("#!/bin/sh\necho replaced\n"), 0o755))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"hello"}), "extension hello changed since it was installed")
	assert.Empty(t, runs.commands, "a changed script must not run")
}

func TestScriptExtensionSymlinkIsRefused(t *testing.T) {
	gittest.Isolate(t)
	source := filepath.Join(t.TempDir(), "gt-hello")
	runGit := func(args ...string) string {
		output, err := (git.System{}).Output(context.Background(), source, false, args...)
		require.NoError(t, err, args)
		return output
	}
	require.NoError(t, os.Mkdir(source, 0o755))
	runGit("init", "--quiet")
	target := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755))
	if err := os.Symlink(target, filepath.Join(source, "gt-hello")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	runGit("add", "gt-hello")
	runGit("commit", "--quiet", "-m", "link")
	commit := runGit("rev-parse", "HEAD")

	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/gt-hello":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "name": "gt-hello", "full_name": "alice/gt-hello", "default_branch": "main", "clone_url": source})
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/gt-hello/git/commits/"):
			_, _ = w.Write([]byte(`{"sha":"` + commit + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = git.System{}
	err := Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"})
	require.ErrorContains(t, err, "as a symbolic link")
	_, err = (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup("hello")
	assert.Error(t, err, "a refused extension must not be installed")
}

func TestExtensionsWithoutGrantGetNoStoredCredentials(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	dir := filepath.Join(t.TempDir(), "gt-hello")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-hello"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir}))
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_EXTENSION": "hello"})
	for _, args := range [][]string{{"auth", "token"}, {"api", "user"}} {
		err := Execute(context.Background(), deps, args)
		require.Equal(t, clierr.Authentication, clierr.ExitCode(err), args)
		require.ErrorContains(t, err, "extension hello was not given your credentials; allow them with gt extension install --force --allow-credentials "+dir, args)
	}
	require.Empty(t, out.String())
	// A granted extension gets GITEA_TOKEN, which gt uses as usual.
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_EXTENSION": "hello", "GITEA_TOKEN": "granted", "GITEA_CLI_HOST": server.URL})
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user"}))

	// It can also use the stored accounts, unless the system policy denies credentials.
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir, "--force", "--allow-credentials"}))
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_EXTENSION": "hello"})
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user"}))
	policy := filepath.Join(t.TempDir(), "policy.yml")
	require.NoError(t, os.WriteFile(policy, []byte("extension_credentials: denied\n"), 0o600))
	deps.PolicyFile = policy
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"api", "user"}), "extension hello was not given your credentials")
}

func TestExtensionGetsNoTokenOfAnotherInstance(t *testing.T) {
	repo := newFakeExtensionRepo("hello binary")
	deps, runs, server := extensionDeps(t, repo, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes", "--allow-credentials"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"--host", "https://other.example", "hello"}))
	_, set := runs.env("GITEA_TOKEN")
	assert.False(t, set, "the GITEA_TOKEN of "+server.URL+" must not reach another instance")
	host, _ := runs.env("GITEA_CLI_HOST")
	assert.Equal(t, "https://other.example", host)
	// Nor to another account than its own, which gt cannot check without a request.
	require.NoError(t, Execute(context.Background(), deps, []string{"--user", "bob", "hello"}))
	_, set = runs.env("GITEA_TOKEN")
	assert.False(t, set, "the GITEA_TOKEN of an unknown account must not reach an extension running as bob")
	// Without its instance, the token cannot be passed, which gt explains as for its own commands.
	deps.Getenv = envFunc(map[string]string{"GITEA_TOKEN": "unbound"})
	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	assert.Contains(t, deps.Streams.ErrOut.(fmt.Stringer).String(), "Could not pass your credentials to extension hello: GITEA_TOKEN requires GITEA_CLI_HOST or GITEA_INSTANCE_URL naming its instance")
}

func TestExtensionGetsARefreshedOAuthToken(t *testing.T) {
	refreshed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" || !refreshed {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "refresh_token": "refresh2", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer server.Close()
	deps, _, diagnostics := testDeps(t)
	runs := &extensionRun{}
	deps.RunExtension = runs.run
	require.NoError(t, deps.Credentials.Save(server.URL, "alice", settings.StoreFile, auth.Credential{AccessToken: "stale", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour)}))
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: map[string]settings.Account{"alice": {AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreFile}}})
	dir := filepath.Join(t.TempDir(), "gt-hello")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gt-hello"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", dir, "--allow-credentials"}))

	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	_, set := runs.env("GITEA_TOKEN")
	assert.False(t, set)
	assert.Contains(t, diagnostics.String(), "Could not pass your credentials to extension hello")

	refreshed = true
	require.NoError(t, Execute(context.Background(), deps, []string{"hello"}))
	token, _ := runs.env("GITEA_TOKEN")
	assert.Equal(t, "fresh", token)
}

func TestExtensionUpgradeRefusesTransferredRepository(t *testing.T) {
	repo := newFakeExtensionRepo("hello v1")
	var server *httptest.Server
	// moved is the full name Gitea now answers the old one with, under the same ID.
	moved := ""
	server = newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if moved != "" && r.URL.Path == "/api/v1/repos/alice/gt-hello" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": repo.repoID, "name": path.Base(moved), "full_name": moved, "default_branch": "main"})
			return
		}
		repo.handler(t, func() string { return server.URL }).ServeHTTP(w, r)
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, nil)
	deps.RunExtension = (&extensionRun{}).run
	moved = "alice/gt-issue"
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}), "gt issue is a built-in command", "the current name of a renamed repository decides")
	moved = ""
	require.NoError(t, Execute(context.Background(), deps, []string{"extension", "install", "alice/gt-hello", "--yes"}))
	moved = "alice/gt-howdy"
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--yes"}), "the repository of extension hello is now named gt-howdy")
	moved = "mallory/gt-hello"
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"extension", "upgrade", "hello", "--yes"}), "is no longer the repository extension hello was installed from, such as after a transfer; reinstall it with gt extension install --force "+server.URL+"/mallory/gt-hello to trust it")
	assert.Equal(t, "alice", installedExtension(t, deps, "hello").Owner)
}

func TestExtensionCreateReleaseWorkflowPublishesFromAFreshRunner(t *testing.T) {
	deps, _, diagnostics := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{"init --quiet": "", "add --all": ""}}
	deps.Streams.OutTTY = true
	dir := t.TempDir()
	require.NoError(t, Execute(context.Background(), deps, []string{"--cwd", dir, "extension", "create", "hello", "--precompiled=go"}))
	assert.Contains(t, diagnostics.String(), "cd "+filepath.Join(dir, "gt-hello")+" && go build && gt extension install .")
	data, err := os.ReadFile(filepath.Join(dir, "gt-hello", ".gitea", "workflows", "release.yml"))
	require.NoError(t, err)
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	step := workflow.Jobs["release"].Steps[3]
	require.Equal(t, "release", step.Name)

	created := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/gt-hello/releases", r.URL.Path)
		created = true
		_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1.0.0"}`))
	}))
	defer server.Close()
	// What a Gitea Actions runner substitutes, with no gt configuration of its own.
	actions := strings.NewReplacer("${{ github.server_url }}", server.URL, "${{ github.repository }}", "alice/gt-hello", "${{ secrets.GITHUB_TOKEN }}", "token")
	env := map[string]string{}
	for key, value := range step.Env {
		env[key] = actions.Replace(value)
	}
	command, ok := strings.CutPrefix(strings.TrimSpace(step.Run), "go run gitea.dev/cli/cmd/gt@latest ")
	require.True(t, ok, step.Run)
	var args []string
	shell := strings.NewReplacer(`"$GITHUB_REF_NAME"`, "v1.0.0", `"$GITHUB_SERVER_URL"`, server.URL, `"$GITHUB_REPOSITORY"`, "alice/gt-hello")
	for _, arg := range strings.Fields(shell.Replace(command)) {
		if arg != "dist/*" && arg != `\` {
			args = append(args, arg)
		}
	}
	deps, _, _ = testDeps(t)
	deps.Getenv = envFunc(env)
	require.NoError(t, Execute(context.Background(), deps, args))
	require.True(t, created)
}

func TestScriptExtensionsWithoutAnAccountAreClonedWithoutCredentials(t *testing.T) {
	commit := strings.Repeat("4", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/gt-hello", r.URL.Path == "/repos/owner/gh-hello":
			_, _ = fmt.Fprintf(w, `{"id":7,"name":%q,"full_name":"alice/gt-hello","default_branch":"main","owner":{"login":"owner"},"clone_url":"https://example.test/clone.git"}`, path.Base(r.URL.Path))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/gt-hello/git/commits/"):
			_, _ = w.Write([]byte(`{"sha":"` + commit + `"}`))
		case r.URL.Path == "/repos/owner/gh-hello/commits/main":
			_, _ = w.Write([]byte(commit))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, repository := range []string{server.URL + "/alice/gt-hello", "https://github.com/owner/gh-hello"} {
		deps, _, _ := testDeps(t)
		recorded := &fakeGit{}
		deps.Git = recorded
		deps.GitHub = func() github.Client { return github.Client{API: server.URL, HTTP: server.Client()} }
		require.Error(t, Execute(context.Background(), deps, []string{"extension", "install", repository, "--yes"}))
		require.NotEmpty(t, recorded.calls, repository)
		assert.True(t, strings.HasPrefix(recorded.calls[0], "-c credential.helper= clone "), recorded.calls[0])
	}
}
