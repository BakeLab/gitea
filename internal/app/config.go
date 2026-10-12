// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gitea.dev/cli/internal/aliases"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) effectivePreferences(snapshot settings.Snapshot) (settings.Preferences, map[string]string, error) {
	preferences := snapshot.Preferences
	sources := map[string]string{}
	for key, configured := range snapshot.Configured {
		if configured {
			sources[key] = "config.yml"
		}
	}
	host, source := preferences.DefaultHost, sources["default_host"]
	if a.options.host != "" {
		host, source = a.options.host, "--host"
	} else {
		for _, key := range []string{"GITEA_CLI_HOST", "GITEA_INSTANCE_URL"} {
			if value := a.deps.Getenv(key); value != "" {
				host, source = value, key
				break
			}
		}
	}
	if host == "" && len(snapshot.Hosts.Hosts) == 1 {
		for name := range snapshot.Hosts.Hosts {
			host, source = name, "sole configured instance"
		}
	}
	if host != "" {
		normalized, err := settings.NormalizeHost(host)
		if err != nil {
			return preferences, nil, clierr.Wrap(clierr.Usage, err)
		}
		preferences.DefaultHost = normalized
		sources["default_host"] = source
		if protocol := snapshot.Hosts.Hosts[normalized].GitProtocol; protocol != "" {
			preferences.GitProtocol = protocol
			sources["git_protocol"] = "hosts.yml " + normalized
		}
	}
	if a.options.remote != "" {
		preferences.Remote = a.options.remote
		sources["remote"] = "--remote"
	}
	if value := strings.TrimSpace(a.deps.Getenv("GITEA_CLI_PAGER")); value != "" {
		preferences.Pager = value
		sources["pager"] = "GITEA_CLI_PAGER"
	} else if value := strings.TrimSpace(a.deps.Getenv("PAGER")); value != "" && sources["pager"] == "" {
		preferences.Pager = value
		sources["pager"] = "PAGER"
	}
	if value := strings.TrimSpace(a.deps.Getenv("GITEA_CLI_COLOR")); value != "" {
		preferences.Color = value
		sources["color"] = "GITEA_CLI_COLOR"
	}
	if value := a.deps.Getenv("GITEA_CLI_EDITOR"); value != "" {
		preferences.Editor = value
		sources["editor"] = "GITEA_CLI_EDITOR"
	} else if value := a.deps.Getenv("VISUAL"); value != "" && sources["editor"] == "" {
		preferences.Editor = value
		sources["editor"] = "VISUAL"
	} else if value := a.deps.Getenv("EDITOR"); value != "" && sources["editor"] == "" {
		preferences.Editor = value
		sources["editor"] = "EDITOR"
	}
	if value := a.deps.Getenv("BROWSER"); value != "" && sources["browser"] == "" {
		preferences.Browser = value
		sources["browser"] = "BROWSER"
	}
	return preferences, sources, nil
}

