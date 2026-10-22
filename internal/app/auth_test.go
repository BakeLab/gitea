// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type statusKeyring struct{ reads []string }

func (s *statusKeyring) Get(_, identity string) (string, error) {
	s.reads = append(s.reads, identity)
	return `{"access_token":"private-status-token"}`, nil
}

func (*statusKeyring) Set(string, string, string) error { return nil }
func (*statusKeyring) Delete(string, string) error      { return nil }

type channelBrowser struct{ opened chan string }

func (b *channelBrowser) Open(_ context.Context, resource, _ string) error {
	select {
	case b.opened <- resource:
	default:
	}
	return nil
}

func TestLoginValidatesUserEnvironmentWithFlagPrecedence(t *testing.T) {
	server := newAuthenticatedServer(http.NotFoundHandler())
	defer server.Close()
	for _, test := range []struct {
		name, environment, flag string
		accepted                bool
	}{
		{"environment matches", "ALICE", "", true},
		{"environment mismatches", "bob", "", false},
		{"blank environment is unset", " ", "", true},
		{"flag overrides environment", "bob", "alice", true},
		{"flag mismatches", "alice", "bob", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, _, _ := testDeps(t)
			deps.Streams.In = strings.NewReader("private-login-token\n")
			deps.Getenv = envFunc(map[string]string{"GITEA_CLI_USER": test.environment})
			args := []string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file", "--no-input"}
			if test.flag != "" {
				args = append(args, "--user", test.flag)
			}
			err := Execute(context.Background(), deps, args)
			if test.accepted {
				require.NoError(t, err)
			} else {
				require.Equal(t, clierr.Usage, clierr.ExitCode(err))
				require.NotContains(t, err.Error(), "private-login-token")
			}
			snapshot, err := deps.Settings.Load()
			require.NoError(t, err)
			if test.accepted {
				require.Equal(t, "alice", snapshot.Hosts.Hosts[server.URL].User)
				require.Equal(t, server.URL, snapshot.Preferences.DefaultHost, "the first instance becomes the default")
			} else {
				require.Empty(t, snapshot.Hosts.Hosts)
				require.Empty(t, snapshot.Preferences.DefaultHost)
			}
		})
	}
}

func TestLoginWithTokenPromptsWithoutEchoOnATerminal(t *testing.T) {
	server := newAuthenticatedServer(http.NotFoundHandler())
	defer server.Close()
	args := []string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file"}
	deps, _, _ := testDeps(t)
	deps.Streams.In = strings.NewReader("echoed-token\n")
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{secret: " private-login-token "}
	deps.Prompter = prompt
	err := Execute(context.Background(), deps, append(args, "--no-input"))
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "gt auth login --with-token < token.txt")
	require.Empty(t, prompt.secrets)
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, []string{"Paste an access token"}, prompt.secrets)
	credential, err := deps.Credentials.Load(server.URL, "alice", settings.StoreFile)
	require.NoError(t, err)
	require.Equal(t, "private-login-token", credential.AccessToken)
}

func TestLoginInsecureInstance(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	deps, _, _ := testDeps(t)
	login := func(flags ...string) error {
		deps.Streams.In = strings.NewReader("token\n")
		return Execute(context.Background(), deps, append([]string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file", "--no-input"}, flags...))
	}
	require.Error(t, login())
	require.Equal(t, clierr.Usage, clierr.ExitCode(login("--client-id", "custom")))
	require.NoError(t, login("--insecure"))
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	host := snapshot.Hosts.Hosts[server.URL]
	require.True(t, host.Insecure)
	require.Empty(t, host.OAuthClientID)
	configureHost(t, deps, server.URL, settings.Host{Insecure: true, OAuthClientID: "custom"})
	require.NoError(t, login())
	snapshot, err = deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "custom", snapshot.Hosts.Hosts[server.URL].OAuthClientID)
}

func TestAuthSwitchInvalidTargetsAreUsageErrors(t *testing.T) {
	for _, host := range []string{"invalid-host", "https://unconfigured.example.org"} {
		deps, out, _ := testDeps(t)
		require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"auth", "switch", "alice", "--host", host})))
		require.Empty(t, out.String())
	}
	deps, out, _ := testDeps(t)
	configureHost(t, deps, "https://example.org", settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice")})
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"auth", "switch", "bob", "--host", "https://example.org"})))
	require.Empty(t, out.String())
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "alice", snapshot.Hosts.Hosts["https://example.org"].User)
	configureHost(t, deps, "https://example.org", settings.Host{Users: tokenAccounts(settings.StoreFile, "Alice")})
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "switch", "alice", "--host", "https://example.org"}))
	snapshot, err = deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "Alice", snapshot.Hosts.Hosts["https://example.org"].User)
}

