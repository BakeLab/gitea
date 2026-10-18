// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type memoryKeyring struct {
	values map[string]string
	fail   bool
}

func (m *memoryKeyring) Get(_ string, k string) (string, error) {
	if m.fail {
		return "", errors.New("unavailable")
	}
	value, ok := m.values[k]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (m *memoryKeyring) Set(_, k, v string) error {
	if m.fail {
		return errors.New("unavailable")
	}
	m.values[k] = v
	return nil
}

func (m *memoryKeyring) Delete(_, k string) error {
	if m.fail {
		return errors.New("unavailable")
	}
	delete(m.values, k)
	return nil
}

// newTestManager couples config and credentials, failing the test on a missing store.
func newTestManager(t *testing.T, config *settings.Store, credentials *Store) Manager {
	t.Helper()
	manager, err := NewManager(config, credentials)
	require.NoError(t, err)
	return manager
}

func TestNewManagerRequiresBothStores(t *testing.T) {
	config := settings.New(t.TempDir())
	_, err := NewManager(nil, &Store{Dir: config.Dir})
	require.ErrorContains(t, err, "settings store")
	_, err = NewManager(config, nil)
	require.ErrorContains(t, err, "credential store")
}

func TestRegisterAndLogoutIsolateAccounts(t *testing.T) {
	config := settings.New(t.TempDir())
	credentials := &Store{Dir: config.Dir, Keyring: &memoryKeyring{values: map[string]string{}}}
	manager := newTestManager(t, config, credentials)
	for _, user := range []string{"alice", "bob", "charlie"} {
		require.NoError(t, manager.Register("https://example.org", user, "keyring", "token", HostOptions{}, Credential{AccessToken: "secret-" + user}))
	}
	removed, err := manager.Logout("https://example.org", "charlie")
	require.NoError(t, err)
	require.Equal(t, "secret-charlie", removed.AccessToken)
	s, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, s.Hosts.Hosts["https://example.org"].User)
	credential, err := credentials.Load("https://example.org", "alice", "keyring")
	require.NoError(t, err)
	require.Equal(t, "secret-alice", credential.AccessToken)
	_, err = credentials.Load("https://example.org", "charlie", "keyring")
	require.Error(t, err)
	data, err := os.ReadFile(filepath.Join(config.Dir, "hosts.yml"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
}

func TestFailedConfigPublishRollsBackCredentials(t *testing.T) {
	config := settings.New(t.TempDir())
	// An invalid auth method fails validation after the credential was saved.
	keyring := &memoryKeyring{values: map[string]string{}}
	credentials := &Store{Dir: config.Dir, Keyring: keyring}
	manager := newTestManager(t, config, credentials)
	require.Error(t, manager.Register("https://example.org", "alice", "keyring", "invalid", HostOptions{}, Credential{AccessToken: "secret"}))
	require.Empty(t, keyring.values)
	_, err := os.Stat(filepath.Join(config.Dir, "hosts.yml"))
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestNoSilentCredentialFallback(t *testing.T) {
	config := settings.New(t.TempDir())
	credentials := &Store{Dir: config.Dir, Keyring: &memoryKeyring{values: map[string]string{}, fail: true}}
	manager := newTestManager(t, config, credentials)
	require.Error(t, manager.Register("https://example.org", "alice", "keyring", "token", HostOptions{}, Credential{AccessToken: "secret"}))
	require.NoError(t, manager.Register("https://example.org", "alice", "file", "token", HostOptions{}, Credential{AccessToken: "secret"}))
	credential, err := credentials.Load("https://example.org", "alice", "file")
	require.NoError(t, err)
	require.Equal(t, "secret", credential.AccessToken)
}

func TestCredentialLoadDistinguishesMissingFromUnavailable(t *testing.T) {
	keyring := &memoryKeyring{values: map[string]string{}}
	store := &Store{Dir: t.TempDir(), Keyring: keyring}
	for _, backend := range []string{settings.StoreKeyring, settings.StoreFile} {
		_, err := store.Load("https://example.org", "alice", backend)
		require.ErrorContains(t, err, "run gt auth login", backend)
	}
	// Logging in again cannot fix a locked or unreachable keyring, so do not suggest it.
	keyring.fail = true
	_, err := store.Load("https://example.org", "alice", settings.StoreKeyring)
	require.EqualError(t, err, "credential storage unavailable: unavailable")
}

func TestCredentialStoreWithoutKeyringRejectsKeyringBackend(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	_, err := store.Load("https://example.org", "alice", settings.StoreKeyring)
	require.ErrorIs(t, err, errNoKeyring)
	require.ErrorIs(t, store.Save("https://example.org", "alice", settings.StoreKeyring, Credential{AccessToken: "secret"}), errNoKeyring)
	require.ErrorIs(t, store.Delete("https://example.org", "alice", settings.StoreKeyring), errNoKeyring)
}

func TestCredentialFilesRoundTripExpiry(t *testing.T) {
	store := &Store{Dir: t.TempDir()}
	expiring := Credential{AccessToken: "access", RefreshToken: "refresh", ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	for _, credential := range []Credential{{AccessToken: "token"}, expiring} {
		require.NoError(t, store.Save("https://example.org", "alice", settings.StoreFile, credential))
		loaded, err := store.Load("https://example.org", "alice", settings.StoreFile)
		require.NoError(t, err)
		require.Equal(t, credential, loaded)
	}
	require.NoError(t, store.Save("https://example.org", "alice", settings.StoreFile, Credential{AccessToken: "token"}))
	data, err := os.ReadFile(store.filename("https://example.org", "alice"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "expires_at")
	// Files written while a missing expiry was stored as the zero time still load without one.
	require.NoError(t, os.WriteFile(store.filename("https://example.org", "alice"), []byte(`{"access_token":"token","expires_at":"0001-01-01T00:00:00Z"}`), 0o600))
	loaded, err := store.Load("https://example.org", "alice", settings.StoreFile)
	require.NoError(t, err)
	require.True(t, loaded.ExpiresAt.IsZero())
}

func TestLogoutRemovesAccountWithMissingCredential(t *testing.T) {
	config := settings.New(t.TempDir())
	keyring := &memoryKeyring{values: map[string]string{}}
	manager := newTestManager(t, config, &Store{Dir: config.Dir, Keyring: keyring})
	require.NoError(t, manager.Register("https://example.org", "alice", settings.StoreKeyring, settings.AuthToken, HostOptions{}, Credential{AccessToken: "secret"}))
	clear(keyring.values)
	removed, err := manager.Logout("https://example.org", "alice")
	require.NoError(t, err)
	require.Empty(t, removed.AccessToken)
	s, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, s.Hosts.Hosts["https://example.org"].Users)
	require.NoError(t, manager.Register("https://example.org", "alice", settings.StoreKeyring, settings.AuthToken, HostOptions{}, Credential{AccessToken: "secret"}))
	// An unavailable keyring keeps the account rather than orphaning its token.
	keyring.fail = true
	_, err = manager.Logout("https://example.org", "alice")
	require.Error(t, err)
	s, err = config.Load()
	require.NoError(t, err)
	require.Contains(t, s.Hosts.Hosts["https://example.org"].Users, "alice")
}

func TestRegisterHTTPSignRejectsTokenAccounts(t *testing.T) {
	config := settings.New(t.TempDir())
	manager := newTestManager(t, config, &Store{Dir: config.Dir, Keyring: &memoryKeyring{values: map[string]string{}}})
	require.Error(t, manager.RegisterHTTPSign("https://example.org", "alice", settings.Account{AuthMethod: settings.AuthToken}, HostOptions{}))
	require.NoError(t, manager.RegisterHTTPSign("https://example.org", "alice", settings.Account{AuthMethod: settings.AuthSSH, CredentialStore: settings.StoreFile, SSHKey: "/keys/id"}, HostOptions{}))
	s, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, settings.Account{AuthMethod: settings.AuthSSH, SSHKey: "/keys/id"}, s.Hosts.Hosts["https://example.org"].Users["alice"])
	_, err = manager.Logout("https://example.org", "alice")
	require.NoError(t, err)
}

func TestRegisterReplacesAnAccountOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous settings.Account
		next     string
	}{
		{"same store", settings.Account{AuthMethod: settings.AuthToken, CredentialStore: settings.StoreFile}, settings.StoreFile},
		{"other store", settings.Account{AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreKeyring}, settings.StoreFile},
		{"SSH key account", settings.Account{AuthMethod: settings.AuthHTTPSign, SSHKey: "/keys/id"}, settings.StoreKeyring},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := settings.New(t.TempDir())
			keyring := &memoryKeyring{values: map[string]string{}}
			credentials := &Store{Dir: config.Dir, Keyring: keyring}
			manager := newTestManager(t, config, credentials)
			if tc.previous.Tokenless() {
				require.NoError(t, manager.RegisterHTTPSign("https://example.org", "alice", tc.previous, HostOptions{}))
			} else {
				require.NoError(t, manager.Register("https://example.org", "alice", tc.previous.CredentialStore, tc.previous.AuthMethod, HostOptions{}, Credential{AccessToken: "old"}))
			}
			require.NoError(t, manager.Register("https://example.org", "bob", settings.StoreFile, settings.AuthToken, HostOptions{}, Credential{AccessToken: "bob"}))
			err := manager.Register("https://example.org", "alice", tc.next, settings.AuthToken, HostOptions{}, Credential{AccessToken: "new"})
			require.ErrorIs(t, err, ErrAccountExists)
			require.NoError(t, manager.Register("https://example.org", "alice", tc.next, settings.AuthToken, HostOptions{Replace: true}, Credential{AccessToken: "new"}))
			s, err := config.Load()
			require.NoError(t, err)
			host := s.Hosts.Hosts["https://example.org"]
			require.Equal(t, "alice", host.User)
			require.Equal(t, settings.Account{AuthMethod: settings.AuthToken, CredentialStore: tc.next}, host.Users["alice"])
			credential, err := credentials.Load("https://example.org", "alice", tc.next)
			require.NoError(t, err)
			require.Equal(t, "new", credential.AccessToken)
			if !tc.previous.Tokenless() && tc.previous.CredentialStore != tc.next {
				_, err := credentials.Load("https://example.org", "alice", tc.previous.CredentialStore)
				require.Error(t, err, "the replaced credential leaves its old store")
			}
		})
	}
}

func TestFailedReplacementRestoresTheCredential(t *testing.T) {
	config := settings.New(t.TempDir())
	keyring := &memoryKeyring{values: map[string]string{}}
	credentials := &Store{Dir: config.Dir, Keyring: keyring}
	manager := newTestManager(t, config, credentials)
	require.NoError(t, manager.Register("https://example.org", "alice", settings.StoreKeyring, settings.AuthToken, HostOptions{}, Credential{AccessToken: "old"}))
	require.Error(t, manager.Register("https://example.org", "alice", settings.StoreFile, "invalid", HostOptions{Replace: true}, Credential{AccessToken: "new"}))
	credential, err := credentials.Load("https://example.org", "alice", settings.StoreKeyring)
	require.NoError(t, err)
	require.Equal(t, "old", credential.AccessToken)
	_, err = credentials.Load("https://example.org", "alice", settings.StoreFile)
	require.Error(t, err)
}