func (a *application) configCommand() *cobra.Command {
	group := &cobra.Command{Use: "config", Short: "Manage preferences", Long: `Manage preferences.

Settings live in config.yml and hosts.yml in the configuration directory
($GITEA_CLI_CONFIG_DIR, or gt under the XDG config directory); gt config path prints both.

config.yml preferences (gt config set <key> <value>):
  default_host  instance URL used when no --host, environment or repository selects one
  git_protocol  https or ssh for clone and checkout remotes
  editor        command used to edit bodies
  prompt        enabled or disabled
  pager         command used to page human output
  browser       command used to open web pages; never disables opening
  color         auto, always or never
  remote        Git remote used for repository discovery
  aliases       command aliases; manage them with gt alias

hosts.yml per-instance settings (gt config set --host <url> <key> <value>):
  user             active account, changed only by gt auth switch
  ssh_host         SSH host[:port] when it differs from the web host
  insecure         true skips TLS certificate verification
  git_protocol     https or ssh, overriding config.yml for this instance
  oauth_client_id  OAuth application used by gt auth login

Per-account SSH settings (gt config set --host <url> --user <name> <key> <value>):
  ssh_key, ssh_agent, ssh_certificate_principal, ssh_key_fingerprint

Environment overrides:
  GITEA_CLI_CONFIG_DIR                 configuration directory
  GITEA_CLI_HOST, GITEA_INSTANCE_URL   instance, overriding default_host
  GITEA_TOKEN                    access token for that instance instead of stored credentials
  GITEA_INSTANCE_INSECURE        with GITEA_TOKEN, skip TLS verification when true
  GITEA_INSTANCE_SSH_HOST        with GITEA_TOKEN, SSH host that identifies the instance's remotes
  GITEA_CLI_USER                       account on the selected instance
  GITEA_CLI_REPO                       repository as owner/name or URL
  GITEA_CLI_EDITOR                     editor, overriding config.yml
  GITEA_CLI_PAGER                      pager, overriding config.yml
  GITEA_CLI_COLOR                      color preference; NO_COLOR disables automatic color
  VISUAL, EDITOR, PAGER, BROWSER editor (VISUAL first), pager and browser when config.yml sets none`}
	group.AddCommand(&cobra.Command{Use: "path", Short: "Show configuration file paths", Annotations: textOutput("config path prints text"), Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "config\t%s\nhosts\t%s\n", filepath.Join(a.deps.Settings.Dir, "config.yml"), filepath.Join(a.deps.Settings.Dir, "hosts.yml"))
		return err
	}})
	for _, name := range []string{"get", "list"} {
		var source bool
		command := &cobra.Command{Use: name, Short: "Show effective preferences"}
		command.Flags().BoolVar(&source, "show-source", false, "Show the source of each effective value")
		if name == "get" {
			command.Use = "get <key>"
			command.Args = argsExact(1)
		} else {
			command.Args = argsExact(0)
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			if a.options.user != "" && a.options.host == "" {
				return clierr.Wrap(clierr.Usage, errors.New("account settings require both --host and --user"))
			}
			snapshot, err := a.deps.Settings.Load()
			if err != nil {
				return err
			}
			preferences, sources, err := a.effectivePreferences(snapshot)
			if err != nil {
				return err
			}
			if _, exists := snapshot.Hosts.Hosts[preferences.DefaultHost]; a.options.user != "" && !exists {
				return clierr.Wrap(clierr.Usage, errors.New("instance is not configured; run gt auth login first"))
			}
			values := map[string]any{
				"version": preferences.Version, "default_host": preferences.DefaultHost,
				"git_protocol": preferences.GitProtocol, "editor": preferences.Editor,
				"prompt": preferences.Prompt, "pager": preferences.Pager,
				"browser": preferences.Browser, "color": preferences.Color,
				"remote": preferences.Remote,
			}
			if host, exists := snapshot.Hosts.Hosts[preferences.DefaultHost]; a.options.host != "" && exists {
				for key, value := range map[string]any{"ssh_host": host.SSHHost, "insecure": host.Insecure, "oauth_client_id": host.OAuthClientID} {
					values[key] = value
					sources[key] = "hosts.yml " + preferences.DefaultHost
				}
				if a.options.user != "" {
					user, exists := matchAccount(host.Users, a.options.user)
					if !exists {
						return clierr.Wrap(clierr.Usage, errors.New("account is not configured on the selected instance"))
					}
					account := host.Users[user]
					for key, value := range map[string]any{"ssh_key": account.SSHKey, "ssh_agent": account.SSHAgent, "ssh_certificate_principal": account.SSHCertPrincipal, "ssh_key_fingerprint": account.SSHKeyFingerprint} {
						values[key] = value
						sources[key] = "hosts.yml " + preferences.DefaultHost + " user " + user
					}
				}
			}
			if len(args) > 0 {
				value, ok := values[args[0]]
				if !ok {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown preference %q", args[0]))
				}
				values = map[string]any{args[0]: value}
			}
			if a.options.json != "" {
				if source {
					results := map[string]any{}
					for key, value := range values {
						origin := sources[key]
						if origin == "" {
							origin = "default"
						}
						results[key] = struct {
							Value  any    `json:"value"`
							Source string `json:"source"`
						}{value, origin}
					}
					return a.writeJSON(cmd, results)
				}
				return a.writeJSON(cmd, values)
			}
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				suffix := ""
				if source {
					origin := "default"
					if sources[key] != "" {
						origin = sources[key]
					}
					suffix = "\t" + origin
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%v%s\n", key, values[key], suffix); err != nil {
					return err
				}
			}
			return nil
		}
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{Use: "set <key> <value>", Short: "Set a preference, or an instance setting with --host", Annotations: textOutput("config set does not return a resource"), Args: argsExact(2), RunE: func(_ *cobra.Command, args []string) error {
		if a.options.user != "" && a.options.host == "" {
			return clierr.Wrap(clierr.Usage, errors.New("account settings require both --host and --user"))
		}
		if a.options.host != "" {
			instance, err := settings.NormalizeHost(a.options.host)
			if err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			return a.deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
				host, exists := s.Hosts.Hosts[instance]
				if !exists {
					return clierr.Wrap(clierr.Usage, errors.New("instance is not configured; run gt auth login first"))
				}
				if a.options.user != "" {
					user, exists := matchAccount(host.Users, a.options.user)
					if !exists {
						return clierr.Wrap(clierr.Usage, errors.New("account is not configured on the selected instance"))
					}
					account := host.Users[user]
					switch args[0] {
					case "ssh_key":
						account.SSHKey = args[1]
					case "ssh_agent":
						if args[1] != "true" && args[1] != "false" {
							return clierr.Wrap(clierr.Usage, errors.New("ssh_agent must be true or false"))
						}
						account.SSHAgent = args[1] == "true"
					case "ssh_certificate_principal":
						account.SSHCertPrincipal = args[1]
					case "ssh_key_fingerprint":
						account.SSHKeyFingerprint = args[1]
					default:
						return clierr.Wrap(clierr.Usage, errors.New("supported account settings: ssh_key, ssh_agent, ssh_certificate_principal, ssh_key_fingerprint"))
					}
					host.Users[user] = account
					s.Hosts.Hosts[instance] = host
					return clierr.Wrap(clierr.Usage, s.Validate())
				}
				switch args[0] {
				case "git_protocol":
					host.GitProtocol = args[1]
				case "ssh_host":
					host.SSHHost = args[1]
				case "insecure":
					if args[1] != "true" && args[1] != "false" {
						return clierr.Wrap(clierr.Usage, errors.New("insecure must be true or false"))
					}
					host.Insecure = args[1] == "true"
				case "oauth_client_id":
					host.OAuthClientID = args[1]
				default:
					return clierr.Wrap(clierr.Usage, fmt.Errorf("preference %q cannot be set per instance; supported keys: git_protocol, ssh_host, insecure, oauth_client_id", args[0]))
				}
				s.Hosts.Hosts[instance] = host
				return clierr.Wrap(clierr.Usage, s.Validate())
			})
		}
		return a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
			p := &s.Preferences
			switch args[0] {
			case "default_host":
				if args[1] == "" {
					p.DefaultHost = ""
					break
				}
				normalized, err := settings.NormalizeHost(args[1])
				if err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
				p.DefaultHost = normalized
			case "git_protocol":
				p.GitProtocol = args[1]
			case "editor":
				p.Editor = args[1]
			case "prompt":
				p.Prompt = args[1]
			case "pager":
				p.Pager = args[1]
			case "browser":
				p.Browser = args[1]
			case "color":
				p.Color = args[1]
			case "remote":
				p.Remote = args[1]
			default:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown or immutable preference %q", args[0]))
			}
			return clierr.Wrap(clierr.Usage, s.Validate())
		})
	}})
	return group
}

