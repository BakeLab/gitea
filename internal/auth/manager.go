// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"errors"

	"gitea.dev/cli/internal/settings"
)

// Manager coordinates account metadata and credential changes under one lock.
type Manager struct {
	settings    *settings.Store
	credentials *Store
}

// NewManager couples the settings and credential stores that account changes keep consistent.
func NewManager(settingsStore *settings.Store, credentials *Store) (Manager, error) {
	if settingsStore == nil {
		return Manager{}, errors.New("account manager requires a settings store")
	}
	if credentials == nil {
		return Manager{}, errors.New("account manager requires a credential store")
	}
	return Manager{settings: settingsStore, credentials: credentials}, nil
}

// HostOptions are instance settings recorded together with a new account.
type HostOptions struct {
	OAuthClientID string
	Insecure      bool
}

// Register publishes account metadata and compensates credential writes on failure.
func (m Manager) Register(instance, user, backend, method string, options HostOptions, credential Credential) error {
	return m.register(instance, user, settings.Account{AuthMethod: method, CredentialStore: backend}, options, &credential)
}

// RegisterHTTPSign publishes SSH/HTTP-signature account metadata without storing a token.
func (m Manager) RegisterHTTPSign(instance, user string, account settings.Account, options HostOptions) error {
	if !account.Tokenless() {
		return errors.New("HTTP signature registration requires auth_method ssh or httpsign")
	}
	account.CredentialStore = ""
	return m.register(instance, user, account, options, nil)
}

func (m Manager) register(instance, user string, account settings.Account, options HostOptions, credential *Credential) error {
	return m.settings.Transaction(settings.TargetHosts, func(s *settings.Snapshot) (func() error, error) {
		host := s.Hosts.Hosts[instance]
		if host.Users == nil {
			host.Users = map[string]settings.Account{}
		}
		if _, exists := host.Users[user]; exists {
			return nil, errors.New("account already configured; logout before authenticating again")
		}
		var rollback func() error
		if credential != nil {
			if err := m.credentials.Save(instance, user, account.CredentialStore, *credential); err != nil {
				return nil, err
			}
			rollback = func() error { return m.credentials.Delete(instance, user, account.CredentialStore) }
		}
		host.Users[user] = account
		host.User = user
		if options.OAuthClientID != "" {
			host.OAuthClientID = options.OAuthClientID
		}
		host.Insecure = host.Insecure || options.Insecure
		s.Hosts.Hosts[instance] = host
		return rollback, nil
	})
}

// Logout removes one account and restores its credential if publication fails.
// An unreadable credential does not prevent removing the account.
func (m Manager) Logout(instance, user string) error {
	return m.settings.Transaction(settings.TargetHosts, func(s *settings.Snapshot) (func() error, error) {
		host := s.Hosts.Hosts[instance]
		account, exists := host.Users[user]
		if !exists {
			return nil, errors.New("account is not configured")
		}
		var restore func() error
		if !account.Tokenless() {
			credential, err := m.credentials.Load(instance, user, account.CredentialStore)
			if err == nil {
				// Preserve a compensating copy until the account file is successfully published.
				restore = func() error { return m.credentials.Save(instance, user, account.CredentialStore, credential) }
			}
			// A missing credential deletes cleanly; any other failure keeps the account so
			// its token is not orphaned in storage.
			if err := m.credentials.Delete(instance, user, account.CredentialStore); err != nil {
				return nil, err
			}
		}
		delete(host.Users, user)
		if host.User == user {
			host.User = ""
			if len(host.Users) == 1 {
				for name := range host.Users {
					host.User = name
				}
			}
		}
		s.Hosts.Hosts[instance] = host
		return restore, nil
	})
}
