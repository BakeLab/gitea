// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/aliases"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// Settings that gt config reads and writes, by where they are stored.
var (
	preferenceKeys = []string{"browser", "color", "default_host", "editor", "git_protocol", "hyperlinks", "markdown", "pager", "prompt", "remote"}
	instanceKeys   = []string{"git_protocol", "insecure", "oauth_client_id", "ssh_host", "user"}
	accountKeys    = []string{"ssh_agent", "ssh_certificate_principal", "ssh_key", "ssh_key_fingerprint"}
	// configKeys are every setting that config get reads, in alphabetical order.
	configKeys = slices.Compact(slices.Sorted(slices.Values(slices.Concat(preferenceKeys, instanceKeys, accountKeys, []string{"version"}))))
)

// configValues lists the values of the settings that accept a fixed set.
var configValues = map[string][]string{
	"git_protocol": {"https", "ssh"},
	"prompt":       {"enabled", "disabled"},
	"color":        {"auto", "always", "never"},
	"hyperlinks":   {"auto", "always", "never"},
	"markdown":     {"auto", "raw"},
	"insecure":     {"true", "false"},
	"ssh_agent":    {"true", "false"},
}

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
		normalized, err := settings.ResolveHost(host, snapshot.Hosts.Hosts)
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

// configSetting is one effective setting.
type configSetting struct {
	Key   string
	Value any
}

// settingValues returns the effective preferences, the settings of the selected instance when it is
// configured, and with --user the settings of that account, each with its source.
func (a *application) settingValues(snapshot settings.Snapshot) (map[string]any, map[string]string, error) {
	if a.options.user != "" && a.options.host == "" {
		return nil, nil, clierr.Wrap(clierr.Usage, errors.New("account settings require both --host and --user"))
	}
	preferences, sources, err := a.effectivePreferences(snapshot)
	if err != nil {
		return nil, nil, err
	}
	values := map[string]any{
		"version": preferences.Version, "default_host": preferences.DefaultHost,
		"git_protocol": preferences.GitProtocol, "editor": preferences.Editor,
		"prompt": preferences.Prompt, "pager": preferences.Pager,
		"browser": preferences.Browser, "color": preferences.Color,
		"hyperlinks": preferences.Hyperlinks, "markdown": preferences.Markdown,
		"remote": preferences.Remote,
	}
	host, configured := snapshot.Hosts.Hosts[preferences.DefaultHost]
	if !configured {
		if a.options.user != "" {
			return nil, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("instance %s is not configured; run gt auth login --host %s", preferences.DefaultHost, preferences.DefaultHost))
		}
		return values, sources, nil
	}
	for key, value := range map[string]any{"user": host.User, "ssh_host": host.SSHHost, "insecure": host.Insecure, "oauth_client_id": host.OAuthClientID} {
		values[key] = value
		sources[key] = "hosts.yml " + preferences.DefaultHost
	}
	if a.options.user != "" {
		user, exists := matchAccount(host.Users, a.options.user)
		if !exists {
			return nil, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("account %q is not logged in on %s (accounts: %s)", a.options.user, preferences.DefaultHost, accountNames(host.Users)))
		}
		account := host.Users[user]
		for key, value := range map[string]any{"ssh_key": account.SSHKey, "ssh_agent": account.SSHAgent, "ssh_certificate_principal": account.SSHCertPrincipal, "ssh_key_fingerprint": account.SSHKeyFingerprint} {
			values[key] = value
			sources[key] = "hosts.yml " + preferences.DefaultHost + " user " + user
		}
	}
	return values, sources, nil
}

