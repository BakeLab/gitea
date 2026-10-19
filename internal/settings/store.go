// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package settings persists preferences and instance metadata, never credentials.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitea.dev/cli/internal/filelock"
	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

// Version identifies the split configuration schema.
const Version = "1"

// LockTimeout bounds the wait for another process's settings update or token refresh.
const LockTimeout = 30 * time.Second

// Authentication methods and credential stores recorded for accounts.
const (
	AuthToken    = "token"
	AuthOAuth    = "oauth"
	AuthSSH      = "ssh"
	AuthHTTPSign = "httpsign"

	StoreKeyring = "keyring"
	StoreFile    = "file"
)

// Preferences contains global, non-secret command defaults.
type Preferences struct {
	Version     string            `yaml:"version" json:"version"`
	DefaultHost string            `yaml:"default_host,omitempty" json:"default_host"`
	GitProtocol string            `yaml:"git_protocol,omitempty" json:"git_protocol"`
	Editor      string            `yaml:"editor,omitempty" json:"editor"`
	Prompt      string            `yaml:"prompt,omitempty" json:"prompt"`
	Pager       string            `yaml:"pager,omitempty" json:"pager"`
	Browser     string            `yaml:"browser,omitempty" json:"browser"`
	Color       string            `yaml:"color,omitempty" json:"color"`
	Hyperlinks  string            `yaml:"hyperlinks,omitempty" json:"hyperlinks"`
	Markdown    string            `yaml:"markdown,omitempty" json:"markdown"`
	Remote      string            `yaml:"remote,omitempty" json:"remote"`
	Aliases     map[string]string `yaml:"aliases,omitempty" json:"aliases"`
}

// Account contains account-specific authentication metadata, never token values.
type Account struct {
	AuthMethod        string `yaml:"auth_method" json:"auth_method"`
	CredentialStore   string `yaml:"credential_store,omitempty" json:"credential_store,omitempty"`
	SSHKey            string `yaml:"ssh_key,omitempty" json:"ssh_key,omitempty"`
	SSHAgent          bool   `yaml:"ssh_agent,omitempty" json:"ssh_agent,omitempty"`
	SSHCertPrincipal  string `yaml:"ssh_certificate_principal,omitempty" json:"ssh_certificate_principal,omitempty"`
	SSHKeyFingerprint string `yaml:"ssh_key_fingerprint,omitempty" json:"ssh_key_fingerprint,omitempty"`
}

// Tokenless reports whether the account signs requests instead of storing a token.
func (a Account) Tokenless() bool {
	return a.AuthMethod == AuthSSH || a.AuthMethod == AuthHTTPSign
}

// Host contains shared connection settings and the active account.
type Host struct {
	User          string             `yaml:"user,omitempty" json:"user"`
	SSHHost       string             `yaml:"ssh_host,omitempty" json:"ssh_host,omitempty"`
	Insecure      bool               `yaml:"insecure,omitempty" json:"insecure"`
	GitProtocol   string             `yaml:"git_protocol,omitempty" json:"git_protocol,omitempty"`
	OAuthClientID string             `yaml:"oauth_client_id,omitempty" json:"oauth_client_id,omitempty"`
	Users         map[string]Account `yaml:"users" json:"users"`
}

// Hosts stores instances keyed by their normalized installation URLs.
type Hosts struct {
	Version string          `yaml:"version" json:"version"`
	Hosts   map[string]Host `yaml:"hosts" json:"hosts"`
}

// Snapshot is one independently loaded configuration generation.
type Snapshot struct {
	Preferences Preferences
	Hosts       Hosts
	// Configured distinguishes explicitly stored settings from built-in defaults.
	Configured map[string]bool
}

// Store coordinates configuration reads and locked atomic updates.
type Store struct {
	Dir string
	mu  sync.Mutex
	// generation counts the updates made through the store.
	generation atomic.Uint64
}

// Generation changes whenever an update through the store ran, so callers can tell when a
// snapshot they loaded earlier may be stale.
func (s *Store) Generation() uint64 { return s.generation.Load() }

// New selects an explicit directory or the platform configuration directory.
func New(dir string) *Store {
	if dir == "" {
		dir = filepath.Join(xdg.ConfigHome, "gitea-cli")
	}
	return &Store{Dir: dir}
}

