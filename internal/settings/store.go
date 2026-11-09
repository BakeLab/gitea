// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package settings persists preferences and instance metadata, never credentials.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
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
	HTTPTimeout string            `yaml:"http_timeout,omitempty" json:"http_timeout"`
	Aliases     map[string]string `yaml:"aliases,omitempty" json:"aliases"`
	// Extensions is enabled or disabled; ExtensionHosts and ExtensionOwners, when set, are the only
	// instances and HOST/OWNER accounts extensions are installed and run from.
	Extensions      string   `yaml:"extensions,omitempty" json:"extensions"`
	ExtensionHosts  []string `yaml:"extension_hosts,omitempty" json:"extension_hosts"`
	ExtensionOwners []string `yaml:"extension_owners,omitempty" json:"extension_owners"`
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
	User          string `yaml:"user,omitempty" json:"user"`
	SSHHost       string `yaml:"ssh_host,omitempty" json:"ssh_host,omitempty"`
	Insecure      bool   `yaml:"insecure,omitempty" json:"insecure"`
	GitProtocol   string `yaml:"git_protocol,omitempty" json:"git_protocol,omitempty"`
	OAuthClientID string `yaml:"oauth_client_id,omitempty" json:"oauth_client_id,omitempty"`
	// Headers are sent with every request to the instance, such as those of an access proxy in
	// front of it. A value of $NAME or ${NAME} is read from the environment variable NAME when sent.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// ClientCert and ClientKey name the PEM files of a TLS client certificate for instances that
	// require one; without ClientKey, the key is read from ClientCert.
	ClientCert string             `yaml:"client_cert,omitempty" json:"client_cert,omitempty"`
	ClientKey  string             `yaml:"client_key,omitempty" json:"client_key,omitempty"`
	Users      map[string]Account `yaml:"users" json:"users"`
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

// DataDir returns dir, or else the platform data directory, where gt keeps installed extensions.
func DataDir(dir string) string {
	if dir == "" {
		dir = filepath.Join(xdg.DataHome, "gitea-cli")
	}
	return dir
}

// New selects an explicit directory or the platform configuration directory.
func New(dir string) *Store {
	if dir == "" {
		dir = filepath.Join(xdg.ConfigHome, "gitea-cli")
	}
	return &Store{Dir: dir}
}