func TestLogoutRejectsUnknownAccountBeforeConfirmation(t *testing.T) {
	deps, out, diagnostics := testDeps(t)
	keyring := &statusKeyring{}
	deps.Credentials.Keyring = keyring
	configureHost(t, deps, "https://example.org", settings.Host{User: "alice", Users: tokenAccounts(settings.StoreKeyring, "alice")})
	err := Execute(context.Background(), deps, []string{"auth", "logout", "--host", "https://example.org", "--user", "bob", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, `account "bob" is not logged in on https://example.org (accounts: alice)`)
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
	require.Empty(t, keyring.reads)
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "alice", snapshot.Hosts.Hosts["https://example.org"].User)
}

func TestAuthStatusFiltersBeforeReadingCredentials(t *testing.T) {
	deps, out, _ := testDeps(t)
	keyring := &statusKeyring{}
	deps.Credentials.Keyring = keyring
	for _, host := range []string{"https://example.org/base", "https://other.example.org"} {
		configureHost(t, deps, host, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreKeyring, "alice", "bob")})
	}
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--host", "https://EXAMPLE.org/base/", "--user", "BOB", "--json"}))
	require.Equal(t, []string{auth.Identity("https://example.org/base", "bob")}, keyring.reads)
	require.JSONEq(t, `[{"host":"https://example.org/base","user":"bob","active":false,"method":"token","credential_store":"keyring","credential_available":true,"expires_at":null,"verified":null,"scopes":null,"error":"","git_credential_helper":""}]`, out.String())
	out.Reset()
	keyring.reads = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--host", "https://unconfigured.example.org", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	require.Empty(t, keyring.reads)
	out.Reset()
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"auth", "status", "--host", "ftp://invalid-host"})))
	err := Execute(context.Background(), deps, []string{"auth", "status", "--host", "unconfigured.example.org"})
	require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
	require.EqualError(t, err, "not logged in to https://unconfigured.example.org; run gt auth login --host https://unconfigured.example.org")
	require.Empty(t, out.String())
	require.Empty(t, keyring.reads)
}

func TestLogoutSelectsAccountWithoutAuthentication(t *testing.T) {
	for _, test := range []struct {
		name        string
		flags       []string
		environment map[string]string
		selected    string
	}{
		{name: "active account", selected: "alice"},
		{name: "environment account", environment: map[string]string{"GITEA_CLI_HOST": "https://example.org", "GITEA_CLI_USER": "bob", "GITEA_TOKEN": "expired"}, selected: "bob"},
		{name: "flag overrides environment", flags: []string{"--host", "https://example.org", "--user", "alice"}, environment: map[string]string{"GITEA_CLI_HOST": "https://other.example.org", "GITEA_CLI_USER": "bob"}, selected: "alice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			keyring := &statusKeyring{}
			deps.Credentials.Keyring = keyring
			deps.Getenv = envFunc(test.environment)
			accounts := tokenAccounts(settings.StoreKeyring, "bob", "charlie")
			accounts["alice"] = settings.Account{AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreKeyring}
			configureHost(t, deps, "https://example.org", settings.Host{User: "alice", Users: accounts})
			args := append([]string{"auth", "logout", "--yes", "--no-input"}, test.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Empty(t, out.String())
			require.Equal(t, []string{auth.Identity("https://example.org", test.selected)}, keyring.reads)
			snapshot, err := deps.Settings.Load()
			require.NoError(t, err)
			host := snapshot.Hosts.Hosts["https://example.org"]
			require.NotContains(t, host.Users, test.selected)
			require.Len(t, host.Users, 2)
			if test.selected == "alice" {
				require.Empty(t, host.User)
			} else {
				require.Equal(t, "alice", host.User)
			}
		})
	}
}

func TestAuthLoginSSHKeyPersistsAndSignsSubsequentRequests(t *testing.T) {
	keyPath := writeAppTempSSHKey(t, "")
	var requests []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Header.Clone())
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/v1/user"):
			require.NotEmpty(t, r.Header.Get("Signature"))
			require.Empty(t, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/v1/repos/alice/project/issues"):
			require.NotEmpty(t, r.Header.Get("Signature"))
			require.Empty(t, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	deps, _, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{
		"auth", "login", "--host", server.URL, "--ssh-key", keyPath, "--no-input", "--yes",
	}))
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	account := snapshot.Hosts.Hosts[server.URL].Users["alice"]
	require.Equal(t, settings.Account{AuthMethod: "httpsign", SSHKey: keyPath}, account)
	require.Empty(t, account.CredentialStore)

	require.NoError(t, Execute(context.Background(), deps, []string{
		"issue", "list", "-R", "alice/project", "--host", server.URL, "--json",
	}))
	require.GreaterOrEqual(t, len(requests), 2)
	for _, header := range requests {
		require.NotEmpty(t, header.Get("Signature"))
		require.Empty(t, header.Get("Authorization"))
	}
}

