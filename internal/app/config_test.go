// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestConfigReportsEffectivePreferenceSourcesWithoutAuthentication(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.DefaultHost = "https://stored.example.org"; return nil }))
	configureHost(t, deps, "https://env.example.org/base", settings.Host{GitProtocol: "ssh"})
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": "https://ENV.example.org/base/"})
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "default_host", "--show-source", "--json"}))
	require.JSONEq(t, `{"default_host":{"value":"https://env.example.org/base","source":"TEA_HOST"}}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "git_protocol", "--show-source", "--json"}))
	require.JSONEq(t, `{"git_protocol":{"value":"ssh","source":"hosts.yml https://env.example.org/base"}}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "default_host", "--host", "https://flag.example.org", "--show-source", "--json"}))
	require.JSONEq(t, `{"default_host":{"value":"https://flag.example.org","source":"--host"}}`, out.String())
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "https://stored.example.org", snapshot.Preferences.DefaultHost)
	require.Equal(t, "https", snapshot.Preferences.GitProtocol)
}

func TestInstanceConfigUpdatesPreserveAccountsAndGlobalPreferences(t *testing.T) {
	deps, out, _ := testDeps(t)
	instance := "https://example.org:8443/base"
	configureHost(t, deps, instance, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice")})
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "editor", "vim"}))
	global, err := os.ReadFile(filepath.Join(deps.Settings.Dir, "config.yml"))
	require.NoError(t, err)
	for _, value := range [][2]string{{"git_protocol", "ssh"}, {"ssh_host", "git.example.org:2222"}, {"insecure", "true"}, {"oauth_client_id", "tea-client"}} {
		require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", value[0], value[1], "--host", instance + "/"}))
	}
	current, err := os.ReadFile(filepath.Join(deps.Settings.Dir, "config.yml"))
	require.NoError(t, err)
	require.Equal(t, global, current)
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "alice", snapshot.Hosts.Hosts[instance].User)
	require.Len(t, snapshot.Hosts.Hosts[instance].Users, 1)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "ssh_host", "--host", instance, "--json", "--show-source"}))
	require.JSONEq(t, `{"ssh_host":{"value":"git.example.org:2222","source":"hosts.yml https://example.org:8443/base"}}`, out.String())
	before, err := os.ReadFile(filepath.Join(deps.Settings.Dir, "hosts.yml"))
	require.NoError(t, err)
	for _, value := range [][2]string{{"prompt", "disabled"}, {"git_protocol", "invalid"}, {"insecure", "yes"}, {"ssh_host", "bad/path"}, {"user", "other"}, {"oauth_token", "secret"}} {
		require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"config", "set", value[0], value[1], "--host", instance})))
	}
	after, err := os.ReadFile(filepath.Join(deps.Settings.Dir, "hosts.yml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestAccountConfigOnlyChangesSelectedAccountMetadata(t *testing.T) {
	deps, out, _ := testDeps(t)
	instance := "https://example.org/base"
	configureHost(t, deps, instance, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice", "bob")})
	for _, setting := range [][2]string{{"ssh_key", "/keys/my key"}, {"ssh_agent", "true"}, {"ssh_certificate_principal", "git"}, {"ssh_key_fingerprint", "SHA256:abcd"}} {
		require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", setting[0], setting[1], "--host", instance, "--user", "bob"}))
	}
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	host := snapshot.Hosts.Hosts[instance]
	require.Equal(t, "alice", host.User)
	require.Equal(t, tokenAccounts(settings.StoreFile, "alice")["alice"], host.Users["alice"])
	require.Equal(t, settings.Account{AuthMethod: "token", CredentialStore: "file", SSHKey: "/keys/my key", SSHAgent: true, SSHCertPrincipal: "git", SSHKeyFingerprint: "SHA256:abcd"}, host.Users["bob"])
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "ssh_agent", "--host", instance, "--user", "bob", "--show-source", "--json"}))
	require.JSONEq(t, `{"ssh_agent":{"value":true,"source":"hosts.yml https://example.org/base user bob"}}`, out.String())
	before, err := os.ReadFile(filepath.Join(deps.Settings.Dir, "hosts.yml"))
	require.NoError(t, err)
	for _, setting := range [][2]string{{"ssh_agent", "yes"}, {"ssh_key", "bad\npath"}, {"ssh_certificate_principal", "bad\x00principal"}, {"ssh_key_fingerprint", "bad\nprint"}, {"credential_store", "keyring"}, {"auth_method", "oauth"}, {"git_protocol", "ssh"}} {
		require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"config", "set", setting[0], setting[1], "--host", instance, "--user", "bob"})))
	}
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"config", "set", "ssh_agent", "false", "--user", "bob"})))
	after, err := os.ReadFile(filepath.Join(deps.Settings.Dir, "hosts.yml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestConfigHelpDocumentsSettingsAndEnvironment(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "--help"}))
	for _, name := range []string{
		"default_host", "git_protocol", "editor", "prompt", "pager", "browser", "color", "remote", "aliases",
		"user", "ssh_host", "insecure", "oauth_client_id", "ssh_key", "ssh_agent", "ssh_certificate_principal", "ssh_key_fingerprint",
		"TEA_CONFIG_DIR", "TEA_HOST", "GITEA_INSTANCE_URL", "GITEA_TOKEN", "TEA_USER", "TEA_REPO", "TEA_EDITOR", "VISUAL", "EDITOR",
		"TEA_PAGER", "PAGER", "TEA_COLOR", "NO_COLOR", "BROWSER",
	} {
		require.Contains(t, out.String(), name)
	}
}
