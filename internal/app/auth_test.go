// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
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

	"gitea.dev/tea/internal/auth"
	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/settings"
	"gitea.dev/tea/internal/transport"
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
		{"flag overrides environment", "bob", "alice", true},
		{"flag mismatches", "alice", "bob", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, _, _ := testDeps(t)
			deps.Streams.In = strings.NewReader("private-login-token\n")
			deps.Getenv = envFunc(map[string]string{"TEA_USER": test.environment})
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
			} else {
				require.Empty(t, snapshot.Hosts.Hosts)
			}
		})
	}
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
	require.ErrorContains(t, err, "account is not configured")
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
	require.JSONEq(t, `[{"host":"https://example.org/base","user":"bob","active":false,"credential_store":"keyring","credential_available":true,"expires_at":null,"verified":null,"error":""}]`, out.String())
	out.Reset()
	keyring.reads = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "status", "--host", "https://unconfigured.example.org", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	require.Empty(t, keyring.reads)
	out.Reset()
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"auth", "status", "--host", "invalid-host"})))
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
		{name: "environment account", environment: map[string]string{"TEA_HOST": "https://example.org", "TEA_USER": "bob", "GITEA_TOKEN": "expired"}, selected: "bob"},
		{name: "flag overrides environment", flags: []string{"--host", "https://example.org", "--user", "alice"}, environment: map[string]string{"TEA_HOST": "https://other.example.org", "TEA_USER": "bob"}, selected: "alice"},
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
	require.Equal(t, "yes", method.SSHCertPrincipal)
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
			require.ErrorContains(t, err, "select exactly one")
			require.Empty(t, out.String())
		})
	}
}

func TestAuthLoginValidatesMethodBeforePromptingForHost(t *testing.T) {
	for args, want := range map[string]string{
		"auth login":                            "select exactly one",
		"auth login --with-token --client-id x": "--client-id requires --oauth",
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
		requests++
		require.Equal(t, "/api/v1/user", r.URL.Path)
		if r.Header.Get("Authorization") != "token valid-status-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"login":"alice"}`))
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
	require.JSONEq(t, `[{"user":"alice","verified":null,"error":""},{"user":"bob","verified":null,"error":""},{"user":"carol","verified":null,"error":""}]`, out.String())
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
	require.Equal(t, server.URL+"\talice\tactive=true\tcredentials=true\texpires=2030-01-02T03:04:05Z\tverified=true\n", out.String())
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
	browser := &channelBrowser{opened: opened}
	deps, _, diagnostics := testDeps(t)
	deps.Browser = browser
	deps.Credentials = &auth.Store{Dir: deps.Settings.Dir, Keyring: &statusKeyring{}}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
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
	require.Contains(t, diagnostics.String(), "Open this URL to authorize tea:")
	require.Contains(t, diagnostics.String(), resource)
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	normalized, err := settings.NormalizeHost(server.URL)
	require.NoError(t, err)
	require.Contains(t, snapshot.Hosts.Hosts[normalized].Users, "alice")
}