func TestAuthLoginSSHKeyCertificateUsesSSHAuthMethod(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	_, userKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	userSigner, err := ssh.NewSignerFromKey(userKey)
	require.NoError(t, err)
	caSigner, err := ssh.NewSignerFromKey(caKey)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(userKey, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600))
	cert := &ssh.Certificate{
		Key:             userSigner.PublicKey(),
		CertType:        ssh.UserCert,
		ValidPrincipals: []string{"alice"},
		ValidAfter:      0,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	require.NoError(t, cert.SignCert(rand.Reader, caSigner))
	require.NoError(t, os.WriteFile(keyPath+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0o600))

	var method settings.Account
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.Header.Get("Signature"))
		require.NotEmpty(t, r.Header.Get("x-ssh-certificate"))
		require.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()

	deps, _, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{
		"auth", "login", "--host", server.URL, "--ssh-key", keyPath, "--no-input", "--yes",
	}))
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	method = snapshot.Hosts.Hosts[server.URL].Users["alice"]
	require.Equal(t, "ssh", method.AuthMethod)
	require.Empty(t, method.SSHCertPrincipal)
	require.Equal(t, keyPath, method.SSHKey)
}

func TestAuthLoginEncryptedSSHKeyPromptsForPassphrase(t *testing.T) {
	keyPath := writeAppTempSSHKey(t, "correct horse")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.Header.Get("Signature"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()

	deps, _, _ := testDeps(t)
	err := Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL, "--ssh-key", keyPath, "--no-input"})
	require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
	require.ErrorContains(t, err, "ssh-agent")

	prompter := &scriptedPrompter{secret: "correct horse", accept: true}
	deps.Prompter = prompter
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL, "--ssh-key", keyPath}))
	require.Equal(t, []string{"Passphrase for " + keyPath}, prompter.secrets)
}

func TestAuthLoginMethodFlagsAreMutuallyExclusive(t *testing.T) {
	keyPath := writeAppTempSSHKey(t, "")
	deps, out, _ := testDeps(t)
	deps.Streams.In = strings.NewReader("token\n")
	for _, args := range [][]string{
		{"auth", "login", "--host", "https://example.org", "--with-token", "--oauth", "--no-input"},
		{"auth", "login", "--host", "https://example.org", "--with-token", "--ssh-key", keyPath, "--no-input"},
		{"auth", "login", "--host", "https://example.org", "--oauth", "--ssh-agent-key", "SHA256:abc", "--no-input"},
		{"auth", "login", "--host", "https://example.org", "--ssh-key", keyPath, "--ssh-agent-principal", "git", "--no-input"},
		{"auth", "login", "--host", "https://example.org", "--ssh-agent-key", "SHA256:abc", "--ssh-agent-principal", "git", "--no-input"},
		{"auth", "login", "--host", "https://example.org", "--no-input"},
	} {
		t.Run(strings.Join(args[3:], " "), func(t *testing.T) {
			err := Execute(context.Background(), deps, args)
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			if strings.Count(strings.Join(args, " "), "--") > 2 {
				require.ErrorContains(t, err, "select one of --with-token, --oauth, --ssh-key")
			} else {
				require.ErrorContains(t, err, "select how to log in: --with-token, --oauth or --ssh-key")
			}
			require.Empty(t, out.String())
		})
	}
}

func TestAuthLoginValidatesMethodBeforePromptingForHost(t *testing.T) {
	for args, want := range map[string]string{
		"auth login --with-token --oauth":       "select one of",
		"auth login --with-token --client-id x": "--client-id cannot be combined with --with-token",
		"auth login --with-token --scopes all":  "--scopes cannot be combined with --with-token",
		"auth login --ssh-key k --scopes all":   "--scopes cannot be combined with --ssh-key",
		"auth login --oauth --scopes nope":      "--scopes must be all, read:activitypub",
	} {
		t.Run(args, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			prompt := &scriptedPrompter{input: "https://example.org"}
			deps.Prompter = prompt
			deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			err := Execute(context.Background(), deps, strings.Fields(args))
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			require.ErrorContains(t, err, want)
			require.Empty(t, prompt.asked)
			require.Empty(t, out.String())
		})
	}
}

func TestAuthLoginOAuthFlagsChooseOAuth(t *testing.T) {
	for _, flag := range []string{"--client-id=custom", "--scopes=read:user"} {
		t.Run(flag, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			prompt := &loginPrompter{}
			deps.Prompter = prompt
			deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
			err := Execute(context.Background(), deps, []string{"auth", "login", "--host", "https://example.org", flag, "--no-input"})
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			require.ErrorContains(t, err, "OAuth authorization requires interaction")
			require.Zero(t, prompt.questions)
			require.Empty(t, out.String())
		})
	}
}

// writeAppTempSSHKey writes a new ed25519 private key, encrypted when passphrase is set.
func writeAppTempSSHKey(t *testing.T, passphrase string) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(privateKey, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(privateKey, "", []byte(passphrase))
	}
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	return path
}

