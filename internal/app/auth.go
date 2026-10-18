// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// verifyAccount checks that a stored credential still authenticates as its account, and returns the
// scopes of its token when the instance reports them.
func (a *application) verifyAccount(ctx context.Context, policy terminal.Policy, instance, user string, host settings.Host, account settings.Account, token string) ([]string, error) {
	client := a.deps.Client(instance, token, host.Insecure)
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
// with no --host, the instance of the working directory's repository when gt has accounts there,
// else the one other commands select.
func (a *application) instanceTarget(ctx context.Context) (target, error) {
	if a.options.host == "" {
		if t, err := a.resolveTarget(ctx, true, false); err == nil && len(t.host.Users) > 0 {
			return t, nil
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
GITEA_INSTANCE_URL or GITEA_CLI_HOST, replaces the stored credentials of its instance. See
gt help environment for how commands select the instance.`}
	group.AddCommand(a.loginCommand(), a.logoutCommand(), a.authStatusCommand(), a.authSwitchCommand(), a.authRefreshCommand(), a.authTokenCommand(), a.setupGitCommand(), a.gitCredentialCommand())
	return group
}

// oauthScopes are the token scopes that gt auth login --scopes accepts.
var oauthScopes = func() []string {
	scopes := []string{"all"}
	for _, area := range []string{"activitypub", "admin", "issue", "misc", "notification", "organization", "package", "repository", "user"} {
		scopes = append(scopes, "read:"+area, "write:"+area)
	}
	return scopes
}()

func (a *application) loginCommand() *cobra.Command {
	var withToken, oauth, insecure bool
	var backend, clientID, sshKey, sshAgentPrincipal, sshAgentKey string
	var scopes []string
	login := &cobra.Command{
		Use: "login", Short: "Log in to a Gitea instance", Args: argsExact(0),
		Annotations: map[string]string{"gt.text": "login does not return a resource", "gt.confirm": "true"},
		Long: `Log in to a Gitea instance. On a terminal without a method flag, gt asks for the instance,
how to log in, where to keep the token and whether git should use gt for the instance.

Create a token on the instance under Settings > Applications. Logging in to an account
that gt already has replaces its stored credential after a confirmation (or with --yes).`,
		Example: `$ gt auth login
$ gt auth login --host gitea.com --with-token < token.txt
$ gt auth login --host gitea.com --oauth --scopes read:repository,read:user`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected := 0
			for _, chosen := range []bool{withToken, oauth, sshKey != "", sshAgentPrincipal != "", sshAgentKey != ""} {
				if chosen {
					selected++
				}
			}
			switch {
			case selected > 1:
				return clierr.Wrap(clierr.Usage, errors.New("select one of --with-token, --oauth, --ssh-key, --ssh-agent-principal or --ssh-agent-key"))
			case selected == 1 && !oauth && clientID != "":
				return clierr.Wrap(clierr.Usage, errors.New("--client-id requires --oauth"))
			case selected == 1 && !oauth && cmd.Flags().Changed("scopes"):
				return clierr.Wrap(clierr.Usage, errors.New("--scopes requires --oauth"))
			}
			snapshot, err := a.settings()
			if err != nil {
				return err
			}
			form, hasForm := a.deps.Prompter.(terminal.LoginForm)
			interactive := selected == 0
			if interactive && (!hasForm || !a.policy(snapshot).CanPrompt(a.deps.Streams)) {
				return clierr.Wrap(clierr.Usage, errors.New("select how to log in: --with-token, --oauth or --ssh-key; on a terminal, gt auth login asks"))
			}
			resolveHost := func(raw string) (string, error) { return settings.ResolveHost(raw, snapshot.Hosts.Hosts) }
			instance := cmp.Or(a.options.host, a.deps.Getenv("GITEA_CLI_HOST"), a.deps.Getenv("GITEA_INSTANCE_URL"))
			if instance != "" {
				if instance, err = resolveHost(instance); err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
			}
			answers := terminal.LoginAnswers{Host: instance, Store: backend, SSHKey: sshKey}
			switch {
			case oauth:
				answers.Method = terminal.LoginOAuth
			case selected == 1 && !withToken:
				answers.Method = terminal.LoginSSHKey
			default:
				answers.Method = terminal.LoginToken
			}
			if interactive {
				if answers.Host == "" {
					answers.Host = a.suggestedHost(cmd.Context())
				}
				answers.SSHKey, answers.SetupGit = defaultSSHKey(), true
				questions := terminal.LoginQuestions{
					Host: instance == "", Method: true, Store: !cmd.Flags().Changed("credential-store"), SetupGit: true,
					ResolveHost: resolveHost,
					TokenHint:   func(host string) string { return "Create one at " + host + "/user/settings/applications" },
				}
				if err := form.Login(cmd.Context(), questions, &answers); err != nil {
					return err
				}
			} else if answers.Host == "" {
				if _, err := a.requireInput(cmd, &answers.Host, "host", "Gitea instance", func(raw string) error { _, err := resolveHost(raw); return err }); err != nil {
					return err
				}
				if answers.Host, err = resolveHost(answers.Host); err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
			}
			instance = answers.Host
			host := snapshot.Hosts.Hosts[instance]
			insecure = insecure || host.Insecure
			replacing := false
			if selectedUser, _ := a.selectedUser(); selectedUser != "" {
				if replacing, err = a.confirmReplace(cmd, instance, selectedUser); err != nil {
					return err
				}
			}
			manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
			if err != nil {
				return err
			}
			if answers.Method == terminal.LoginSSHKey {
				account, user, err := a.signedLogin(cmd, snapshot, instance, insecure, settings.Account{SSHKey: answers.SSHKey}, sshAgentPrincipal, sshAgentKey)
				if err != nil {
					return err
				}
				if !replacing {
					if _, err := a.confirmReplace(cmd, instance, user); err != nil {
						return err
					}
				}
				if err := manager.RegisterHTTPSign(instance, user, account, auth.HostOptions{Insecure: insecure, Replace: true}); err != nil {
					return err
				}
				a.done(cmd, "Logged in to %s as %s (%s)", instance, user, credentialLabel(account))
				return nil
			}
			credential := auth.Credential{AccessToken: answers.Token}
			method := settings.AuthToken
			switch {
			case answers.Method == terminal.LoginOAuth:
				if a.options.noInput {
					return clierr.Wrap(clierr.Usage, errors.New("OAuth authorization requires interaction; use --with-token with --no-input"))
				}
				clientID = cmp.Or(clientID, host.OAuthClientID, auth.DefaultClientID)
				credential, err = auth.OAuthLogin(cmd.Context(), auth.OAuthOptions{Instance: instance, ClientID: clientID, Scopes: scopes, HTTP: a.deps.Client(instance, "", insecure).HTTP, Authorize: func(_ context.Context, authorizationURL string) error {
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
			case interactive:
			case a.deps.Streams.InTTY:
				if !a.policy(snapshot).CanPrompt(a.deps.Streams) {
					return clierr.Wrap(clierr.Usage, errors.New("--with-token reads the token from stdin; pipe it in, for example: gt auth login --with-token < token.txt"))
				}
				token, err := a.deps.Prompter.SecretInput(cmd.Context(), "Paste an access token")
				if err != nil {
					return err
				}
				if credential.AccessToken = strings.TrimSpace(token); credential.AccessToken == "" {
					return clierr.Wrap(clierr.Usage, errors.New("access token cannot be empty"))
				}
			default:
				data, err := io.ReadAll(io.LimitReader(a.deps.Streams.In, 65537))
				if err != nil {
					return errors.New("cannot read token from stdin")
				}
				credential.AccessToken = strings.TrimSpace(string(data))
				if credential.AccessToken == "" || len(data) > 65536 || strings.ContainsAny(credential.AccessToken, "\r\n") {
					return clierr.Wrap(clierr.Usage, errors.New("stdin must contain one nonempty token"))
				}
			}
			user, err := services.CurrentAccount(cmd.Context(), a.deps.Client(instance, credential.AccessToken, insecure))
			if err != nil {
				return err
			}
			if selectedUser, _ := a.selectedUser(); !services.MatchAccount(selectedUser, user) {
				return clierr.Wrap(clierr.Usage, errors.New("token account does not match --user or GITEA_CLI_USER"))
			}
			if !replacing {
				if _, err := a.confirmReplace(cmd, instance, user); err != nil {
					return err
				}
			}
			account := settings.Account{AuthMethod: method, CredentialStore: answers.Store}
			if err := manager.Register(instance, user, answers.Store, method, auth.HostOptions{OAuthClientID: clientID, Insecure: insecure, Replace: true}, credential); err != nil {
				return err
			}
			a.done(cmd, "Logged in to %s as %s (%s)", instance, user, credentialLabel(account))
			if interactive && answers.SetupGit {
				if err := a.setupGit(cmd, instance, false, false); err != nil {
					a.warn(cmd, "Could not configure git: %v; run gt auth setup-git", err)
				}
			}
			return nil
		},
	}
	login.Flags().BoolVar(&oauth, "oauth", false, "Authorize in a web browser with OAuth")
	login.Flags().StringVar(&clientID, "client-id", "", "OAuth application `ID` registered on the instance (default: the instance's stored ID, then gt's)")
	login.Flags().StringSliceVar(&scopes, "scopes", nil, "OAuth token `scopes`, such as read:repository (default: write access to everything but administration)")
	login.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification for this instance")
	login.Flags().BoolVar(&withToken, "with-token", false, "Read an access token from stdin, or ask for it on a terminal")
	login.Flags().StringVar(&sshKey, "ssh-key", "", "Sign requests with the SSH private key in `file`")
	login.Flags().StringVar(&sshAgentPrincipal, "ssh-agent-principal", "", "Sign requests with the ssh-agent certificate of `principal`")
	login.Flags().StringVar(&sshAgentKey, "ssh-agent-key", "", "Sign requests with the ssh-agent key of `fingerprint`")
	enumFlag(login, &backend, "credential-store", "", settings.StoreKeyring, "Where to keep the token", settings.StoreKeyring, settings.StoreFile)
	enumValues(login, "scopes", oauthScopes...)
	flagAliases(login, map[string]string{"web": "oauth"})
	return login
}

// confirmReplace asks before a login replaces the stored credential of an account that gt already
// has, and reports whether it does.
func (a *application) confirmReplace(cmd *cobra.Command, instance, user string) (bool, error) {
	snapshot, err := a.settings()
	if err != nil {
		return false, err
	}
	configured, exists := matchAccount(snapshot.Hosts.Hosts[instance].Users, user)
	if !exists {
		return false, nil
	}
	return true, a.confirmAction(cmd, terminal.Confirmation{
		Prompt:     fmt.Sprintf("%s is already logged in to %s. Replace the stored credential?", configured, instance),
		DefaultYes: true,
		NonTTY:     "replace the stored credential of " + configured + " on " + instance,
	})
}

// signedLogin authenticates with an SSH key or ssh-agent and returns the account to store and its login.
func (a *application) signedLogin(cmd *cobra.Command, snapshot settings.Snapshot, instance string, insecure bool, account settings.Account, agentPrincipal, agentKey string) (settings.Account, string, error) {
	switch {
	case agentPrincipal != "":
		account = settings.Account{AuthMethod: settings.AuthSSH, SSHAgent: true, SSHCertPrincipal: agentPrincipal}
	case agentKey != "":
		account = settings.Account{AuthMethod: settings.AuthHTTPSign, SSHAgent: true, SSHKeyFingerprint: agentKey}
	default:
		if _, err := os.Stat(account.SSHKey + "-cert.pub"); err == nil {
			account.AuthMethod, account.SSHCertPrincipal = settings.AuthSSH, "yes"
		} else if errors.Is(err, os.ErrNotExist) {
			account.AuthMethod = settings.AuthHTTPSign
		} else {
			return account, "", err
		}
	}
	signer, err := auth.SignerForAccount(account, a.sshPassphrase(cmd.Context(), a.policy(snapshot), account.SSHKey))
	if err != nil {
		return account, "", clierr.Wrap(clierr.Authentication, err)
	}
	client := a.deps.Client(instance, "", insecure)
	client.Signer = signer
	user, err := services.CurrentAccount(cmd.Context(), client)
	if err != nil {
		return account, "", err
	}
	if selectedUser, _ := a.selectedUser(); !services.MatchAccount(selectedUser, user) {
		return account, "", clierr.Wrap(clierr.Usage, errors.New("authenticated account does not match --user or GITEA_CLI_USER"))
	}
	return account, user, nil
}

// suggestedHost returns the instance that other commands would select, to prefill a login.
func (a *application) suggestedHost(ctx context.Context) string {
	for _, repository := range []bool{true, false} {
		if t, err := a.resolveTarget(ctx, repository, false); err == nil && t.Host != "" {
			return t.Host
		}
	}
	return ""
}

// defaultSSHKey returns the first of the usual SSH private keys that exists.
func defaultSSHKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// authStatusRow is one account, or the environment token, that auth status reports.
type authStatusRow struct {
	Host      string     `json:"host"`
	User      string     `json:"user"`
	Active    bool       `json:"active"`
	Method    string     `json:"method"`
	Store     string     `json:"credential_store"`
	Available bool       `json:"credential_available"`
	ExpiresAt *time.Time `json:"expires_at"`
	Verified  *bool      `json:"verified"`
	Scopes    []string   `json:"scopes,omitempty"`
	Error     string     `json:"error"`
	account   settings.Account
}

// usable reports whether the row's credential can authenticate, as far as gt knows.
func (r authStatusRow) usable() bool {
	return r.Available && (r.Verified == nil || *r.Verified)
}

// environment reports whether the row is the GITEA_TOKEN of the environment.
func (r authStatusRow) environment() bool {
	return r.Store == "environment"
}

func (a *application) authStatusCommand() *cobra.Command {
	var verify bool
	status := &cobra.Command{
		Use: "status", Short: "Show the accounts gt can use", Args: argsExact(0),
		Long: `Show the accounts gt can use on each instance, the active one, and how they
authenticate. Fails with exit code 4 when there is no account, or when a credential is
missing or rejected.`,
		Example: `$ gt auth status
# Check the credentials against their instances
$ gt auth status --verify
$ gt auth status --host gitea.com --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.settings()
			if err != nil {
				return err
			}
			selectedHost := ""
			if a.options.host != "" {
				if selectedHost, err = settings.ResolveHost(a.options.host, s.Hosts.Hosts); err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
			}
			rows := []authStatusRow{}
			for instance, host := range s.Hosts.Hosts {
				if selectedHost != "" && instance != selectedHost {
					continue
				}
				for user := range host.Users {
					if !services.MatchAccount(a.options.user, user) {
						continue
					}
					rows = append(rows, a.accountStatus(cmd.Context(), s, instance, user, verify))
				}
			}
			if row, ok := a.environmentStatus(cmd.Context(), s, verify); ok && (selectedHost == "" || row.Host == selectedHost) && services.MatchAccount(a.options.user, cmp.Or(row.User, a.options.user)) {
				rows = append(rows, row)
			}
			slices.SortFunc(rows, func(x, y authStatusRow) int {
				return cmp.Or(strings.Compare(x.Host, y.Host), cmp.Compare(boolOrder(y.environment()), boolOrder(x.environment())), strings.Compare(x.User, y.User))
			})
			if a.options.json != "" {
				return a.writeJSON(cmd, rows)
			}
			if len(rows) == 0 {
				if selectedHost != "" {
					return clierr.Wrap(clierr.Authentication, fmt.Errorf("not logged in to %s; run gt auth login --host %s", selectedHost, selectedHost))
				}
				return clierr.Wrap(clierr.Authentication, errors.New("not logged in to any Gitea instance; run gt auth login"))
			}
			if err := a.writeAuthStatus(cmd, rows); err != nil {
				return err
			}
			if failed := len(slices.DeleteFunc(slices.Clone(rows), authStatusRow.usable)); failed > 0 {
				return clierr.Wrap(clierr.Authentication, fmt.Errorf("%d of %d credentials cannot authenticate; log in again with gt auth login", failed, len(rows)))
			}
			return nil
		},
	}
	status.Flags().BoolVar(&verify, "verify", false, "Check each credential against its instance")
	return status
}

// accountStatus reports a stored account without revealing its credential.
func (a *application) accountStatus(ctx context.Context, s settings.Snapshot, instance, user string, verify bool) authStatusRow {
	host := s.Hosts.Hosts[instance]
	account := host.Users[user]
	row := authStatusRow{Host: instance, User: user, Active: host.User == user, Method: account.AuthMethod, Store: account.CredentialStore, Available: account.Tokenless(), account: account}
	var credential auth.Credential
	if !row.Available {
		var err error
		if credential, err = a.deps.Credentials.Load(instance, user, account.CredentialStore); err != nil {
			row.Error = err.Error()
		}
		row.Available = err == nil
		if row.Available && account.AuthMethod == settings.AuthOAuth && !credential.ExpiresAt.IsZero() {
			row.ExpiresAt = &credential.ExpiresAt
		}
	}
	if verify {
		err := errors.New("credential unavailable")
		if row.Available {
			row.Scopes, err = a.verifyAccount(ctx, a.policy(s), instance, user, host, account, credential.AccessToken)
		}
		verified := err == nil
		row.Verified = &verified
		if err != nil {
			row.Error = err.Error()
		}
	}
	return row
}

// environmentStatus reports the GITEA_TOKEN of the environment, when one is set.
func (a *application) environmentStatus(ctx context.Context, s settings.Snapshot, verify bool) (authStatusRow, bool) {
	token := a.deps.Getenv("GITEA_TOKEN")
	if token == "" {
		return authStatusRow{}, false
	}
	row := authStatusRow{Method: settings.AuthToken, Store: "environment", Active: true, Available: true}
	instance, err := settings.ResolveHost(a.tokenInstance(), s.Hosts.Hosts)
	if a.tokenInstance() == "" || err != nil {
		row.Available, row.Error = false, "GITEA_TOKEN needs GITEA_INSTANCE_URL or GITEA_CLI_HOST naming its instance"
		return row, true
	}
	row.Host = instance
	if verify {
		client := a.deps.Client(instance, token, s.Hosts.Hosts[instance].Insecure)
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		user, err := services.CurrentAccount(ctx, client)
		verified := err == nil
		row.User, row.Verified = user, &verified
		if err != nil {
			row.Error = err.Error()
		} else {
			row.Scopes, _ = services.TokenScopes(ctx, client)
		}
	}
	return row, true
}

// writeAuthStatus shows the accounts by instance on a terminal, and as tab-separated rows of host,
// user, active, method, store, credential availability, verification, expiry and error otherwise.
func (a *application) writeAuthStatus(cmd *cobra.Command, rows []authStatusRow) error {
	d := a.display()
	if !d.TTY {
		return writeTable(a, cmd, rows, listSpec[authStatusRow]{Row: func(row authStatusRow) []terminal.Cell {
			verified, expires := "", terminal.Cell{}
			if row.Verified != nil {
				verified = fmt.Sprint(*row.Verified)
			}
			if row.ExpiresAt != nil {
				expires.Time = *row.ExpiresAt
			}
			return []terminal.Cell{{Text: row.Host}, {Text: row.User}, {Text: fmt.Sprint(row.Active)}, {Text: row.Method}, {Text: row.Store}, {Text: fmt.Sprint(row.Available)}, {Text: verified}, expires, {Text: row.Error}}
		}}, listPage{Number: 1})
	}
	byHost := map[string][]authStatusRow{}
	for _, row := range rows {
		byHost[row.Host] = append(byHost[row.Host], row)
	}
	var b strings.Builder
	for _, instance := range slices.Sorted(maps.Keys(byHost)) {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(terminal.Emphasis(cmp.Or(instance, "unknown instance"), d.Color) + "\n")
		stored := false
		for _, row := range byHost[instance] {
			icon := terminal.Colorize("green", "✓", d.Color)
			if !row.usable() {
				icon = terminal.Colorize("red", "X", d.Color)
			}
			b.WriteString("  " + icon + " " + statusLine(row, d) + "\n")
			stored = stored || (!row.environment() && !row.account.Tokenless())
		}
		if stored {
			line := "Git credential helper: not configured; run gt auth setup-git"
			if scope := git.HelperScope(cmd.Context(), a.gitProbe(), a.options.cwd, instance); scope != "" {
				line = "Git credential helper: configured (" + scope + ")"
			}
			b.WriteString("  " + terminal.Colorize("muted", "- "+line, d.Color) + "\n")
		}
	}
	return a.writeHuman(cmd, b.String())
}

// statusLine describes one account of auth status: "Logged in as alice (token, keyring) · active".
func statusLine(row authStatusRow, d terminal.Display) string {
	muted := func(text string) string { return terminal.Colorize("muted", text, d.Color) }
	var line string
	if row.environment() {
		line = "Using GITEA_TOKEN from the environment"
		if row.User != "" {
			line += " as " + terminal.Emphasis(terminal.SafeLine(row.User), d.Color)
		}
	} else {
		line = "Logged in as " + terminal.Emphasis(terminal.SafeLine(row.User), d.Color) + " " + muted("("+credentialLabel(row.account)+")")
	}
	var notes []string
	if row.Active && !row.environment() {
		notes = append(notes, "active")
	}
	if row.ExpiresAt != nil {
		if d.Now.Before(*row.ExpiresAt) {
			notes = append(notes, "token expires "+terminal.RelativeTime(d.Now, *row.ExpiresAt))
		} else {
			notes = append(notes, "token expired; gt refreshes it when used")
		}
	}
	if row.Verified != nil && *row.Verified {
		note := "verified"
		if len(row.Scopes) > 0 {
			note += ", scopes: " + terminal.SafeLine(strings.Join(row.Scopes, ", "))
		}
		notes = append(notes, note)
	}
	if row.Error != "" {
		notes = append(notes, terminal.Colorize("red", terminal.SafeLine(row.Error), d.Color))
	}
	if len(notes) > 0 {
		line += " · " + strings.Join(notes, " · ")
	}
	return line
}

func (a *application) authSwitchCommand() *cobra.Command {
	return &cobra.Command{
		Use: "switch [<user>]", Short: "Switch the active account of an instance", Args: usageArgs(cobra.MaximumNArgs(1)),
		Annotations: textOutput("switching accounts does not return a resource"), ValidArgsFunction: a.completeAccounts,
		Long: `Switch the active account of an instance, which commands use unless --user or GITEA_CLI_USER
selects another. With two accounts, gt auth switch toggles between them; with more, it
asks which one on a terminal.`,
		Example: "$ gt auth switch\n$ gt auth switch bob\n$ gt auth switch bob --host gitea.com",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if len(args) == 1 {
				requested = args[0]
			}
			var user string
			switch {
			case requested != "":
				matched, ok := matchAccount(users, requested)
				if !ok {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("account %q is not logged in on %s (accounts: %s)", requested, t.Host, accountNames(users)))
				}
				user = matched
			case len(names) == 1:
				return fmt.Errorf("%s is the only account on %s; log in to another with gt auth login --host %s", names[0], t.Host, t.Host)
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
instance, such as a copy of its token that a keychain kept after a push.`,
		Example: "$ gt auth logout\n$ gt auth logout bob --host gitea.com\n# Without confirmation\n$ gt auth logout alice --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if a.options.user != "" && !strings.EqualFold(a.options.user, args[0]) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("--user %s and the account %s differ", a.options.user, args[0]))
				}
				a.options.user = args[0]
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
			if _, err := manager.Refresh(cmd.Context(), t.Host, t.User, a.deps.Client(t.Host, "", t.host.Insecure).HTTP, true, a.deps.Now()); err != nil {
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
			if token != "" && t.User != "" {
				// Like connect, print the environment token only for the account it authenticates.
				actual, err := services.CurrentAccount(cmd.Context(), a.deps.Client(t.Host, token, t.host.Insecure))
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
				credential, err := manager.GitCredential(cmd.Context(), t.Host, t.User, a.deps.Client(t.Host, "", t.host.Insecure).HTTP, a.deps.Now())
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