func (a *application) aliasCommand() *cobra.Command {
	group := &cobra.Command{Use: "alias", Short: "Manage shortcuts to built-in commands"}
	group.AddCommand(&cobra.Command{Use: "set <name> <expansion>", Short: "Define a command alias", Annotations: textOutput("alias set does not return a resource"), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		return a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
			s.Preferences.Aliases[args[0]] = args[1]
			return clierr.Wrap(clierr.Usage, aliases.Check(args[0], s.Preferences.Aliases, builtins(cmd.Root())))
		})
	}})
	group.AddCommand(&cobra.Command{Use: "delete <name>", Short: "Remove a command alias", Annotations: textOutput("alias deletion does not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
			if _, ok := s.Preferences.Aliases[args[0]]; !ok {
				return clierr.Wrap(clierr.Usage, errors.New("alias does not exist"))
			}
			delete(s.Preferences.Aliases, args[0])
			return nil
		})
	}})
	group.AddCommand(&cobra.Command{Use: "list", Short: "List command aliases", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, s.Preferences.Aliases)
		}
		names := make([]string, 0, len(s.Preferences.Aliases))
		for name := range s.Preferences.Aliases {
			names = append(names, name)
		}
		sort.Strings(names)
		return a.writeHuman(cmd, table(names, func(name string) []terminal.Cell { return terminal.Row(name, s.Preferences.Aliases[name]) }, a.colorEnabled()))
	}})
	return group
}