func TestAuthRefreshExpiredCredentialOnlyOnce(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/login/oauth/access_token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "original-refresh", r.Form.Get("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer server.Close()
	deps, out, diagnostics := testDeps(t)
	now := time.Now()
	deps.Now = func() time.Time { return now }
	manager := testManager(t, deps)
	require.NoError(t, manager.Register(server.URL, "alice", "file", "oauth", auth.HostOptions{OAuthClientID: "client"}, auth.Credential{AccessToken: "expired", RefreshToken: "original-refresh", ExpiresAt: now.Add(-time.Hour)}))
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "refresh", "--host", server.URL, "--user", "alice", "--no-input"}))
	require.Equal(t, 1, requests)
	credential, err := deps.Credentials.Load(server.URL, "alice", "file")
	require.NoError(t, err)
	require.Equal(t, "rotated-refresh", credential.RefreshToken)
	require.Equal(t, "new-access", credential.AccessToken)
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}

func TestAuthRefreshRejectsTokenAccounts(t *testing.T) {
	deps, out, _ := testDeps(t)
	manager := testManager(t, deps)
	require.NoError(t, manager.Register("https://example.org", "alice", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "token"}))
	err := Execute(context.Background(), deps, []string{"auth", "refresh", "--host", "https://example.org", "--user", "alice", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "only OAuth accounts")
	require.Empty(t, out.String())
}

