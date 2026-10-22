// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"cmp"
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
	// Headers are added to the instance's headers, replacing those of the same name.
	Headers map[string]string
	// ClientCert and ClientKey replace the instance's TLS client certificate when set.
	ClientCert, ClientKey string
	// Replace lets a registration replace the metadata and stored credential of an account that is
	// already configured, as when logging in again with a new token.
	Replace bool
}

// Apply returns host with the settings of options: those it sets replace host's, and headers are
// added to host's.
func (options HostOptions) Apply(host settings.Host) settings.Host {
	host.OAuthClientID = cmp.Or(options.OAuthClientID, host.OAuthClientID)
	host.Insecure = host.Insecure || options.Insecure
	if options.ClientCert != "" {
		host.ClientCert, host.ClientKey = options.ClientCert, options.ClientKey
	}
	return host.WithHeaders(options.Headers)
}

// ErrAccountExists rejects a registration of a configured account without HostOptions.Replace.
var ErrAccountExists = errors.New("account already configured")

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
		previous, exists := host.Users[user]
		if exists && !options.Replace {
			return nil, ErrAccountExists
		}
		var undo []func() error
		rollback := func() error {
			var err error
			for i := len(undo) - 1; i >= 0; i-- {
				err = errors.Join(err, undo[i]())
			}
			return err
		}
		// A replaced token is restored if publishing fails, and removed from a store the account leaves.
		if exists && !previous.Tokenless() {
			if old, err := m.credentials.Load(instance, user, previous.CredentialStore); err == nil {
				undo = append(undo, func() error { return m.credentials.Save(instance, user, previous.CredentialStore, old) })
			}
			if credential == nil || previous.CredentialStore != account.CredentialStore {
				if err := m.credentials.Delete(instance, user, previous.CredentialStore); err != nil {
					return nil, err
				}
			}
		}
		if credential != nil {
			if err := m.credentials.Save(instance, user, account.CredentialStore, *credential); err != nil {
				return nil, errors.Join(err, rollback())
			}
			undo = append(undo, func() error { return m.credentials.Delete(instance, user, account.CredentialStore) })
		}
		if exists && credential != nil && !previous.Tokenless() {
			// Logging in again replaces the credential; settings made with gt config set stay.
			kept := previous
			kept.AuthMethod, kept.CredentialStore = account.AuthMethod, account.CredentialStore
			account = kept
		}
		host = options.Apply(host)
		host.Users[user] = account
		host.User = user
		s.Hosts.Hosts[instance] = host
		return rollback, nil
	})
}

// Logout removes one account, restores its credential if publication fails and returns the
// removed credential. An unreadable credential does not prevent removing the account.
func (m Manager) Logout(instance, user string) (Credential, error) {
	var removed Credential
	err := m.settings.Transaction(settings.TargetHosts, func(s *settings.Snapshot) (func() error, error) {
		host := s.Hosts.Hosts[instance]
		account, exists := host.Users[user]
		if !exists {
			return nil, errors.New("account is not configured")
		}
		var restore func() error
		if !account.Tokenless() {
			credential, err := m.credentials.Load(instance, user, account.CredentialStore)
			if err == nil {
				removed = credential
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
		if len(host.Users) == 0 && !host.HasSettings() {
			// An instance left without accounts or settings would still count when selecting one.
			delete(s.Hosts.Hosts, instance)
			return restore, nil
		}
		s.Hosts.Hosts[instance] = host
		return restore, nil
	})
	if err != nil {
		return Credential{}, err
	}
	return removed, nil
}
