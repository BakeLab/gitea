// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// verifyAccount checks that a stored credential still authenticates as its account.
func (a *application) verifyAccount(ctx context.Context, policy terminal.Policy, instance, user string, host settings.Host, account settings.Account, token string) error {
	client := a.deps.Client(instance, token, host.Insecure)
	if account.Tokenless() {
		signer, err := auth.SignerForAccount(account, a.sshPassphrase(ctx, policy, account.SSHKey))
		if err != nil {
			return err
		}
		client.Signer = signer
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	actual, err := services.CurrentAccount(ctx, client)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, user) {
		return errors.New("credential authenticates a different account")
	}
	return nil
}

// sshPassphrase prompts for the passphrase of an encrypted SSH key, or returns nil when prompting is not allowed.
func (a *application) sshPassphrase(ctx context.Context, policy terminal.Policy, keyPath string) func() ([]byte, error) {
	if a.deps.Prompter == nil || !policy.CanPrompt(a.deps.Streams) {
		return nil
	}
	return func() ([]byte, error) {
		passphrase, err := a.deps.Prompter.SecretInput(ctx, "Passphrase for "+keyPath)
		return []byte(passphrase), err
	}
}

// authStatusRow is one configured account reported by auth status.
type authStatusRow struct {
	Host      string     `json:"host"`
	User      string     `json:"user"`
	Active    bool       `json:"active"`
	Store     string     `json:"credential_store"`
	Available bool       `json:"credential_available"`
	ExpiresAt *time.Time `json:"expires_at"`
	Verified  *bool      `json:"verified"`
	Error     string     `json:"error"`
}