func TestAuthStatusVerifyChecksStoredCredentials(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			requests++
		}
		switch {
		case r.Header.Get("Authorization") != "token valid-status-token":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/api/v1/token":
			_, _ = w.Write([]byte(`{"scopes":["write:repository","read:user"]}`))
		default:
			require.Equal(t, "/api/v1/user", r.URL.Path)
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	expires := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, deps.Credentials.Save(server.URL, "alice", settings.StoreFile, auth.Credential{AccessToken: "valid-status-token", RefreshToken: "refresh", ExpiresAt: expires}))
	require.NoError(t, deps.Credentials.Save(server.URL, "bob", settings.StoreFile, auth.Credential{AccessToken: "revoked-status-token"}))
	accounts := tokenAccounts(settings.StoreFile, "bob", "carol")
	accounts["alice"] = settings.Account{AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreFile}
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: accounts})
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--json=user,verified,error"}))
	require.Zero(t, requests)
	require.JSONEq(t, `[{"user":"alice","verified":null,"error":""},{"user":"bob","verified":null,"error":""},{"user":"carol","verified":null,"error":"credential unavailable; run gt auth login for this instance and account"}]`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--verify", "--json"}))
	require.Equal(t, 2, requests)
	var rows []struct {
		User      string     `json:"user"`
		ExpiresAt *time.Time `json:"expires_at"`
		Verified  *bool      `json:"verified"`
		Error     string     `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Len(t, rows, 3)
	require.Equal(t, "alice", rows[0].User)
	require.True(t, rows[0].ExpiresAt.Equal(expires))
	require.True(t, *rows[0].Verified)
	require.Empty(t, rows[0].Error)
	for _, row := range rows[1:] {
		require.False(t, *row.Verified, row.User)
		require.Nil(t, row.ExpiresAt)
		require.NotEmpty(t, row.Error)
	}
	require.Equal(t, "credential unavailable", rows[2].Error)
	require.NotContains(t, out.String(), "status-token")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--verify", "--user", "alice"}))
	require.Equal(t, server.URL+"\talice\ttrue\toauth\tfile\ttrue\ttrue\t2030-01-02T03:04:05Z\t\n", out.String())
}

func TestAuthStatusVerifyRefreshesExpiredOAuthToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "refresh_token": "refresh2", "expires_in": 3600, "token_type": "Bearer"})
		case "/api/v1/token":
			_, _ = w.Write([]byte(`{"scopes":["all"]}`))
		default:
			if r.Header.Get("Authorization") != "token fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	require.NoError(t, deps.Credentials.Save(server.URL, "alice", settings.StoreFile, auth.Credential{AccessToken: "stale", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour)}))
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: map[string]settings.Account{"alice": {AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreFile}}})
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--verify", "--json=user,verified,error"}))
	require.JSONEq(t, `[{"user":"alice","verified":true,"error":""}]`, out.String())
}

func TestOAuthLoginOpensAuthorizationURLInBrowser(t *testing.T) {
	opened := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			require.NoError(t, r.ParseForm())
			require.Equal(t, "authorization_code", r.Form.Get("grant_type"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 3600, "token_type": "Bearer"})
		case "/api/v1/user":
			require.Equal(t, "token access", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("stdout terminal %t", tty), func(t *testing.T) {
			browser := &channelBrowser{opened: opened}
			deps, _, diagnostics := testDeps(t)
			deps.Browser = browser
			deps.Credentials = &auth.Store{Dir: deps.Settings.Dir, Keyring: &statusKeyring{}}
			deps.Streams.InTTY, deps.Streams.ErrTTY, deps.Streams.OutTTY = true, true, tty
			deps.Client = func(instance, token string, insecure bool) *transport.Client {
				client := transport.New(instance, token, insecure)
				client.HTTP = server.Client()
				return client
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- Execute(ctx, deps, []string{"auth", "login", "--host", server.URL, "--oauth", "--client-id", "test-client", "--credential-store", "file", "--yes"})
			}()
			var resource string
			select {
			case resource = <-opened:
			case <-time.After(3 * time.Second):
				t.Fatal("browser was not opened")
			}
			u, err := url.Parse(resource)
			require.NoError(t, err)
			require.Equal(t, "/login/oauth/authorize", u.Path)
			require.Equal(t, "test-client", u.Query().Get("client_id"))
			redirect := u.Query().Get("redirect_uri")
			state := u.Query().Get("state")
			require.NotEmpty(t, redirect)
			require.NotEmpty(t, state)
			callback, err := http.Get(redirect + "?code=valid-code&state=" + url.QueryEscape(state))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, callback.StatusCode)
			_ = callback.Body.Close()
			require.NoError(t, <-done)
			if tty {
				require.Contains(t, diagnostics.String(), "Opening "+resource+" in your browser.", "the whole URL, which people can copy")
			} else {
				require.Contains(t, diagnostics.String(), "Open this URL to authorize gt: "+resource)
			}
			require.Equal(t, 1, strings.Count(diagnostics.String(), resource), "the URL is printed once")
			snapshot, err := deps.Settings.Load()
			require.NoError(t, err)
			normalized, err := settings.NormalizeHost(server.URL)
			require.NoError(t, err)
			require.Contains(t, snapshot.Hosts.Hosts[normalized].Users, "alice")
		})
	}
}

// loginPrompter answers the interactive login and account choices, recording what it was asked.
type loginPrompter struct {
	scriptedPrompter
	answers   terminal.LoginAnswers
	questions terminal.LoginQuestions
	prefilled terminal.LoginAnswers
	choice    int
	options   []string
}

func (p *loginPrompter) Login(_ context.Context, questions terminal.LoginQuestions, answers *terminal.LoginAnswers) error {
	p.questions, p.prefilled = questions, *answers
	host := answers.Host
	*answers = p.answers
	answers.Host = cmp.Or(answers.Host, host)
	return nil
}

func (p *loginPrompter) Choose(_ context.Context, _ string, choices []terminal.Choice, _ string) (string, error) {
	p.options = make([]string, len(choices))
	for i, choice := range choices {
		p.options[i] = choice.Label
	}
	return choices[p.choice].Value, nil
}

// terminalDeps returns testDeps on a terminal, where gt prompts and reports mutations.
func terminalDeps(t *testing.T) (Dependencies, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	deps, out, diagnostics := testDeps(t)
	deps.Streams.InTTY, deps.Streams.OutTTY, deps.Streams.ErrTTY = true, true, true
	return deps, out, diagnostics
}

func TestAuthLoginAsksHowToLogIn(t *testing.T) {
	server := newAuthenticatedServer(http.NotFoundHandler())
	defer server.Close()
	deps, _, diagnostics := terminalDeps(t)
	prompt := &loginPrompter{answers: terminal.LoginAnswers{Method: terminal.LoginToken, Token: "pasted-token", Store: settings.StoreFile}}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL}))
	require.Equal(t, terminal.LoginAnswers{Host: server.URL, Method: terminal.LoginToken, Store: settings.StoreKeyring, SSHKey: prompt.prefilled.SSHKey, SetupGit: true}, prompt.prefilled)
	require.False(t, prompt.questions.Host)
	require.True(t, prompt.questions.Method && prompt.questions.Store && prompt.questions.SetupGit)
	require.Equal(t, "Create one at https://gitea.com/user/settings/applications", prompt.questions.TokenHint("https://gitea.com"))
	credential, err := deps.Credentials.Load(server.URL, "alice", settings.StoreFile)
	require.NoError(t, err)
	require.Equal(t, "pasted-token", credential.AccessToken)
	require.Equal(t, "✓ Logged in to "+server.URL+" as alice (token, file store)\n✓ Set default_host to "+server.URL+"\n", withoutANSI(diagnostics.String()))
	// Without --host, the sole instance is the suggestion; logging in again replaces the token.
	diagnostics.Reset()
	prompt.answers.Token, prompt.accept = "rotated-token", true
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "login"}))
	require.True(t, prompt.questions.Host)
	require.Equal(t, server.URL, prompt.prefilled.Host)
	require.Equal(t, []string{"alice is already logged in to " + server.URL + ". Replace the stored credential?"}, prompt.confirmations)
	credential, err = deps.Credentials.Load(server.URL, "alice", settings.StoreFile)
	require.NoError(t, err)
	require.Equal(t, "rotated-token", credential.AccessToken)
}

func TestAuthLoginReplacesACredentialOnlyWhenConfirmed(t *testing.T) {
	server := newAuthenticatedServer(http.NotFoundHandler())
	defer server.Close()
	deps, _, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	login := func(extra ...string) error {
		deps.Streams.In = strings.NewReader("new-token\n")
		return Execute(context.Background(), deps, append([]string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file", "--no-input"}, extra...))
	}
	err := login()
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "confirmation required to replace the stored credential of alice on "+server.URL+"; rerun with --yes")
	credential, err := deps.Credentials.Load(server.URL, "alice", settings.StoreFile)
	require.NoError(t, err)
	require.Equal(t, "alice-token", credential.AccessToken)
	require.NoError(t, login("--yes"))
	credential, err = deps.Credentials.Load(server.URL, "alice", settings.StoreFile)
	require.NoError(t, err)
	require.Equal(t, "new-token", credential.AccessToken)
	// A named account is confirmed before gt asks for its token.
	deps, _, _ = terminalDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	prompt := &scriptedPrompter{secret: "unused"}
	deps.Prompter = prompt
	err = Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL, "--with-token", "--user", "alice"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	require.Len(t, prompt.confirmations, 1)
	require.Empty(t, prompt.secrets)
}

func TestAuthStatusShowsAccountsByInstance(t *testing.T) {
	const instance = "https://example.org"
	helper := "config --global --get-all credential." + instance + ".helper"
	for _, tc := range []struct {
		name     string
		users    []string
		env      map[string]string
		helper   bool
		want     string
		failure  string
		exitCode int
	}{
		{
			name: "accounts", users: []string{"alice", "bob"}, helper: true,
			want: instance + "\n  ✓ Logged in as alice (token, file store) · active\n  ✓ Logged in as bob (token, file store)\n  - Git credential helper: configured (global)\n",
		},
		{
			name: "environment token", users: []string{"alice"}, env: map[string]string{"GITEA_TOKEN": "env", "GITEA_CLI_HOST": "example.org"},
			want: instance + "\n  ✓ Using GITEA_TOKEN from the environment\n  ✓ Logged in as alice (token, file store)\n  - Git credential helper: not configured; run gt auth setup-git\n",
		},
		{
			name: "environment token only", env: map[string]string{"GITEA_TOKEN": "env", "GITEA_INSTANCE_URL": instance},
			want: instance + "\n  ✓ Using GITEA_TOKEN from the environment\n",
		},
		{name: "logged out", failure: "not logged in to any Gitea instance; run gt auth login", exitCode: clierr.Authentication},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := terminalDeps(t)
			deps.Getenv = envFunc(tc.env)
			if tc.helper {
				deps.Git = &fakeGit{results: map[string]string{helper: "!'/usr/bin/gt' auth git-credential --host " + instance}}
			}
			if len(tc.users) > 0 {
				configureAccounts(t, deps, instance, tc.users[0], tc.users...)
			}
			err := Execute(context.Background(), deps, []string{"auth", "status"})
			if tc.failure != "" {
				require.Equal(t, tc.exitCode, clierr.ExitCode(err))
				require.EqualError(t, err, tc.failure)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, withoutANSI(out.String()))
			if tc.helper {
				out.Reset()
				require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--json", "user,git_credential_helper"}))
				require.JSONEq(t, `[{"user":"alice","git_credential_helper":"global"},{"user":"bob","git_credential_helper":"global"}]`, withoutANSI(out.String()))
			}
		})
	}
	deps, out, _ := testDeps(t)
	configureHost(t, deps, instance, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice")})
	err := Execute(context.Background(), deps, []string{"auth", "status"})
	require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
	require.EqualError(t, err, "1 of 1 credentials cannot authenticate; log in again with gt auth login")
	require.Equal(t, instance+"\talice\ttrue\ttoken\tfile\tfalse\t\t\tcredential unavailable; run gt auth login for this instance and account\n", out.String())
}

func TestAuthStatusWarnsWhenTheEnvironmentTokenIsAnotherAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"bob"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	deps.Getenv = envFunc(map[string]string{"GITEA_TOKEN": "env", "GITEA_CLI_HOST": server.URL})
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--verify", "--json", "user,active"}))
	require.JSONEq(t, `[{"user":"bob","active":true},{"user":"alice","active":false}]`, out.String())
	require.Equal(t, "! GITEA_TOKEN authenticates as bob, not the active account alice; commands on "+server.URL+" act as bob\n", withoutANSI(diagnostics.String()))
}

func TestAuthSwitchSelectsInstanceAndAccount(t *testing.T) {
	const instance = "https://example.org"
	deps, _, diagnostics := terminalDeps(t)
	configureAccounts(t, deps, instance, "alice", "alice")
	err := Execute(context.Background(), deps, []string{"auth", "switch"})
	require.EqualError(t, err, "alice is the only account on "+instance+"; log in to another with gt auth login --host "+instance)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	configureAccounts(t, deps, instance, "alice", "alice", "bob")
	active := func() string {
		s, err := deps.Settings.Load()
		require.NoError(t, err)
		return s.Hosts.Hosts[instance].User
	}
	for _, tc := range []struct {
		args       []string
		want, diag string
	}{
		{nil, "bob", "✓ Switched active account for " + instance + " to bob\n"},
		{nil, "alice", "✓ Switched active account for " + instance + " to alice\n"},
		{[]string{"ALICE"}, "alice", "! alice is already the active account on " + instance + "\n"},
		{[]string{"--user", "bob"}, "bob", "✓ Switched active account for " + instance + " to bob\n"},
	} {
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"auth", "switch"}, tc.args...)))
		require.Equal(t, tc.want, active())
		require.Equal(t, tc.diag, withoutANSI(diagnostics.String()))
	}
	configureAccounts(t, deps, instance, "bob", "alice", "bob", "carol")
	prompt := &loginPrompter{choice: 2}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "switch"}))
	require.Equal(t, []string{"alice", "bob (active)", "carol"}, prompt.options)
	require.Equal(t, "carol", active())
	err = Execute(context.Background(), deps, []string{"auth", "switch", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "name the account: gt auth switch <user> (accounts on "+instance+": alice, bob, carol)")
	err = Execute(context.Background(), deps, []string{"auth", "switch", "dave"})
	require.EqualError(t, err, `account "dave" is not logged in on `+instance+" (accounts: alice, bob, carol)")
}

func TestAuthTokenPrintsTheCredential(t *testing.T) {
	server := newAuthenticatedServer(http.NotFoundHandler())
	defer server.Close()
	instance := server.URL
	deps, out, _ := testDeps(t)
	configureAccounts(t, deps, instance, "alice", "alice")
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "token"}))
	require.Equal(t, "alice-token\n", out.String())
	err := Execute(context.Background(), deps, []string{"auth", "token", "--user", "bob"})
	require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
	out.Reset()
	deps.Getenv = envFunc(map[string]string{"GITEA_TOKEN": "env-token", "GITEA_CLI_HOST": instance})
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "token"}))
	require.Equal(t, "env-token\n", out.String())
	deps.Getenv = envFunc(nil)
	configureHost(t, deps, instance, settings.Host{User: "alice", Users: map[string]settings.Account{"alice": {AuthMethod: settings.AuthHTTPSign, SSHKey: "/keys/id"}}})
	require.EqualError(t, Execute(context.Background(), deps, []string{"auth", "token"}), "alice on "+instance+" signs requests with an SSH key and has no token")
	require.EqualError(t, Execute(context.Background(), deps, []string{"auth", "token", "--json"}), "auth token prints the token; --json is not supported")
}

// rejectingGit records the input of Git commands that read standard input.
type rejectingGit struct {
	fakeGit
	inputs []string
}

func (g *rejectingGit) Input(_ context.Context, _, input string, args ...string) (string, error) {
	g.inputs = append(g.inputs, strings.Join(args, " ")+"\n"+input)
	return "", nil
}

func TestLogoutAsksGitToForgetTheAccount(t *testing.T) {
	const instance = "https://example.org:3000"
	deps, _, diagnostics := terminalDeps(t)
	runner := &rejectingGit{}
	deps.Git = runner
	configureAccounts(t, deps, instance, "alice", "alice", "bob")
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "logout", "--yes"}))
	require.Equal(t, []string{"credential reject\nprotocol=https\nhost=example.org:3000\nusername=alice\npassword=alice-token\n\n"}, runner.inputs)
	require.Equal(t, "✓ Logged out of "+instance+" (alice)\n✓ Switched active account for "+instance+" to bob\n", withoutANSI(diagnostics.String()))
	err := Execute(context.Background(), deps, []string{"auth", "logout", "carol", "--yes"})
	require.EqualError(t, err, `account "carol" is not logged in on `+instance+" (accounts: bob)")
}

func TestLogoutFindsNamedAccountOnAnotherInstanceThanTheClone(t *testing.T) {
	deps, _, _ := testDeps(t)
	deps.Credentials.Keyring = &statusKeyring{}
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": "https://a.example.org/alice/project.git"}}
	configureHost(t, deps, "https://a.example.org", settings.Host{User: "alice", Users: tokenAccounts(settings.StoreKeyring, "alice")})
	configureHost(t, deps, "https://b.example.org", settings.Host{User: "bob", Users: tokenAccounts(settings.StoreKeyring, "bob"), SSHHost: "ssh.b.example.org:2222"})
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.DefaultHost = "https://b.example.org"; return nil }))
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "logout", "bob", "--yes", "--no-input"}))
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.NotContains(t, snapshot.Hosts.Hosts["https://b.example.org"].Users, "bob")
	require.Equal(t, "ssh.b.example.org:2222", snapshot.Hosts.Hosts["https://b.example.org"].SSHHost, "the instance stays for its settings")
	require.Contains(t, snapshot.Hosts.Hosts["https://a.example.org"].Users, "alice")
	require.Equal(t, "https://a.example.org", snapshot.Preferences.DefaultHost, "default_host moves off the instance without accounts")
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "logout", "alice", "--yes", "--no-input"}))
	snapshot, err = deps.Settings.Load()
	require.NoError(t, err)
	require.Empty(t, snapshot.Preferences.DefaultHost)
}

func TestAuthStatusVerifyHonorsInstanceInsecureForEnvironmentToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/token" {
			_, _ = w.Write([]byte(`{"scopes":["all"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()
	environment := map[string]string{"GITEA_INSTANCE_URL": server.URL, "GITEA_TOKEN": "secret"}
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(environment)
	require.Error(t, Execute(context.Background(), deps, []string{"auth", "status", "--verify"}))
	out.Reset()
	environment["GITEA_INSTANCE_INSECURE"] = "true"
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--verify", "--json=user,verified"}))
	require.JSONEq(t, `[{"user":"alice","verified":true}]`, out.String())
}

// An instance with REQUIRE_SIGNIN_VIEW answers every unsigned request, /api/v1/version included,
// with 403; login must only make the signed requests.
func TestAuthLoginSSHKeyNeverAsksTheVersionOfAPrivateInstance(t *testing.T) {
	keyPath := writeAppTempSSHKey(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Signature") == "" {
			http.Error(w, `{"message":"Only signed in user is allowed to call APIs."}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{
		"auth", "login", "--host", server.URL, "--ssh-key", keyPath, "--no-input", "--yes",
	}))
}

func TestAuthSwitchTreatsThePositionalAccountLikeUser(t *testing.T) {
	const instance = "https://example.org"
	deps, _, _ := terminalDeps(t)
	configureAccounts(t, deps, instance, "alice", "alice", "bob")
	err := Execute(context.Background(), deps, []string{"auth", "switch", "bob", "--user", "alice"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "the account argument bob does not match --user alice")
	s, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "alice", s.Hosts.Hosts[instance].User)
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "switch", "BOB", "--user", "bob"}))
}

func TestLoginHeadersReachEveryRequestToTheInstance(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Cf-Access-Client-Id")+" "+r.Header.Get("Cf-Access-Client-Secret"))
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"CF_SECRET": "from-env"})
	deps.Streams.In = strings.NewReader("token\n")
	login := []string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file", "--no-input"}
	require.NoError(t, Execute(context.Background(), deps, append(login, "--header", "cf-access-client-id: client", "--header", "CF-Access-Client-Secret: $CF_SECRET")))
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, map[string]string{"Cf-Access-Client-Id": "client", "Cf-Access-Client-Secret": "$CF_SECRET"}, snapshot.Hosts.Hosts[server.URL].Headers)
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user", "--host", server.URL}))
	require.Equal(t, []string{"client from-env", "client from-env"}, seen)

	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "headers", "Cf-Access-Client-Secret:", "--host", server.URL}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "headers", "--host", server.URL, "--json"}))
	require.JSONEq(t, `{"headers":{"Cf-Access-Client-Id":"client"}}`, out.String())
	requireUsageBeforeRequests(t, "--client-key requires --client-cert", "auth", "login", "--host", server.URL, "--with-token", "--client-key", "key.pem")
	for _, header := range []string{"Authorization: token other", "Bad Name: x", "missing colon"} {
		requireUsageBeforeRequests(t, "", "auth", "login", "--host", server.URL, "--with-token", "--header", header)
	}
}

func TestHeaderValuesNameVariablesAsDollarNameOrBraced(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Braced"))
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"BRACED_SECRET": "from-env"})
	deps.Streams.In = strings.NewReader("token\n")
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file", "--no-input", "--header", "X-Braced: ${BRACED_SECRET}", "--header", "X-Unset: $UNSET_SECRET"}))
	require.Contains(t, diagnostics.String(), "X-Unset is not sent: $UNSET_SECRET is not set\n")
	require.NotContains(t, diagnostics.String(), "X-Braced")
	require.NoError(t, Execute(context.Background(), deps, []string{"api", "user", "--host", server.URL}))
	require.Equal(t, []string{"from-env", "from-env"}, seen)

	for _, value := range []string{"$NOT-A-VAR name", "${UNCLOSED", "$", "${}", "$1ST"} {
		err := Execute(context.Background(), deps, []string{"config", "set", "headers", "X-Bad: " + value, "--host", server.URL})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%q: %v", value, err)
		require.EqualError(t, err, fmt.Sprintf("header X-Bad must name an environment variable as $NAME or ${NAME}, not %q", value))
		requireUsageBeforeRequests(t, "header X-Bad must name an environment variable", "auth", "login", "--host", server.URL, "--with-token", "--header", "X-Bad: "+value)
	}
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "headers", "X-Good: ${_GOOD_1}", "--host", server.URL}))
	require.Contains(t, diagnostics.String(), "X-Good is not sent: ${_GOOD_1} is not set\n")
}

func TestLoginWithATokenThatCannotReadTheAccountNamesTheScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"token does not have at least one of required scope(s): [read:user]"}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Streams.In = strings.NewReader("a1b2c3\n")
	err := Execute(context.Background(), deps, []string{"auth", "login", "--host", server.URL, "--with-token", "--credential-store", "file", "--no-input"})
	require.ErrorContains(t, Explain(context.Background(), deps, err), "token does not have at least one of required scope(s): [read:user]; create a token with that scope")
}
