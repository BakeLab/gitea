// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
)

func (a *application) gitCredentialCommand() *cobra.Command {
	return &cobra.Command{Use: "git-credential <get|store|erase>", Hidden: true, Annotations: textOutput("Git credentials use Git's protocol"), Short: "Serve Git's credential helper protocol", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "get" && args[0] != "store" && args[0] != "erase" {
			return clierr.Wrap(clierr.Usage, errors.New("Git credential operation must be get, store, or erase"))
		}
		request, err := auth.ReadGitRequest(cmd.InOrStdin())
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		if args[0] != "get" {
			return nil
		}
		snapshot, err := a.settings()
		if err != nil {
			return err
		}
		if snapshot.Hosts.Hosts, err = a.environmentHosts(snapshot.Hosts.Hosts); err != nil {
			return err
		}
		environmentHost := a.tokenInstance()
		instance, err := auth.MatchGitInstance(request, snapshot.Hosts.Hosts, a.options.host, environmentHost)
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		if instance == "" {
			return nil
		}
		host := snapshot.Hosts.Hosts[instance]
		user, _ := a.selectedUser()
		if user == "" {
			user = request.Username
		}
		if user == "" {
			user = host.User
		}
		if request.Username != "" && user != "" && !strings.EqualFold(request.Username, user) {
			return nil
		}
		// Account keys retain the canonical login returned at authentication time.
		if configured, ok := matchAccount(host.Users, user); ok {
			user = configured
		}
		credential := auth.Credential{}
		token := a.deps.Getenv("GITEA_TOKEN")
		environmentMatches := false
		if environmentHost != "" {
			normalized, err := settings.NormalizeHost(environmentHost)
			environmentMatches = err == nil && normalized == instance
		}
		if token != "" && environmentMatches {
			actual, err := services.CurrentAccount(cmd.Context(), a.deps.Client(instance, token, host.Insecure))
			if err != nil {
				return err
			}
			if !services.MatchAccount(user, actual) {
				return nil
			}
			user, credential.AccessToken = actual, token
		} else {
			if user == "" {
				return nil
			}
			if _, exists := host.Users[user]; !exists {
				return nil
			}
			manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
			if err != nil {
				return err
			}
			credential, err = manager.GitCredential(cmd.Context(), instance, user, a.deps.Client(instance, "", host.Insecure).HTTP, a.deps.Now())
			if err != nil {
				return clierr.Wrap(clierr.Authentication, err)
			}
		}
		if strings.ContainsAny(user+credential.AccessToken, "\x00\r\n") {
			return clierr.Wrap(clierr.Authentication, errors.New("stored Git credential contains invalid protocol characters; run gt auth login"))
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "username=%s\npassword=%s\n\n", user, credential.AccessToken)
		return err
	}}
}

func (a *application) setupGitCommand() *cobra.Command {
	var local, keepHelpers bool
	command := &cobra.Command{
		Use: "setup-git", Short: "Configure git to use gt for credentials", Args: argsExact(0),
		Annotations: textOutput("setup-git configures Git"),
		Long: `Configure git to ask gt for the credentials of an instance, so that git push and fetch
over HTTPS use the active account, or the account of --user. gt's own clone and checkout
commands do not need this.

Other credential helpers, such as a system keychain, are turned off for the instance:
otherwise they answer first and keep a copy of gt's token. --keep-helpers keeps them.`,
		Example: `$ gt auth setup-git
$ gt auth setup-git --host gitea.com
# Only for the current repository, always as alice
$ gt auth setup-git --local --user alice`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := a.instanceTarget(cmd.Context())
			if err != nil {
				return err
			}
			return a.setupGit(cmd, t.Host, local, keepHelpers)
		},
	}
	command.Flags().BoolVar(&local, "local", false, "Configure only the current Git repository instead of global Git configuration")
	command.Flags().BoolVar(&keepHelpers, "keep-helpers", false, "Keep other credential helpers for the instance; they are asked first and store gt's token")
	return command
}

// setupGit configures Git to ask gt for the credentials of instance, globally or in the repository.
func (a *application) setupGit(cmd *cobra.Command, instance string, local, keepHelpers bool) error {
	executable, configDir, err := a.credentialHelper()
	if err != nil {
		return err
	}
	pinned, _ := a.selectedUser()
	if err := git.SetupCredentials(cmd.Context(), a.deps.Git, git.CredentialSetup{Directory: a.options.cwd, Local: local, KeepHelpers: keepHelpers, Instance: instance, Executable: executable, User: pinned, ConfigDirectory: configDir}); err != nil {
		return err
	}
	scope := "global git configuration"
	if local {
		scope = "this repository"
	}
	a.done(cmd, "Configured git to use gt for %s (%s)", instance, scope)
	return nil
}

// credentialHelper locates gt's executable and absolute configuration directory for Git's credential helper.
func (a *application) credentialHelper() (executable, configDir string, err error) {
	executable, err = a.deps.Executable()
	if err != nil {
		return "", "", errors.New("cannot locate gt's executable for Git authentication")
	}
	configDir, err = filepath.Abs(a.deps.Settings.Dir)
	if err != nil {
		return "", "", errors.New("cannot resolve configuration directory for Git authentication")
	}
	return executable, configDir, nil
}

func (a *application) gitCredentialArguments(instance, user string) ([]string, error) {
	executable, configDir, err := a.credentialHelper()
	if err != nil {
		return nil, err
	}
	return git.CredentialArguments(instance, executable, user, configDir)
}

func (a *application) gitTransportArguments(target target, ssh bool) ([]string, error) {
	if !ssh {
		return a.gitCredentialArguments(target.Host, target.User)
	}
	account := target.host.Users[target.User]
	return git.SSHArguments(account.SSHKey, account.SSHAgent)
}
