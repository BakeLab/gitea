// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestConfigReportsEffectivePreferenceSourcesWithoutAuthentication(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error { s.Preferences.DefaultHost = "https://stored.example.org"; return nil }))
	configureHost(t, deps, "https://env.example.org/base", settings.Host{GitProtocol: "ssh"})
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": "https://ENV.example.org/base/"})
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "default_host", "--show-source", "--json"}))
	require.JSONEq(t, `{"default_host":{"value":"https://env.example.org/base","source":"GITEA_CLI_HOST"}}`, out.String())
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
	for _, value := range [][2]string{{"git_protocol", "ssh"}, {"ssh_host", "git.example.org:2222"}, {"insecure", "true"}, {"oauth_client_id", "gt-client"}} {
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
	// A relative key path is stored absolute, so that it names the same key from every directory.
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "ssh_key", "keys/id", "--host", instance, "--user", "bob"}))
	loaded, err := deps.Settings.Load()
	require.NoError(t, err)
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(wd, "keys", "id"), loaded.Hosts.Hosts[instance].Users["bob"].SSHKey)
}

func TestConfigHelpDocumentsSettingsAndEnvironment(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "--help"}))
	for _, name := range []string{
		"default_host", "git_protocol", "editor", "prompt", "pager", "browser", "color", "hyperlinks", "markdown", "remote", "aliases",
		"user", "ssh_host", "insecure", "oauth_client_id", "ssh_key", "ssh_agent", "ssh_certificate_principal", "ssh_key_fingerprint",
		"gt help environment",
	} {
		require.Contains(t, out.String(), name)
	}
	require.NotContains(t, out.String(), "GITEA_TOKEN", "the environment table lives in gt help environment")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"help", "environment"}))
	for _, name := range []string{"GITEA_CLI_CONFIG_DIR", "GITEA_CLI_HOST", "GITEA_INSTANCE_URL", "GITEA_TOKEN", "GITEA_CLI_USER", "GITEA_CLI_REPO", "GITEA_CLI_EDITOR", "VISUAL", "EDITOR", "GITEA_CLI_PAGER", "PAGER", "GITEA_CLI_COLOR", "NO_COLOR", "BROWSER"} {
		require.Contains(t, out.String(), name)
	}
}

func TestConfigGetPrintsOnlyTheValue(t *testing.T) {
	deps, out, _ := testDeps(t)
	configureHost(t, deps, "http://localhost:3001", settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice", "bob")})
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "editor", "code --wait"}))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"config", "get", "editor"}, "code --wait\n"},
		{[]string{"config", "get", "editor", "--show-source"}, "code --wait\tconfig.yml\n"},
		{[]string{"config", "get", "pager"}, "\n"},
		{[]string{"config", "get", "hyperlinks", "--show-source"}, "auto\tdefault\n"},
		{[]string{"config", "get", "user", "--host", "http://localhost:3001"}, "alice\n"},
		{[]string{"config", "get", "user", "--host", "localhost:3001", "--show-source"}, "alice\thosts.yml http://localhost:3001\n"},
		{[]string{"config", "get", "user"}, "alice\n"},
		{[]string{"config", "get", "insecure"}, "false\n"},
	} {
		t.Run(strings.Join(tc.args[2:], " "), func(t *testing.T) {
			out.Reset()
			require.NoError(t, Execute(context.Background(), deps, tc.args))
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestConfigNamesValidSettings(t *testing.T) {
	deps, _, _ := testDeps(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"config", "get", "nope"}, `unknown setting "nope"; settings: browser, color, default_host, editor, git_protocol, hyperlinks, insecure, markdown,`},
		{[]string{"config", "get", "user"}, "user is an instance setting; select a configured instance with --host URL"},
		{[]string{"config", "get", "ssh_key"}, "ssh_key is an account setting; select the account with --host URL --user NAME"},
		{[]string{"config", "set", "nope", "1"}, `unknown setting "nope"; preferences: browser, color, default_host,`},
		{[]string{"config", "set", "user", "bob"}, "the active account changes with gt auth switch"},
		{[]string{"config", "set", "hyperlinks", "off"}, "hyperlinks must be auto, always, or never"},
		{[]string{"config", "set", "markdown", "never"}, "markdown must be auto or raw"},
	} {
		t.Run(strings.Join(tc.args[1:], " "), func(t *testing.T) {
			err := Execute(context.Background(), deps, tc.args)
			require.Equal(t, clierr.Usage, clierr.ExitCode(err))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestConfigPathWorksWithBrokenSettings(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "hosts.yml"), []byte("version: '1'\nhosts: [\n"), 0o600))
	require.Error(t, Execute(context.Background(), deps, []string{"config", "list"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "path"}))
	require.Equal(t, "config\t"+filepath.Join(deps.Settings.Dir, "config.yml")+"\nhosts\t"+filepath.Join(deps.Settings.Dir, "hosts.yml")+"\n", out.String())
}

func TestConfigSetReportsChangesOnATerminal(t *testing.T) {
	deps, out, diagnostics := testDeps(t)
	deps.Streams.OutTTY = true
	configureHost(t, deps, "https://example.org", settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice")})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"config", "set", "markdown", "raw"}, "✓ Set markdown to raw\n"},
		{[]string{"config", "set", "editor", ""}, "✓ Cleared editor\n"},
		{[]string{"config", "set", "ssh_host", "git.example.org:2222", "--host", "example.org"}, "✓ Set ssh_host for https://example.org to git.example.org:2222\n"},
		{[]string{"config", "set", "default_host", "https://example.org"}, "✓ Set default_host to https://example.org\n"},
		{[]string{"config", "set", "default_host", "localhost:3002"}, "✓ Set default_host to https://localhost:3002\n! No account on https://localhost:3002 yet; log in with: gt auth login --host https://localhost:3002\n"},
	} {
		t.Run(strings.Join(tc.args[2:], " "), func(t *testing.T) {
			diagnostics.Reset()
			require.NoError(t, Execute(context.Background(), deps, tc.args))
			require.Equal(t, tc.want, withoutANSI(diagnostics.String()))
			require.Empty(t, out.String())
		})
	}
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "raw", snapshot.Preferences.Markdown)
	require.Equal(t, "git.example.org:2222", snapshot.Hosts.Hosts["https://example.org"].SSHHost)
}

func TestDisplayFollowsHyperlinkAndMarkdownPreferences(t *testing.T) {
	for _, tc := range []struct {
		config      []string
		links, rich bool
	}{
		{nil, true, true},
		{[]string{"hyperlinks", "never"}, false, true},
		{[]string{"markdown", "raw"}, true, false},
	} {
		deps, _, _ := testDeps(t)
		deps.Streams.OutTTY, deps.Now = true, time.Now
		for i := 0; i < len(tc.config); i += 2 {
			require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", tc.config[i], tc.config[i+1]}))
		}
		d := (&application{deps: deps}).display()
		require.Equal(t, tc.links, d.Links, tc.config)
		require.Equal(t, tc.rich, d.Markdown, tc.config)
	}
}

func TestAliasSetGuardsExistingAliasesAndQuoting(t *testing.T) {
	deps, _, diagnostics := testDeps(t)
	deps.Streams.OutTTY = true
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append([]string{"alias", "set"}, args...))
	}
	require.NoError(t, run("il", "gt issue list --label bug"))
	require.Equal(t, "✓ Added alias il for \"issue list --label bug\"\n", withoutANSI(diagnostics.String()))
	err := run("il", "issue list")
	require.ErrorContains(t, err, `alias il already expands to "issue list --label bug"; replace it with --clobber`)
	diagnostics.Reset()
	require.NoError(t, run("il", "issue list --state all", "--clobber"))
	require.Equal(t, "✓ Changed alias il from \"issue list --label bug\" to \"issue list --state all\"\n", withoutANSI(diagnostics.String()))
	err = run("ls", "issue", "list")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, `the expansion must be one argument; quote it: gt alias set ls "issue list"`)
	err = run("il", "issue", "list", "--state", "all")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `unknown flag: --state; quote an expansion with flags: gt alias set il "issue list --state all"`)
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, map[string]string{"il": "issue list --state all"}, snapshot.Preferences.Aliases)
}