// unknownSetting explains why key has no value: it names an account or instance setting that needs
// --host or --user, or no setting at all.
func unknownSetting(key string) error {
	switch {
	case slices.Contains(accountKeys, key):
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is an account setting; select the account with --host URL --user NAME", key))
	case slices.Contains(instanceKeys, key):
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is an instance setting; select a configured instance with --host URL", key))
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown setting %q; settings: %s", key, strings.Join(configKeys, ", ")))
}

func (a *application) configCommand() *cobra.Command {
	group := &cobra.Command{Use: "config", Short: "Manage settings", Long: `Manage gt's settings.

Preferences live in config.yml and instance settings in hosts.yml, in the configuration
directory ($GITEA_CLI_CONFIG_DIR, or gt under the XDG config directory); gt config path prints
both files. Environment variables that override settings are listed in gt help environment.

Preferences: default_host, git_protocol, editor, prompt (enabled or disabled), pager,
browser (never disables opening), color, hyperlinks and markdown (auto or raw), remote and
aliases (see gt alias).
Instance settings (--host <url>): user, ssh_host, insecure, git_protocol, oauth_client_id.
Account settings (--host <url> --user <name>): ssh_key, ssh_agent,
ssh_certificate_principal, ssh_key_fingerprint.`}
	group.AddCommand(&cobra.Command{
		Use: "path", Short: "Print the paths of the settings files", Args: argsExact(0),
		Annotations: map[string]string{"gt.text": "config path prints text", "gt.offline": "true"},
		Example:     "$ gt config path\n# Settings of another configuration directory\n$ GITEA_CLI_CONFIG_DIR=~/work/gt gt config path",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "config\t%s\nhosts\t%s\n", filepath.Join(a.deps.Settings.Dir, "config.yml"), filepath.Join(a.deps.Settings.Dir, "hosts.yml"))
			return err
		},
	})
	var getSource bool
	get := &cobra.Command{
		Use: "get <key>", Short: "Print the value of a setting", Args: argsExact(1), ValidArgsFunction: a.completeConfig,
		Example: `$ gt config get editor
$ gt config get default_host --show-source
$ gt config get ssh_key --host https://gitea.com --user alice`,
		RunE: func(cmd *cobra.Command, args []string) error {
			snapshot, err := a.settings()
			if err != nil {
				return err
			}
			values, sources, err := a.settingValues(snapshot)
			if err != nil {
				return err
			}
			value, ok := values[args[0]]
			if !ok {
				return unknownSetting(args[0])
			}
			if a.options.json != "" {
				return a.writeSettingsJSON(cmd, map[string]any{args[0]: value}, sources, getSource)
			}
			line := fmt.Sprint(value)
			if getSource {
				line += "\t" + settingSource(sources, args[0])
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), line)
			return err
		},
	}
	get.Flags().BoolVar(&getSource, "show-source", false, "Also print where the value comes from")
	group.AddCommand(get)
	list := &cobra.Command{
		Use: "list", Short: "List settings and their values", Args: argsExact(0),
		Example: `$ gt config list
# Include the settings of an instance
$ gt config list --host https://gitea.com`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snapshot, err := a.settings()
			if err != nil {
				return err
			}
			values, _, err := a.settingValues(snapshot)
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, values)
			}
			items := make([]configSetting, 0, len(values))
			for _, key := range slices.Sorted(maps.Keys(values)) {
				items = append(items, configSetting{key, values[key]})
			}
			return writeTable(a, cmd, items, listSpec[configSetting]{
				Columns: []terminal.Column{{Header: "Key"}, {Header: "Value", Flex: true}},
				Row: func(item configSetting) []terminal.Cell {
					return []terminal.Cell{{Text: item.Key}, {Text: fmt.Sprint(item.Value)}}
				},
			}, listPage{Number: 1})
		},
	}
	group.AddCommand(list)
	group.AddCommand(&cobra.Command{
		Use: "set <key> <value>", Short: "Change a setting", Args: argsExact(2), ValidArgsFunction: a.completeConfig,
		Annotations: textOutput("config set does not return a resource"),
		Example: `$ gt config set editor "code --wait"
$ gt config set ssh_host git.example.com:2222 --host https://example.com
$ gt config set ssh_key ~/.ssh/id_ed25519 --host https://example.com --user alice`,
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if a.options.user != "" && a.options.host == "" {
				return clierr.Wrap(clierr.Usage, errors.New("account settings require both --host and --user"))
			}
			if a.options.host == "" {
				value, err := a.setPreference(key, value)
				if err != nil {
					return err
				}
				a.reportSet(cmd, key, value, "")
				if key == "default_host" && value != "" {
					a.warnWithoutAccount(cmd)
				}
				return nil
			}
			instance, err := a.setInstanceSetting(key, value)
			if err != nil {
				return err
			}
			scope := " for " + instance
			if a.options.user != "" {
				scope = " for " + a.options.user + " on " + instance
			}
			a.reportSet(cmd, key, value, scope)
			return nil
		},
	})
	return group
}

// settingSource names where the value of key comes from.
func settingSource(sources map[string]string, key string) string {
	if source := sources[key]; source != "" {
		return source
	}
	return "default"
}

// writeSettingsJSON writes settings as a key-value object, or with source as objects of value and source.
func (a *application) writeSettingsJSON(cmd *cobra.Command, values map[string]any, sources map[string]string, source bool) error {
	if !source {
		return a.writeJSON(cmd, values)
	}
	type sourced struct {
		Value  any    `json:"value"`
		Source string `json:"source"`
	}
	results := map[string]sourced{}
	for key, value := range values {
		results[key] = sourced{value, settingSource(sources, key)}
	}
	return a.writeJSON(cmd, results)
}

