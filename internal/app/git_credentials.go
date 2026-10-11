// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gitea.dev/tea/internal/auth"
	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/git"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/settings"
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
		snapshot, err := a.deps.Settings.Load()
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
			return clierr.Wrap(clierr.Authentication, errors.New("stored Git credential contains invalid protocol characters; run tea auth login"))
		}
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "username=%s\npassword=%s\n\n", user, credential.AccessToken)
		return err
	}}
}

func (a *application) setupGitCommand() *cobra.Command {
	var local bool
	command := &cobra.Command{Use: "setup-git", Short: "Explicitly register tea as a scoped Git credential helper", Annotations: textOutput("setup-git configures Git"), Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolveTarget(cmd.Context(), false, false)
		if err != nil {
			return err
		}
		executable, configDir, err := a.credentialHelper()
		if err != nil {
			return err
		}
		pinned, _ := a.selectedUser()
		return git.SetupCredentials(cmd.Context(), a.deps.Git, git.CredentialSetup{Directory: a.options.cwd, Local: local, Instance: target.Host, Executable: executable, User: pinned, ConfigDirectory: configDir})
	}}
	command.Flags().BoolVar(&local, "local", false, "Configure only the current Git repository instead of global Git configuration")
	return command
}

// credentialHelper locates tea's executable and absolute configuration directory for Git's credential helper.
func (a *application) credentialHelper() (executable, configDir string, err error) {
	executable, err = a.deps.Executable()
	if err != nil {
		return "", "", errors.New("cannot locate tea's executable for Git authentication")
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
