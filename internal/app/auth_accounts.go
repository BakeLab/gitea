// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) authSwitchCommand() *cobra.Command {
	return &cobra.Command{
		Use: "switch [<user>]", Short: "Switch the active account of an instance", Args: usageArgs(cobra.MaximumNArgs(1)),
		Annotations: textOutput("switching accounts does not return a resource"), ValidArgsFunction: a.completeAccounts,
		Long: `Switch the active account of an instance, which commands use unless --user or GITEA_CLI_USER
selects another. With two accounts, gt auth switch toggles between them; with more, it
asks which one on a terminal.`,
		Example: "$ gt auth switch\n$ gt auth switch bob\n$ gt auth switch bob --host gitea.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.positionalAccount(args); err != nil {
				return err
			}
			t, err := a.instanceTarget(cmd.Context())
			if err != nil {
				return err
			}
			users := t.host.Users
			if len(users) == 0 {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("not logged in to %s; run gt auth login --host %s", t.Host, t.Host))
			}
			names := slices.Sorted(maps.Keys(users))
			requested := a.options.user
			var user string
			switch {
			case requested != "":
				matched, ok := findAccountKey(users, requested)
				if !ok {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("account %q is not logged in on %s (accounts: %s)", requested, t.Host, accountNames(users)))
				}
				user = matched
			case len(names) == 1:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is the only account on %s; log in to another with gt auth login --host %s", names[0], t.Host, t.Host))
			case len(names) == 2 && t.host.User != "":
				user = names[0]
				if user == t.host.User {
					user = names[1]
				}
			default:
				if user, err = a.chooseAccount(cmd, t.Host, names, t.host.User); err != nil {
					return err
				}
			}
			if user == t.host.User {
				a.warn(cmd, "%s is already the active account on %s", user, t.Host)
				return nil
			}
			err = a.deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
				host := s.Hosts.Hosts[t.Host]
				if _, exists := host.Users[user]; !exists {
					return fmt.Errorf("account %q was logged out meanwhile", user)
				}
				host.User = user
				s.Hosts.Hosts[t.Host] = host
				return nil
			})
			if err != nil {
				return err
			}
			a.done(cmd, "Switched active account for %s to %s", t.Host, user)
			return nil
		},
	}
}

// positionalAccount treats the account argument of auth switch and auth logout like --user.
func (a *application) positionalAccount(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if a.options.user != "" && !strings.EqualFold(a.options.user, args[0]) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--user %s and the account %s differ", a.options.user, args[0]))
	}
	a.options.user = args[0]
	return nil
}

// chooseAccount asks which of the accounts of instance to use, or explains how to name one.
func (a *application) chooseAccount(cmd *cobra.Command, instance string, names []string, active string) (string, error) {
	chooser, ok := a.chooser()
	if !ok {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("name the account: gt auth switch <user> (accounts on %s: %s)", instance, strings.Join(names, ", ")))
	}
	choices := make([]terminal.Choice, len(names))
	for i, name := range names {
		choices[i] = terminal.Choice{Label: name, Value: name}
		if name == active {
			choices[i].Label += " (active)"
		}
	}
	return chooser.Choose(cmd.Context(), "Which account of "+instance+" should be active?", choices, active)
}

func (a *application) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use: "logout [<user>]", Short: "Log out of an account", Args: usageArgs(cobra.MaximumNArgs(1)), ValidArgsFunction: a.completeAccounts,
		Annotations: map[string]string{"gt.text": "logout does not return a resource", "gt.confirm": "true"},
		Long: `Log out of an account: gt forgets the account and deletes its stored credential. Git's
other credential helpers are asked to forget what they stored for the account on the
instance, such as a copy of its token that a keychain kept after a push. When the instance
of default_host has no account and no settings left, default_host moves to the only other
instance with accounts, or is unset.`,
		Example: "$ gt auth logout\n$ gt auth logout bob --host gitea.com\n# Without confirmation\n$ gt auth logout alice --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.positionalAccount(args); err != nil {
				return err
			}
			t, err := a.instanceTarget(cmd.Context())
			if err != nil {
				return err
			}
			_, exists := t.host.Users[t.User]
			switch {
			case len(t.host.Users) == 0:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("not logged in to %s", t.Host))
			case t.User == "":
				return clierr.Wrap(clierr.Usage, fmt.Errorf("no active account on %s; name the account: gt auth logout <user> (accounts: %s)", t.Host, accountNames(t.host.Users)))
			case !exists:
				return clierr.Wrap(clierr.Usage, t.accountError(t.User))
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Log out %s from %s?", t.User, t.Host)}); err != nil {
				return err
			}
			manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
			if err != nil {
				return err
			}
			removed, err := manager.Logout(t.Host, t.User)
			if err != nil {
				return err
			}
			a.done(cmd, "Logged out of %s (%s)", t.Host, t.User)
			a.replaceRemovedDefault(cmd, t.Host)
			// Helpers erase only the copies that hold the removed token, not the user's own credentials.
			if err := git.RejectCredentials(cmd.Context(), a.gitProbe(), a.options.cwd, t.Host, t.User, removed.AccessToken); err != nil {
				a.warn(cmd, "Could not ask git's credential helpers to forget %s on %s", t.User, t.Host)
			}
			if s, err := a.settings(); err == nil {
				if active := s.Hosts.Hosts[t.Host].User; active != "" && active != t.host.User {
					a.done(cmd, "Switched active account for %s to %s", t.Host, active)
				}
			}
			return nil
		},
	}
}

