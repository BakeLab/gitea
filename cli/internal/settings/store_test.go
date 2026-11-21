// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDoesNotCreateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	s := New(dir)
	snapshot, err := s.Load()
	require.NoError(t, err)
	require.Equal(t, "enabled", snapshot.Preferences.Prompt)
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestMalformedFilesAreNeverOverwritten(t *testing.T) {
	for _, input := range []string{"version: '2'\n", "version: '1'\nunknown: true\n", "version: '1'\n---\nversion: '1'\n", "version: '1'\nprompt: nonsense\n", "bad: ["} {
		t.Run(input, func(t *testing.T) {
			s := New(t.TempDir())
			file := filepath.Join(s.Dir, "config.yml")
			require.NoError(t, os.WriteFile(file, []byte(input), 0o600))
			_, err := s.Load()
			require.Error(t, err)
			err = s.Update(TargetPreferences, func(snapshot *Snapshot) error { snapshot.Preferences.Editor = "vi"; return nil })
			require.Error(t, err)
			actual, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, input, string(actual))
			_, err = os.Stat(filepath.Join(s.Dir, "settings.lock"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	for raw, want := range map[string]string{"https://EXAMPLE.org:443/": "https://example.org", "https://example.org:3000/gitea/": "https://example.org:3000/gitea", "http://example.org:80": "http://example.org", "https://[::1]:443/": "https://[::1]"} {
		actual, err := NormalizeHost(raw)
		require.NoError(t, err)
		require.Equal(t, want, actual)
	}
	for _, raw := range []string{"example.org", "ftp://example.org", "https://user:password@example.org", "https://example.org?q=1", "https://example.org/#fragment", "https://example.org/a%2fb"} {
		_, err := NormalizeHost(raw)
		require.Error(t, err, raw)
	}
}

func TestConcurrentUpdatesAndHostSeparation(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			store := New(dir)
			err := store.Update(TargetPreferences, func(snapshot *Snapshot) error {
				snapshot.Preferences.Aliases[string(rune('a'+i))] = "issue list"
				return nil
			})
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	store := New(dir)
	snapshot, err := store.Load()
	require.NoError(t, err)
	require.Len(t, snapshot.Preferences.Aliases, 8)
	require.NoError(t, store.Update(TargetHosts, func(s *Snapshot) error {
		s.Hosts.Hosts["https://example.org/base"] = Host{User: "alice", Users: map[string]Account{"alice": {AuthMethod: "token", CredentialStore: "file"}, "bob": {AuthMethod: "oauth", CredentialStore: "keyring"}}}
		return nil
	}))
	snapshot, err = store.Load()
	require.NoError(t, err)
	require.Len(t, snapshot.Hosts.Hosts, 1)
	data, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "alice")
	info, err := os.Stat(filepath.Join(dir, "hosts.yml"))
	require.NoError(t, err)
	if runtime.GOOS != "windows" { // Windows reports 0666 regardless of the requested mode
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	// A change to the file an update does not write fails instead of being dropped.
	require.Error(t, store.Update(TargetHosts, func(s *Snapshot) error { s.Preferences.Editor = "vi"; return nil }))
	require.Error(t, store.Update(TargetPreferences, func(s *Snapshot) error { delete(s.Hosts.Hosts, "https://example.org/base"); return nil }))
	snapshot, err = store.Load()
	require.NoError(t, err)
	require.Empty(t, snapshot.Preferences.Editor)
	require.Len(t, snapshot.Hosts.Hosts, 1)
}

func TestUpdatePersistsOnlyExplicitPreferences(t *testing.T) {
	s := New(t.TempDir())
	require.NoError(t, s.Update(TargetPreferences, func(snapshot *Snapshot) error { snapshot.Preferences.Editor = "vi"; return nil }))
	snapshot, err := s.Load()
	require.NoError(t, err)
	require.True(t, snapshot.Configured["editor"])
	for _, key := range []string{"git_protocol", "prompt", "color", "hyperlinks", "markdown", "aliases"} {
		require.False(t, snapshot.Configured[key], key)
	}
	require.Equal(t, Defaults().GitProtocol, snapshot.Preferences.GitProtocol)
	require.NoError(t, s.Update(TargetPreferences, func(snapshot *Snapshot) error { snapshot.Preferences.Color = "never"; return nil }))
	data, err := os.ReadFile(filepath.Join(s.Dir, "config.yml"))
	require.NoError(t, err)
	require.Equal(t, "version: \"1\"\neditor: vi\ncolor: never\n", string(data))
}

func TestUpdateKeepsASymlinkedFile(t *testing.T) {
	s, dotfiles := New(t.TempDir()), t.TempDir()
	target := filepath.Join(dotfiles, "config.yml")
	require.NoError(t, os.WriteFile(target, []byte("version: \"1\"\n"), 0o600))
	if err := os.Symlink(target, filepath.Join(s.Dir, "config.yml")); err != nil {
		t.Skip("symbolic links are unavailable:", err)
	}
	require.NoError(t, s.Update(TargetPreferences, func(snapshot *Snapshot) error { snapshot.Preferences.Editor = "vi"; return nil }))
	link, err := os.Readlink(filepath.Join(s.Dir, "config.yml"))
	require.NoError(t, err)
	require.Equal(t, target, link)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "version: \"1\"\neditor: vi\n", string(data))

	// A link to a file not created yet, through a relative link, creates that file.
	require.NoError(t, os.Symlink("hosts.yml", filepath.Join(dotfiles, "relative.yml")))
	require.NoError(t, os.Symlink(filepath.Join(dotfiles, "relative.yml"), filepath.Join(s.Dir, "hosts.yml")))
	require.NoError(t, s.Update(TargetHosts, func(snapshot *Snapshot) error {
		snapshot.Hosts.Hosts = map[string]Host{"https://example.org": {}}
		return nil
	}))
	info, err := os.Lstat(filepath.Join(s.Dir, "hosts.yml"))
	require.NoError(t, err)
	require.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink, "the dangling link is kept")
	data, err = os.ReadFile(filepath.Join(dotfiles, "hosts.yml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "https://example.org")

	missing := filepath.Join(dotfiles, "missing")
	require.NoError(t, os.Remove(filepath.Join(s.Dir, "config.yml")))
	require.NoError(t, os.Symlink(filepath.Join(missing, "config.yml"), filepath.Join(s.Dir, "config.yml")))
	err = s.Update(TargetPreferences, func(snapshot *Snapshot) error { snapshot.Preferences.Editor = "vi"; return nil })
	require.EqualError(t, err, "cannot write "+filepath.Join(s.Dir, "config.yml")+": its target directory "+missing+" does not exist")
}

func TestDisplayPreferencesAcceptOnlyTheirValues(t *testing.T) {
	for _, tc := range []struct {
		config string
		valid  bool
	}{
		{"hyperlinks: never\n", true},
		{"hyperlinks: always\nmarkdown: raw\n", true},
		{"hyperlinks: off\n", false},
		{"markdown: never\n", false},
	} {
		t.Run(tc.config, func(t *testing.T) {
			s := New(t.TempDir())
			require.NoError(t, os.WriteFile(filepath.Join(s.Dir, "config.yml"), []byte("version: '1'\n"+tc.config), 0o600))
			_, err := s.Load()
			require.Equal(t, tc.valid, err == nil, "%v", err)
		})
	}
}

func TestTokenlessAccounts(t *testing.T) {
	for method, want := range map[string]bool{AuthToken: false, AuthOAuth: false, AuthSSH: true, AuthHTTPSign: true} {
		require.Equal(t, want, Account{AuthMethod: method}.Tokenless(), method)
	}
}

func TestFilesOfAnotherVersionAreReportedByVersion(t *testing.T) {
	for _, name := range []string{"config.yml", "hosts.yml"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("version: \"2\"\nnew_setting: true\n"), 0o600))
		_, err := New(dir).Load()
		require.EqualError(t, err, "unsupported configuration version in "+filepath.Join(dir, name))
	}
}

func TestHeaderNamesAreReadInCanonicalForm(t *testing.T) {
	dir := t.TempDir()
	write := func(headers string) error {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte("version: \"1\"\nhosts:\n  https://example.org:\n    users: {}\n    headers:\n"+headers), 0o600))
		_, err := New(dir).Load()
		return err
	}
	require.NoError(t, write("      x-api-key: a\n"))
	snapshot, err := New(dir).Load()
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-Api-Key": "a"}, snapshot.Hosts.Hosts["https://example.org"].Headers)
	require.EqualError(t, write("      x-api-key: a\n      X-Api-Key: b\n"), "hosts.yml sets the header X-Api-Key of https://example.org twice")
}