// reportSet confirms a changed setting; scope names the instance or account it belongs to.
func (a *application) reportSet(cmd *cobra.Command, key, value, scope string) {
	if value == "" {
		a.done(cmd, "Cleared %s%s", key, scope)
		return
	}
	a.done(cmd, "Set %s%s to %s", key, scope, value)
}

// warnWithoutAccount points out a default_host that has no account to act as yet.
func (a *application) warnWithoutAccount(cmd *cobra.Command) {
	snapshot, err := a.settings()
	if host := snapshot.Preferences.DefaultHost; err == nil && len(snapshot.Hosts.Hosts[host].Users) == 0 {
		a.warn(cmd, "No account on %s yet; log in with: gt auth login --host %s", host, host)
	}
}

// setPreference changes a preference in config.yml and returns the stored value, such as a
// normalized default_host.
func (a *application) setPreference(key, value string) (string, error) {
	return value, a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
		p := &s.Preferences
		switch key {
		case "default_host":
			if value != "" {
				normalized, err := settings.ResolveHost(value, s.Hosts.Hosts)
				if err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
				value = normalized
			}
			p.DefaultHost = value
		case "git_protocol":
			p.GitProtocol = value
		case "editor":
			p.Editor = value
		case "prompt":
			p.Prompt = value
		case "pager":
			p.Pager = value
		case "browser":
			p.Browser = value
		case "color":
			p.Color = value
		case "hyperlinks":
			p.Hyperlinks = value
		case "markdown":
			p.Markdown = value
		case "remote":
			p.Remote = value
		case "user":
			return clierr.Wrap(clierr.Usage, errors.New("the active account changes with gt auth switch"))
		default:
			if slices.Contains(instanceKeys, key) || slices.Contains(accountKeys, key) {
				return unknownSetting(key)
			}
			return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown setting %q; preferences: %s", key, strings.Join(preferenceKeys, ", ")))
		}
		return clierr.Wrap(clierr.Usage, s.Validate())
	})
}

// setInstanceSetting changes a setting of the --host instance in hosts.yml, or with --user one of its
// account's settings, and returns the instance.
func (a *application) setInstanceSetting(key, value string) (string, error) {
	var instance string
	return instance, a.deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		var err error
		if instance, err = settings.ResolveHost(a.options.host, s.Hosts.Hosts); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		host, exists := s.Hosts.Hosts[instance]
		if !exists {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("instance %s is not configured; run gt auth login --host %s", instance, instance))
		}
		if a.options.user != "" {
			user, exists := matchAccount(host.Users, a.options.user)
			if !exists {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("account %q is not logged in on %s (accounts: %s)", a.options.user, instance, accountNames(host.Users)))
			}
			account := host.Users[user]
			switch key {
			case "ssh_key":
				account.SSHKey = value
			case "ssh_agent":
				if value != "true" && value != "false" {
					return clierr.Wrap(clierr.Usage, errors.New("ssh_agent must be true or false"))
				}
				account.SSHAgent = value == "true"
			case "ssh_certificate_principal":
				account.SSHCertPrincipal = value
			case "ssh_key_fingerprint":
				account.SSHKeyFingerprint = value
			default:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%q is not an account setting; account settings: %s", key, strings.Join(accountKeys, ", ")))
			}
			host.Users[user] = account
			s.Hosts.Hosts[instance] = host
			return clierr.Wrap(clierr.Usage, s.Validate())
		}
		switch key {
		case "git_protocol":
			host.GitProtocol = value
		case "ssh_host":
			host.SSHHost = value
		case "insecure":
			if value != "true" && value != "false" {
				return clierr.Wrap(clierr.Usage, errors.New("insecure must be true or false"))
			}
			host.Insecure = value == "true"
		case "oauth_client_id":
			host.OAuthClientID = value
		case "user":
			return clierr.Wrap(clierr.Usage, errors.New("the active account changes with gt auth switch"))
		default:
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%q is not an instance setting; instance settings: git_protocol, insecure, oauth_client_id, ssh_host", key))
		}
		s.Hosts.Hosts[instance] = host
		return clierr.Wrap(clierr.Usage, s.Validate())
	})
}