// Defaults returns a fresh preferences value with built-in settings.
func Defaults() Preferences {
	return Preferences{Version: Version, GitProtocol: "https", Prompt: "enabled", Color: "auto", Hyperlinks: "auto", Markdown: "auto", HTTPTimeout: "2m", Aliases: map[string]string{}, Extensions: "enabled"}
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
	if p.HTTPTimeout == defaults.HTTPTimeout {
		p.HTTPTimeout = ""
	}
	if p.Extensions == defaults.Extensions {
		p.Extensions = ""
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
	data, err := ReadFile(configPath)
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
	hostData, err := ReadFile(hostsPath)
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
		if err := canonicalHeaders(snapshot.Hosts.Hosts); err != nil {
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
	if _, err := ParseTimeout(p.HTTPTimeout); err != nil {
		return err
	}
	if p.Extensions != "enabled" && p.Extensions != "disabled" {
		return errors.New("extensions must be enabled or disabled")
	}
	if err := validateExtensionSources(p.ExtensionHosts, p.ExtensionOwners); err != nil {
		return err
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
		if strings.ContainsAny(host.ClientCert+host.ClientKey, "\x00\r\n") {
			return fmt.Errorf("client certificate paths of %s cannot contain NUL or line breaks", instance)
		}
		for name, value := range host.Headers {
			if err := ValidateHeader(name, value); err != nil {
				return fmt.Errorf("invalid header for %s: %w", instance, err)
			}
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

// AtomicWrite publishes a complete private file after flushing its contents. A symbolic link is
// kept and its target replaced, or created when it does not exist yet.
func AtomicWrite(name string, data []byte) error {
	filename, err := linkTarget(name)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".gt-*")
	if errors.Is(err, os.ErrNotExist) && filename != name {
		return fmt.Errorf("cannot write %s: its target directory %s does not exist", name, filepath.Dir(filename))
	}
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
	return rename(temp.Name(), filename)
}

// linkTarget follows the symbolic links of filename to the file they name, which may not exist.
func linkTarget(filename string) (string, error) {
	for range 40 {
		info, err := os.Lstat(filename)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return filename, nil
		}
		link, err := os.Readlink(filename)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(filename), link)
		}
		filename = link
	}
	return "", fmt.Errorf("too many symbolic links at %s", filename)
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

// ParseTimeout parses the http_timeout preference: a duration such as 30s or 5m, or 0 for none.
func ParseTimeout(value string) (time.Duration, error) {
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout < 0 {
		return 0, errors.New("http_timeout must be a duration such as 30s or 5m, or 0 for none")
	}
	return timeout, nil
}

// HasSettings reports whether h stores instance settings besides its accounts.
func (h Host) HasSettings() bool {
	return h.SSHHost != "" || h.Insecure || h.GitProtocol != "" || h.OAuthClientID != "" || len(h.Headers) > 0 || h.ClientCert != "" || h.ClientKey != ""
}

// WithHeaders returns h with headers added, replacing those of the same name; an empty value
// removes the header. Names are in canonical form, as ParseHeader and Load leave them.
func (h Host) WithHeaders(headers map[string]string) Host {
	if len(headers) == 0 {
		return h
	}
	h.Headers = maps.Clone(h.Headers)
	if h.Headers == nil {
		h.Headers = map[string]string{}
	}
	for name, value := range headers {
		if value == "" {
			delete(h.Headers, name)
		} else {
			h.Headers[name] = value
		}
	}
	if len(h.Headers) == 0 {
		h.Headers = nil
	}
	return h
}

// canonicalHeaders writes the header names of every host in canonical form, so that a hand-edited
// hosts.yml cannot hold one header twice.
func canonicalHeaders(hosts map[string]Host) error {
	for instance, host := range hosts {
		if len(host.Headers) == 0 {
			continue
		}
		canonical := map[string]string{}
		for name, value := range host.Headers {
			key := http.CanonicalHeaderKey(name)
			if _, duplicate := canonical[key]; duplicate {
				return fmt.Errorf("hosts.yml sets the header %s of %s twice", key, instance)
			}
			canonical[key] = value
		}
		host.Headers = canonical
		hosts[instance] = host
	}
	return nil
}

// headerName is the token form of an HTTP header name.
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

// reservedHeaders are set by gt itself and cannot be configured.
var reservedHeaders = []string{"Authorization", "Sudo", "Signature", "X-Ssh-Certificate", "Date", "Digest", "Host", "Content-Length", "Content-Type", "Transfer-Encoding"}

// ValidateHeader checks a configured header: a valid name that gt does not set itself, and a value
// on one line.
func ValidateHeader(name, value string) error {
	switch {
	case !headerName.MatchString(name):
		return fmt.Errorf("%q is not a valid header name", name)
	case slices.ContainsFunc(reservedHeaders, func(reserved string) bool { return strings.EqualFold(reserved, name) }):
		return fmt.Errorf("%s is set by gt and cannot be configured", name)
	case strings.ContainsAny(value, "\x00\r\n"):
		return fmt.Errorf("the value of %s must be on one line", name)
	}
	return nil
}

// ParseHeader parses a "Name: value" header argument.
func ParseHeader(raw string) (name, value string, err error) {
	name, value, ok := strings.Cut(raw, ":")
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if !ok {
		return "", "", fmt.Errorf("header %q must have the form \"Name: value\"", raw)
	}
	return http.CanonicalHeaderKey(name), value, ValidateHeader(name, value)
}