// Defaults returns a fresh preferences value with built-in settings.
func Defaults() Preferences {
	return Preferences{Version: Version, GitProtocol: "https", Prompt: "enabled", Color: "auto", Hyperlinks: "auto", Markdown: "auto", Aliases: map[string]string{}}
}

// stored omits built-in defaults so that only explicit settings are persisted.
func (p Preferences) stored() Preferences {
	defaults := Defaults()
	if p.GitProtocol == defaults.GitProtocol {
		p.GitProtocol = ""
	}
	if p.Prompt == defaults.Prompt {
		p.Prompt = ""
	}
	if p.Color == defaults.Color {
		p.Color = ""
	}
	if p.Hyperlinks == defaults.Hyperlinks {
		p.Hyperlinks = ""
	}
	if p.Markdown == defaults.Markdown {
		p.Markdown = ""
	}
	return p
}

// NormalizeHost retains ports and installation paths as part of instance identity.
func NormalizeHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("invalid instance URL %q: use http(s)://host[:port][/base-path]", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	// Encoded separators cannot identify an installation unambiguously.
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || strings.Contains(strings.ToLower(u.EscapedPath()), "%5c") || strings.Contains(u.Path, "\\") {
		return "", fmt.Errorf("invalid instance path in %q", raw)
	}
	u.Path = strings.TrimSuffix(path.Clean("/"+u.Path), "/")
	u.RawPath = ""
	return u.String(), nil
}

func decode(file string, data []byte, target any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s must contain exactly one YAML document", file)
	}
	return nil
}

// Load reads both configuration files without creating paths or mutating globals.
func (s *Store) Load() (Snapshot, error) {
	snapshot := Snapshot{Preferences: Defaults(), Hosts: Hosts{Version: Version, Hosts: map[string]Host{}}, Configured: map[string]bool{}}
	configPath := filepath.Join(s.Dir, "config.yml")
	data, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return snapshot, err
	}
	if err == nil {
		keys, err := versionedKeys(configPath, data)
		if err != nil {
			return snapshot, err
		}
		for key := range keys {
			snapshot.Configured[key] = true
		}
		if err := decode(configPath, data, &snapshot.Preferences); err != nil {
			return snapshot, err
		}
	}
	hostsPath := filepath.Join(s.Dir, "hosts.yml")
	hostData, err := os.ReadFile(hostsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return snapshot, fmt.Errorf("read %s: %w", hostsPath, err)
	}
	if err == nil {
		if _, err := versionedKeys(hostsPath, hostData); err != nil {
			return snapshot, err
		}
		if err := decode(hostsPath, hostData, &snapshot.Hosts); err != nil {
			return snapshot, err
		}
	}
	if snapshot.Hosts.Hosts == nil {
		snapshot.Hosts.Hosts = map[string]Host{}
	}
	if snapshot.Preferences.Aliases == nil {
		snapshot.Preferences.Aliases = map[string]string{}
	}
	return snapshot, snapshot.Validate()
}

// Validate rejects unsupported versions and inconsistent instance/account settings.
func (s Snapshot) Validate() error {
	p := s.Preferences
	if p.Version != Version || s.Hosts.Version != Version {
		return errors.New("unsupported settings version")
	}
	if p.GitProtocol != "https" && p.GitProtocol != "ssh" {
		return errors.New("git_protocol must be https or ssh")
	}
	if p.Prompt != "enabled" && p.Prompt != "disabled" {
		return errors.New("prompt must be enabled or disabled")
	}
	if p.Color != "auto" && p.Color != "always" && p.Color != "never" {
		return errors.New("color must be auto, always, or never")
	}
	if p.Hyperlinks != "auto" && p.Hyperlinks != "always" && p.Hyperlinks != "never" {
		return errors.New("hyperlinks must be auto, always, or never")
	}
	if p.Markdown != "auto" && p.Markdown != "raw" {
		return errors.New("markdown must be auto or raw")
	}
	if p.DefaultHost != "" {
		normalized, err := NormalizeHost(p.DefaultHost)
		if err != nil || normalized != p.DefaultHost {
			return errors.New("default_host must be a normalized instance URL")
		}
	}
	for instance, host := range s.Hosts.Hosts {
		normalized, err := NormalizeHost(instance)
		if err != nil || normalized != instance {
			return fmt.Errorf("instance %q must be a normalized URL", instance)
		}
		if host.SSHHost != "" {
			if _, err := SSHEndpoint(host.SSHHost); err != nil {
				return fmt.Errorf("invalid ssh_host for %s: %w", instance, err)
			}
		}
		if host.GitProtocol != "" && host.GitProtocol != "https" && host.GitProtocol != "ssh" {
			return fmt.Errorf("invalid git_protocol for %s", instance)
		}
		if host.User != "" {
			if _, exists := host.Users[host.User]; !exists {
				return fmt.Errorf("active user %q is missing on %s", host.User, instance)
			}
		}
		for name, account := range host.Users {
			if strings.ContainsAny(account.SSHKey+account.SSHCertPrincipal+account.SSHKeyFingerprint, "\x00\r\n") {
				return errors.New("account SSH settings cannot contain NUL or line breaks")
			}
			if strings.TrimSpace(name) == "" {
				return errors.New("account name cannot be empty")
			}
			if account.AuthMethod != AuthToken && account.AuthMethod != AuthOAuth && !account.Tokenless() {
				return fmt.Errorf("invalid authentication method for %s on %s", name, instance)
			}
			if account.Tokenless() {
				if account.CredentialStore != "" {
					return fmt.Errorf("SSH and HTTP signature accounts cannot use a credential store for %s on %s", name, instance)
				}
			} else if account.CredentialStore != StoreKeyring && account.CredentialStore != StoreFile {
				return fmt.Errorf("invalid credential store for %s on %s", name, instance)
			}
		}
	}
	return nil
}