func TestAliasDeleteAndList(t *testing.T) {
	deps, out, diagnostics := testDeps(t)
	deps.Streams.OutTTY = true
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append([]string{"alias"}, args...))
	}
	require.NoError(t, run("list"))
	require.Empty(t, out.String())
	require.Equal(t, "no aliases configured; create one with: gt alias set <name> <expansion>\n", diagnostics.String())
	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, run("set", name, "issue list"))
	}
	diagnostics.Reset()
	require.NoError(t, run("list"))
	require.Equal(t, "NAME  EXPANSION\na     issue list\nb     issue list\nc     issue list\n", withoutANSI(out.String()))
	err := run("delete", "a", "nope", "zz")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, `no alias named "nope", "zz"; gt alias list shows them`)
	require.NoError(t, run("delete", "a", "b"))
	require.Equal(t, "✓ Deleted alias a (issue list)\n✓ Deleted alias b (issue list)\n", withoutANSI(diagnostics.String()))
	require.Equal(t, clierr.Usage, clierr.ExitCode(run("delete")))
	require.Equal(t, clierr.Usage, clierr.ExitCode(run("delete", "c", "--all")))
	require.NoError(t, run("delete", "--all"))
	snapshot, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Empty(t, snapshot.Preferences.Aliases)
}

func TestPlatformCommandsComplete(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/notifications", r.URL.Path)
		_, _ = w.Write([]byte(notificationThreads))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice", "bob")
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "il", "issue list"}))
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"alias", "delete", ""}, []string{"il\tissue list"}},
		{[]string{"config", "get", "hyp"}, []string{"hyperlinks"}},
		{[]string{"config", "set", "hyperlinks", ""}, []string{"auto", "always", "never"}},
		{[]string{"auth", "switch", ""}, []string{"alice\t" + server.URL + " (active)", "bob\t" + server.URL}},
		{[]string{"auth", "login", "--credential-store", ""}, []string{"keyring", "file"}},
		{[]string{"auth", "login", "--scopes", "read:repository,write:re"}, []string{"read:repository,write:repository"}},
		{[]string{"notification", "list", "--type", ""}, []string{"issue", "pull", "commit", "repository"}},
		{[]string{"notification", "view", ""}, []string{"12\talice/project#17 Improve greeting", "3\talice/project#8 Improve error messages"}},
	} {
		candidates, directive := complete(t, deps, out, tc.args...)
		require.Equal(t, tc.want, candidates, tc.args)
		require.NotZero(t, directive&cobra.ShellCompDirectiveNoFileComp, tc.args)
	}
}

func TestConfigGetShowsTheEnvironmentOverridesOfTheTokenInstance(t *testing.T) {
	deps, out, _ := testDeps(t)
	configureHost(t, deps, "https://example.org", settings.Host{SSHHost: "ssh.example.org"})
	deps.Getenv = envFunc(map[string]string{"GITEA_TOKEN": "secret", "GITEA_CLI_HOST": "https://example.org", "GITEA_INSTANCE_SSH_HOST": "git.example.org:2222"})
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "get", "ssh_host", "--show-source", "--json"}))
	require.JSONEq(t, `{"ssh_host":{"value":"git.example.org:2222","source":"GITEA_INSTANCE_SSH_HOST"}}`, out.String())
}
