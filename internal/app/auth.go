// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// verifyAccount checks that a stored credential still authenticates as its account, and returns the
// scopes of its token when the instance reports them.
func (a *application) verifyAccount(ctx context.Context, policy terminal.Policy, instance, user string, host settings.Host, account settings.Account, token string) ([]string, error) {
	client := a.instanceClient(instance, token, host)
	if account.Tokenless() {
		signer, err := auth.SignerForAccount(account, a.sshPassphrase(ctx, policy, account.SSHKey))
		if err != nil {
			return nil, err
		}
		client.Signer = signer
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	actual, err := services.CurrentAccount(ctx, client)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(actual, user) {
		return nil, errors.New("credential authenticates a different account")
	}
	if account.Tokenless() {
		return nil, nil
	}
	scopes, _ := services.TokenScopes(ctx, client)
	return scopes, nil
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

// instanceTarget selects the instance and account an auth command acts on, without authenticating:
// with no --host, the instance of the working directory's repository when gt has accounts there
// (and the account named by --user or GITEA_CLI_USER, if any), else the one other commands select.
func (a *application) instanceTarget(ctx context.Context) (target, error) {
	if a.options.host == "" {
		if t, err := a.resolveTarget(ctx, true, false); err == nil && len(t.host.Users) > 0 {
			user, _ := a.selectedUser()
			if _, ok := findAccountKey(t.host.Users, user); user == "" || ok {
				return t, nil
			}
		}
	}
	return a.resolveTarget(ctx, false, false)
}

// credentialLabel describes how an account authenticates, such as "token, keyring".
func credentialLabel(account settings.Account) string {
	switch account.AuthMethod {
	case settings.AuthSSH:
		return "SSH certificate"
	case settings.AuthHTTPSign:
		if account.SSHAgent {
			return "SSH key in ssh-agent"
		}
		return "SSH key"
	}
	method, store := "token", "keyring"
	if account.AuthMethod == settings.AuthOAuth {
		method = "OAuth"
	}
	if account.CredentialStore == settings.StoreFile {
		store = "file store"
	}
	return method + ", " + store
}

func (a *application) authCommand() *cobra.Command {
	group := &cobra.Command{Use: "auth", Short: "Authenticate gt and git with Gitea", Long: `Authenticate gt and git with Gitea.

gt keeps the accounts of each instance in hosts.yml and their tokens in the system
keyring, or in a private file when you choose so. One account per instance is active;
--user or GITEA_CLI_USER selects another for a single command. GITEA_TOKEN, together with
GITEA_CLI_HOST or GITEA_INSTANCE_URL, replaces the stored credentials of its instance for the
active account too; it must belong to the account that --user or GITEA_CLI_USER names. See
gt help environment for how commands select the instance.`}
	group.AddCommand(a.loginCommand(), a.logoutCommand(), a.authStatusCommand(), a.authSwitchCommand(), a.authRefreshCommand(), a.authTokenCommand(), a.setupGitCommand(), a.gitCredentialCommand())
	return group
}