// Target selects which settings file an update publishes.
type Target int

const (
	// TargetPreferences publishes config.yml, the global non-secret defaults.
	TargetPreferences Target = iota
	// TargetHosts publishes hosts.yml, the instance and account metadata.
	TargetHosts
)

// Update serializes writers, reloads current settings, and changes exactly the target file.
// Cross-file transactions are not exposed.
func (s *Store) Update(target Target, update func(*Snapshot) error) error {
	return s.Transaction(target, func(snapshot *Snapshot) (func() error, error) {
		return nil, update(snapshot)
	})
}

// Transaction runs a compensating action under the lock if publishing the target file fails.
// It is used to keep credential storage and account metadata consistent.
func (s *Store) Transaction(target Target, update func(*Snapshot) (func() error, error)) error {
	defer s.generation.Add(1)
	return s.Locked(func(snapshot Snapshot) error {
		unpublished := func() ([]byte, error) {
			if target == TargetHosts {
				return yaml.Marshal(snapshot.Preferences)
			}
			return yaml.Marshal(snapshot.Hosts)
		}
		before, err := unpublished()
		if err != nil {
			return err
		}
		rollback, err := update(&snapshot)
		undo := func(cause error) error {
			if cause != nil && rollback != nil {
				return errors.Join(cause, rollback())
			}
			return cause
		}
		if err != nil {
			return undo(err)
		}
		if after, err := unpublished(); err != nil || !bytes.Equal(before, after) {
			return undo(errors.Join(errors.New("settings update changed a file it does not write"), err))
		}
		if err := snapshot.Validate(); err != nil {
			return undo(err)
		}
		filename := "config.yml"
		var value any = snapshot.Preferences.stored()
		if target == TargetHosts {
			filename = "hosts.yml"
			value = snapshot.Hosts
		}
		data, err := yaml.Marshal(value)
		if err != nil {
			return undo(err)
		}
		return undo(AtomicWrite(filepath.Join(s.Dir, filename), data))
	})
}

// Locked coordinates account changes and token refresh without rewriting settings.
func (s *Store) Locked(fn func(Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Load(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return filelock.New(filepath.Join(s.Dir, "settings.lock"), LockTimeout).WithLock(func() error {
		snapshot, err := s.Load()
		if err != nil {
			return err
		}
		return fn(snapshot)
	})
}

// AtomicWrite publishes a complete private file after flushing its contents.
func AtomicWrite(filename string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(filename), ".gt-*")
	if err != nil {
		return err
	}
	defer func() { _ = temp.Close(); _ = os.Remove(temp.Name()) }()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), filename)
}

// versionedKeys returns the top-level keys of the settings file at path, checking its version
// first so that a file of another version is reported as such rather than by its unknown fields.
func versionedKeys(path string, data []byte) (map[string]yaml.Node, error) {
	var keys map[string]yaml.Node
	if err := yaml.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if version, ok := keys["version"]; !ok || version.Value != Version {
		return nil, fmt.Errorf("unsupported configuration version in %s", path)
	}
	return keys, nil
}