func (a *application) authCommand() *cobra.Command {
	group := &cobra.Command{Use: "auth", Short: "Manage instance accounts and credentials"}
	group.AddCommand(a.gitCredentialCommand(), a.setupGitCommand())
	var withToken bool
	var backend string
	var oauth bool
	var clientID string
	var sshKey string
	var sshAgentPrincipal string
	var sshAgentKey string
	var insecure bool
	login := &cobra.Command{Use: "login", Short: "Authenticate with a token, OAuth, or SSH HTTP signature", Annotations: textOutput("login does not return a resource"), Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		methods := 0
		for _, selected := range []bool{withToken, oauth, sshKey != "", sshAgentPrincipal != "", sshAgentKey != ""} {
			if selected {
				methods++
			}
		}
		if methods != 1 {
			return clierr.Wrap(clierr.Usage, errors.New("select exactly one of --with-token, --oauth, --ssh-key, --ssh-agent-principal, or --ssh-agent-key"))
		}
		if clientID != "" && !oauth {
			return clierr.Wrap(clierr.Usage, errors.New("--client-id requires --oauth"))
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		instance := a.options.host
		if instance == "" {
			instance = a.deps.Getenv("GITEA_CLI_HOST")
		}
		if instance == "" {
			instance = a.deps.Getenv("GITEA_INSTANCE_URL")
		}
		prompted, err := a.require(cmd, &instance, "host")
		if err != nil {
			return err
		}
		instance, err = settings.NormalizeHost(instance)
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		host := snapshot.Hosts.Hosts[instance]
		insecure = insecure || host.Insecure
		manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
		if err != nil {
			return err
		}
		if sshKey != "" || sshAgentPrincipal != "" || sshAgentKey != "" {
			account := settings.Account{}
			switch {
			case sshKey != "":
				account.SSHKey = sshKey
				if _, err := os.Stat(sshKey + "-cert.pub"); err == nil {
					account.AuthMethod = settings.AuthSSH
					account.SSHCertPrincipal = "yes"
				} else if errors.Is(err, os.ErrNotExist) {
					account.AuthMethod = settings.AuthHTTPSign
				} else {
					return err
				}
			case sshAgentPrincipal != "":
				account.AuthMethod = settings.AuthSSH
				account.SSHAgent = true
				account.SSHCertPrincipal = sshAgentPrincipal
			case sshAgentKey != "":
				account.AuthMethod = settings.AuthHTTPSign
				account.SSHAgent = true
				account.SSHKeyFingerprint = sshAgentKey
			}
			signer, err := auth.SignerForAccount(account, a.sshPassphrase(cmd.Context(), a.policy(snapshot), account.SSHKey))
			if err != nil {
				return clierr.Wrap(clierr.Authentication, err)
			}
			client := a.deps.Client(instance, "", insecure)
			client.Signer = signer
			user, err := services.CurrentAccount(cmd.Context(), client)
			if err != nil {
				return err
			}
			if selectedUser, _ := a.selectedUser(); !services.MatchAccount(selectedUser, user) {
				return clierr.Wrap(clierr.Usage, errors.New("authenticated account does not match --user or GITEA_CLI_USER"))
			}
			if prompted {
				if err := a.confirm(cmd, "Save credentials for "+user+" on "+instance); err != nil {
					return err
				}
			}
			return manager.RegisterHTTPSign(instance, user, account, auth.HostOptions{Insecure: insecure})
		}
		if err := oneOf("credential-store", backend, settings.StoreKeyring, settings.StoreFile); err != nil {
			return err
		}
		credential := auth.Credential{}
		method := settings.AuthToken
		if oauth {
			if a.options.noInput {
				return clierr.Wrap(clierr.Usage, errors.New("OAuth authorization requires interaction; use --with-token with --no-input"))
			}
			if clientID == "" {
				clientID = host.OAuthClientID
			}
			if clientID == "" {
				clientID = auth.DefaultClientID
			}
			credential, err = auth.OAuthLogin(cmd.Context(), auth.OAuthOptions{Instance: instance, ClientID: clientID, HTTP: a.deps.Client(instance, "", insecure).HTTP, Authorize: func(_ context.Context, authorizationURL string) error {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Open this URL to authorize gt:", authorizationURL); err != nil {
					return err
				}
				if err := a.openBrowser(cmd, authorizationURL); err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser automatically; open the URL above manually.")
				}
				return nil
			}})
			if err != nil {
				return err
			}
			method = settings.AuthOAuth
		} else {
			data, err := io.ReadAll(io.LimitReader(a.deps.Streams.In, 65537))
			if err != nil {
				return errors.New("cannot read token from stdin")
			}
			credential.AccessToken = strings.TrimSpace(string(data))
			if credential.AccessToken == "" || len(data) > 65536 || strings.ContainsAny(credential.AccessToken, "\r\n") {
				return clierr.Wrap(clierr.Usage, errors.New("stdin must contain one nonempty token"))
			}
		}
		token := credential.AccessToken
		client := a.deps.Client(instance, token, insecure)
		user, err := services.CurrentAccount(cmd.Context(), client)
		if err != nil {
			return err
		}
		if selectedUser, _ := a.selectedUser(); !services.MatchAccount(selectedUser, user) {
			return clierr.Wrap(clierr.Usage, errors.New("token account does not match --user or GITEA_CLI_USER"))
		}
		if prompted {
			if err := a.confirm(cmd, "Save credentials for "+user+" on "+instance); err != nil {
				return err
			}
		}
		return manager.Register(instance, user, backend, method, auth.HostOptions{OAuthClientID: clientID, Insecure: insecure}, credential)
	}}
	login.Flags().BoolVar(&oauth, "oauth", false, "Authorize through a browser using OAuth PKCE")
	login.Flags().StringVar(&clientID, "client-id", "", "OAuth client ID registered on the instance (default: the instance's stored ID, then gt's)")
	login.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification for this instance")
	login.Flags().BoolVar(&withToken, "with-token", false, "Read a personal access token from stdin")
	login.Flags().StringVar(&sshKey, "ssh-key", "", "Authenticate with an SSH private key via HTTP signatures")
	login.Flags().StringVar(&sshAgentPrincipal, "ssh-agent-principal", "", "Authenticate with an ssh-agent certificate principal via HTTP signatures")
	login.Flags().StringVar(&sshAgentKey, "ssh-agent-key", "", "Authenticate with an ssh-agent public key fingerprint via HTTP signatures")
	login.Flags().StringVar(&backend, "credential-store", settings.StoreKeyring, "Credential backend: keyring or explicitly opted-in file")
	group.AddCommand(login)
	var verify bool
	status := &cobra.Command{Use: "status", Short: "Show configured accounts without revealing credentials", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		selectedHost := ""
		if a.options.host != "" {
			var err error
			selectedHost, err = settings.NormalizeHost(a.options.host)
			if err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
		}
		s, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		rows := []authStatusRow{}
		for instance, host := range s.Hosts.Hosts {
			if selectedHost != "" && instance != selectedHost {
				continue
			}
			for user, account := range host.Users {
				if !services.MatchAccount(a.options.user, user) {
					continue
				}
				item := authStatusRow{Host: instance, User: user, Active: host.User == user, Store: account.CredentialStore, Available: account.Tokenless()}
				var credential auth.Credential
				if !item.Available {
					var err error
					credential, err = a.deps.Credentials.Load(instance, user, account.CredentialStore)
					item.Available = err == nil
					if item.Available && account.AuthMethod == settings.AuthOAuth && !credential.ExpiresAt.IsZero() {
						item.ExpiresAt = &credential.ExpiresAt
					}
				}
				if verify {
					err := errors.New("credential unavailable")
					if item.Available {
						err = a.verifyAccount(cmd.Context(), a.policy(s), instance, user, host, account, credential.AccessToken)
					}
					verified := err == nil
					item.Verified = &verified
					if err != nil {
						item.Error = err.Error()
					}
				}
				rows = append(rows, item)
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Host == rows[j].Host {
				return rows[i].User < rows[j].User
			}
			return rows[i].Host < rows[j].Host
		})
		if a.options.json != "" {
			return a.writeJSON(cmd, rows)
		}
		for _, row := range rows {
			line := fmt.Sprintf("%s\t%s\tactive=%t\tcredentials=%t", row.Host, row.User, row.Active, row.Available)
			if row.ExpiresAt != nil {
				line += "\texpires=" + row.ExpiresAt.Format(time.RFC3339)
			}
			if row.Verified != nil {
				line += fmt.Sprintf("\tverified=%t", *row.Verified)
			}
			if row.Error != "" {
				line += "\terror=" + row.Error
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
				return err
			}
		}
		return nil
	}}
	status.Flags().BoolVar(&verify, "verify", false, "Check each listed credential against its instance")
	group.AddCommand(status)
	group.AddCommand(&cobra.Command{Use: "switch <user>", Short: "Select the active account for an instance", Annotations: textOutput("switching accounts does not return a resource"), Args: argsExact(1), RunE: func(_ *cobra.Command, args []string) error {
		if a.options.host == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--host is required"))
		}
		instance, err := settings.NormalizeHost(a.options.host)
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		return a.deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
			host, exists := s.Hosts.Hosts[instance]
			if !exists {
				return clierr.Wrap(clierr.Usage, errors.New("instance is not configured; run gt auth login first"))
			}
			user, exists := matchAccount(host.Users, args[0])
			if !exists {
				return clierr.Wrap(clierr.Usage, errors.New("account is not configured on the selected instance; use gt auth status --host URL to list accounts"))
			}
			host.User = user
			s.Hosts.Hosts[instance] = host
			return nil
		})
	}})
	group.AddCommand(&cobra.Command{Use: "logout", Short: "Remove one account and its credentials", Annotations: textOutput("logout does not return a resource"), Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		selected, err := a.resolveTarget(cmd.Context(), false, false)
		if err != nil {
			return err
		}
		instance, user := selected.Host, selected.User
		s, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		if _, exists := s.Hosts.Hosts[instance].Users[user]; !exists {
			return clierr.Wrap(clierr.Usage, errors.New("account is not configured on the selected instance; use gt auth status --host URL to list accounts"))
		}
		if err := a.confirm(cmd, "Remove account "+user+" on "+instance); err != nil {
			return err
		}
		manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
		if err != nil {
			return err
		}
		return manager.Logout(instance, user)
	}})
	group.AddCommand(&cobra.Command{Use: "refresh", Short: "Refresh the selected OAuth account", Annotations: textOutput("refresh does not return a resource"), Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolveTarget(cmd.Context(), false, false)
		if err != nil {
			return err
		}
		account, exists := t.host.Users[t.User]
		if !exists {
			return clierr.Wrap(clierr.Authentication, errors.New("no selected account on this instance; run gt auth login or gt auth switch"))
		}
		if account.AuthMethod != settings.AuthOAuth {
			return clierr.Wrap(clierr.Usage, errors.New("only OAuth accounts can be refreshed; use gt auth login to replace a token"))
		}
		manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
		if err != nil {
			return err
		}
		_, err = manager.Refresh(cmd.Context(), t.Host, t.User, a.deps.Client(t.Host, "", t.host.Insecure).HTTP, true, a.deps.Now())
		return clierr.Wrap(clierr.Authentication, err)
	}})
	return group
}