func (a *application) authRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use: "refresh", Short: "Refresh an OAuth token", Args: argsExact(0),
		Annotations: textOutput("refresh does not return a resource"),
		Long: `Refresh the OAuth token of the active account, or of --user, now. gt refreshes expiring
OAuth tokens on its own; tokens pasted with --with-token cannot be refreshed and are
replaced by logging in again.`,
		Example: "$ gt auth refresh\n$ gt auth refresh --host gitea.com --user alice",
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := a.instanceTarget(cmd.Context())
			if err != nil {
				return err
			}
			account, exists := t.host.Users[t.User]
			if !exists {
				return clierr.Wrap(clierr.Authentication, t.accountError(t.User))
			}
			if account.AuthMethod != settings.AuthOAuth {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("only OAuth accounts can be refreshed; replace the token of %s with: gt auth login --host %s --with-token", t.User, t.Host))
			}
			manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
			if err != nil {
				return err
			}
			if _, err := manager.Refresh(cmd.Context(), t.Host, t.User, a.instanceClient(t.Host, "", t.host).HTTP, true, a.deps.Now()); err != nil {
				return clierr.Wrap(clierr.Authentication, err)
			}
			a.done(cmd, "Refreshed the OAuth token of %s on %s", t.User, t.Host)
			return nil
		},
	}
}

func (a *application) authTokenCommand() *cobra.Command {
	return &cobra.Command{
		Use: "token", Short: "Print the access token of an account", Args: argsExact(0),
		Annotations: textOutput("auth token prints the token"),
		Long: `Print the access token that gt uses for the active account, or for --user, so that
scripts can call the API themselves. The GITEA_TOKEN of the instance takes precedence, as
it does for other commands, and an expiring OAuth token is refreshed first. Accounts that
sign requests with an SSH key have no token.`,
		Example: `$ gt auth token
$ curl -H "Authorization: token $(gt auth token)" https://gitea.com/api/v1/user
$ gt auth token --host gitea.com --user alice`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := a.instanceTarget(cmd.Context())
			if err != nil {
				return err
			}
			s, err := a.settings()
			if err != nil {
				return err
			}
			token := a.deps.Getenv("GITEA_TOKEN")
			if instance, err := settings.ResolveHost(a.tokenInstance(), s.Hosts.Hosts); err != nil || instance != t.Host {
				token = ""
			}
			if token != "" && t.explicitUser() {
				// Like connect, print the environment token only for the account --user or GITEA_CLI_USER names.
				actual, err := services.CurrentAccount(cmd.Context(), a.instanceClient(t.Host, token, t.host))
				if err != nil {
					return err
				}
				if !services.MatchAccount(t.User, actual) {
					return clierr.Wrap(clierr.Authentication, errEnvironmentAccount)
				}
			}
			if token == "" {
				account, exists := t.host.Users[t.User]
				switch {
				case !exists:
					return clierr.Wrap(clierr.Authentication, t.accountError(t.User))
				case account.Tokenless():
					return fmt.Errorf("%s on %s signs requests with an SSH key and has no token", t.User, t.Host)
				}
				manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
				if err != nil {
					return err
				}
				credential, err := manager.GitCredential(cmd.Context(), t.Host, t.User, a.instanceClient(t.Host, "", t.host).HTTP, a.deps.Now())
				if err != nil {
					return clierr.Wrap(clierr.Authentication, err)
				}
				token = credential.AccessToken
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
}

// replaceRemovedDefault moves default_host off instance after logout removed it from hosts.yml: to
// the only other instance with accounts, or nowhere.
func (a *application) replaceRemovedDefault(cmd *cobra.Command, instance string) {
	replacement := func(s settings.Snapshot) (string, bool) {
		if _, kept := s.Hosts.Hosts[instance]; kept || s.Preferences.DefaultHost != instance {
			return "", false
		}
		var candidates []string
		for name, host := range s.Hosts.Hosts {
			if len(host.Users) > 0 {
				candidates = append(candidates, name)
			}
		}
		if len(candidates) == 1 {
			return candidates[0], true
		}
		return "", true
	}
	if s, err := a.deps.Settings.Load(); err != nil {
		return
	} else if _, changed := replacement(s); !changed {
		return
	}
	var next string
	var changed bool
	err := a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
		next, changed = replacement(*s)
		if changed {
			s.Preferences.DefaultHost = next
		}
		return nil
	})
	switch {
	case err != nil:
		a.warn(cmd, "Could not update default_host, which names the removed instance %s: %v", instance, err)
	case !changed:
	case next != "":
		a.done(cmd, "Set default_host to %s", next)
	default:
		a.done(cmd, "Unset default_host")
	}
}