func (a *application) aliasCommand() *cobra.Command {
	group := &cobra.Command{Use: "alias", Short: "Create shortcuts for gt commands", Long: `Create shortcuts for gt commands.

An alias expands to a gt command with its arguments before gt runs it: after
gt alias set il "issue list --label bug", gt il --state all runs
gt issue list --label bug --state all. $1, $2 and so on in the expansion take the
arguments in that position; other arguments follow the expansion. Aliases never run a
shell, and built-in commands always win over an alias of the same name.`}
	var clobber bool
	set := &cobra.Command{
		Use: "set <name> <expansion>", Short: "Create a shortcut for a gt command",
		Annotations: textOutput("alias set does not return a resource"),
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 2 {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("the expansion must be one argument; quote it: gt alias set %s %q", args[0], strings.Join(args[1:], " ")))
			}
			return argsExact(2)(cmd, args)
		},
		Example: `# gt il runs gt issue list --label bug
$ gt alias set il "issue list --label bug"
# $1 takes the first argument: gt review 13 runs gt pr review 13 --approve
$ gt alias set review 'pr review $1 --approve'
# Replace an existing alias
$ gt alias set il "issue list --assignee @me" --clobber`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, expansion := args[0], strings.TrimPrefix(strings.TrimSpace(args[1]), "gt ")
			var previous string
			err := a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
				previous = s.Preferences.Aliases[name]
				if previous != "" && !clobber {
					return fmt.Errorf("alias %s already expands to %q; replace it with --clobber", name, previous)
				}
				s.Preferences.Aliases[name] = expansion
				return clierr.Wrap(clierr.Usage, aliases.Check(name, s.Preferences.Aliases, builtins(cmd.Root())))
			})
			if err != nil {
				return err
			}
			if previous != "" {
				a.done(cmd, "Changed alias %s from %q to %q", name, previous, expansion)
			} else {
				a.done(cmd, "Added alias %s for %q", name, expansion)
			}
			return nil
		},
	}
	set.Flags().BoolVar(&clobber, "clobber", false, "Replace an existing alias of the same name")
	set.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%w; quote an expansion with flags: gt alias set il \"issue list --state all\"", err))
	})
	group.AddCommand(set)
	var all bool
	remove := &cobra.Command{
		Use: "delete [<name>...]", Short: "Delete shortcuts", ValidArgsFunction: a.completeAliases,
		Annotations: textOutput("alias deletion does not return a resource"),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return clierr.Wrap(clierr.Usage, errors.New("name the aliases to delete, or pass --all"))
			}
			if len(args) > 0 && all {
				return clierr.Wrap(clierr.Usage, errors.New("alias names and --all are mutually exclusive"))
			}
			return nil
		},
		Example: "$ gt alias delete il\n$ gt alias delete il review\n$ gt alias delete --all",
		RunE: func(cmd *cobra.Command, args []string) error {
			deleted := map[string]string{}
			err := a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
				names := args
				if all {
					names = slices.Collect(maps.Keys(s.Preferences.Aliases))
				}
				var missing []string
				for _, name := range names {
					expansion, ok := s.Preferences.Aliases[name]
					if !ok {
						missing = append(missing, strconv.Quote(name))
						continue
					}
					deleted[name] = expansion
					delete(s.Preferences.Aliases, name)
				}
				if len(missing) > 0 {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("no alias named %s; gt alias list shows them", strings.Join(missing, ", ")))
				}
				return nil
			})
			if err != nil {
				return err
			}
			if len(deleted) == 0 {
				a.warn(cmd, "No aliases to delete")
			}
			for _, name := range slices.Sorted(maps.Keys(deleted)) {
				a.done(cmd, "Deleted alias %s (%s)", name, deleted[name])
			}
			return nil
		},
	}
	remove.Flags().BoolVar(&all, "all", false, "Delete every alias")
	group.AddCommand(remove)
	group.AddCommand(&cobra.Command{
		Use: "list", Short: "List shortcuts", Args: argsExact(0),
		Example: "$ gt alias list\n# Print the expansion of one alias\n$ gt alias list --jq .il",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.settings()
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, s.Preferences.Aliases)
			}
			return writeTable(a, cmd, slices.Sorted(maps.Keys(s.Preferences.Aliases)), listSpec[string]{
				Columns: []terminal.Column{{Header: "Name"}, {Header: "Expansion", Flex: true}},
				Row: func(name string) []terminal.Cell {
					return []terminal.Cell{{Text: name, Bold: true}, {Text: s.Preferences.Aliases[name]}}
				},
				Empty: "no aliases configured; create one with: gt alias set <name> <expansion>",
			}, listPage{Number: 1})
		},
	})
	return group
}
