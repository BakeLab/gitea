// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"

	"github.com/spf13/cobra"
)

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
	var backend, clientID, sshKey, sshAgentPrincipal, sshAgentKey, clientCert, clientKey string
	var scopes, headerArgs []string
	login := &cobra.Command{
		Use: "login", Short: "Log in to a Gitea instance", Args: argsExact(0),
		Annotations: map[string]string{"gt.text": "login does not return a resource", "gt.confirm": "true"},
		Long: `Log in to a Gitea instance. On a terminal without a method flag, gt asks for the instance,
how to log in, where to keep the token and whether git should use gt for the instance.

Create a token on the instance under Settings > Applications. Logging in to an account
that gt already has replaces its stored credential after a confirmation (or with --yes).
The first instance logged in to becomes default_host, unless one is set.`,
		Example: `$ gt auth login
$ gt auth login --host gitea.com --with-token < token.txt
$ gt auth login --host gitea.com --oauth --scopes read:repository,read:user
$ gt auth login --host gitea.example.com --with-token --header 'CF-Access-Client-Id: $CF_ID' --header 'CF-Access-Client-Secret: $CF_SECRET'`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if clientKey != "" && clientCert == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--client-key requires --client-cert"))
			}
			for _, path := range []*string{&clientCert, &clientKey} {
				if *path != "" {
					var err error
					if *path, err = settingPath(*path); err != nil {
						return err
					}
				}
			}
			headers := map[string]string{}
			for _, raw := range headerArgs {
				name, value, err := settings.ParseHeader(raw)
				if err == nil {
					err = checkHeaderValue(name, value)
				}
				if err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
				headers[name] = value
			}
			a.warnUnsentHeaders(cmd, headers)
			var methods []string
			for _, choice := range []struct {
				flag   string
				chosen bool
			}{{"--with-token", withToken}, {"--oauth", oauth}, {"--ssh-key", sshKey != ""}, {"--ssh-agent-principal", sshAgentPrincipal != ""}, {"--ssh-agent-key", sshAgentKey != ""}} {
				if choice.chosen {
					methods = append(methods, choice.flag)
				}
			}
			if len(methods) == 0 && (clientID != "" || cmd.Flags().Changed("scopes")) {
				// --client-id and --scopes only apply to OAuth, so they choose it.
				oauth, methods = true, []string{"--oauth"}
			}
			switch {
			case len(methods) > 1:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with %s", methods[0], methods[1]))
			case !oauth && clientID != "":
				return clierr.Wrap(clierr.Usage, errors.New("--client-id cannot be combined with "+methods[0]))
			case !oauth && cmd.Flags().Changed("scopes"):
				return clierr.Wrap(clierr.Usage, errors.New("--scopes cannot be combined with "+methods[0]))
			}
			snapshot, err := a.settings()
			if err != nil {
				return err
			}
			form, hasForm := a.deps.Prompter.(terminal.LoginForm)
			interactive := len(methods) == 0
			if interactive && (!hasForm || !a.policy(snapshot).CanPrompt(a.deps.Streams)) {
				return clierr.Wrap(clierr.Usage, errors.New(commandName(cmd)+" requires --with-token, --oauth or --ssh-key when it cannot ask on a terminal"))
			}
			resolveHost := func(raw string) (string, error) { return settings.ResolveHost(raw, snapshot.Hosts.Hosts) }
			instance := cmp.Or(a.options.host, a.tokenInstance())
			if instance != "" {
				if instance, err = resolveHost(instance); err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
			}
			answers := terminal.LoginAnswers{Host: instance, Store: backend, SSHKey: sshKey}
			switch {
			case oauth:
				answers.Method = terminal.LoginOAuth
			case len(methods) == 1 && !withToken:
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
			hostOptions := auth.HostOptions{Insecure: insecure, Headers: headers, ClientCert: clientCert, ClientKey: clientKey, Replace: true}
			loginHost := hostOptions.Apply(host)
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
				account, user, err := a.signedLogin(cmd, snapshot, instance, loginHost, settings.Account{SSHKey: answers.SSHKey}, sshAgentPrincipal, sshAgentKey)
				if err != nil {
					return err
				}
				if !replacing {
					if _, err := a.confirmReplace(cmd, instance, user); err != nil {
						return err
					}
				}
				if err := manager.RegisterHTTPSign(instance, user, account, hostOptions); err != nil {
					return err
				}
				a.done(cmd, "Logged in to %s as %s (%s)", instance, user, credentialLabel(account))
				a.defaultToFirstInstance(cmd, instance)
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
				client := a.instanceClient(instance, "", loginHost)
				// A browser cannot report an instance that never answers; it would only wait.
				if _, err := services.ServerVersion(cmd.Context(), client); err != nil {
					if _, answered := errors.AsType[*transport.APIError](err); !answered {
						return err
					}
				}
				credential, err = auth.OAuthLogin(cmd.Context(), auth.OAuthOptions{Instance: instance, ClientID: clientID, Scopes: scopes, HTTP: client.HTTP, Authorize: func(_ context.Context, authorizationURL string) error {
					// On a terminal, openBrowserShowing names the whole URL, which people can copy when
					// no browser shows up, and the URL is printed again only when opening fails.
					announced := a.deps.Streams.OutTTY
					if !announced {
						if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Open this URL to authorize gt:", authorizationURL); err != nil {
							return err
						}
					}
					if err := a.openBrowserShowing(cmd, authorizationURL, terminal.SafeLine(authorizationURL)); err != nil {
						if announced {
							_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser automatically; open this URL to authorize gt:", authorizationURL)
						} else {
							_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser automatically; open the URL above manually.")
						}
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
			user, err := services.CurrentAccount(cmd.Context(), a.instanceClient(instance, credential.AccessToken, loginHost))
			if err != nil {
				return rejectedLogin(err, "the token", "check it or create a new one under Settings > Applications")
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
			hostOptions.OAuthClientID = clientID
			if err := manager.Register(instance, user, answers.Store, method, hostOptions, credential); err != nil {
				if method == settings.AuthOAuth {
					return fmt.Errorf("OAuth authorization completed, but credentials could not be saved or registered: %w", err)
				}
				return err
			}
			if method == settings.AuthOAuth {
				a.feedback(cmd, "✓", "green", "OAuth authorization completed for %s as %s; credential saved successfully (%s)", instance, user, credentialLabel(account))
			} else {
				a.done(cmd, "Logged in to %s as %s (%s)", instance, user, credentialLabel(account))
			}
			a.defaultToFirstInstance(cmd, instance)
			if interactive && answers.SetupGit {
				if err := a.setupGit(cmd, instance, false, false); err != nil {
					a.warn(cmd, "Could not configure git: %v; run gt auth setup-git", err)
				}
			}
			return nil
		},
	}
	login.Flags().BoolVar(&oauth, "oauth", false, "Authorize in a web browser with OAuth")
	login.Flags().StringVar(&clientID, "client-id", "", "OAuth application `ID` registered on the instance (default: the instance's stored ID, then gt's); implies --oauth")
	login.Flags().StringSliceVar(&scopes, "scopes", nil, "OAuth token `scopes`, such as read:repository (default: write access to everything but administration); implies --oauth")
	login.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification for this instance")
	login.Flags().StringVar(&clientCert, "client-cert", "", "Present the PEM TLS client certificate in `file` to an instance that requires one; convert a PKCS#12 file with: openssl pkcs12 -in cert.p12 -clcerts -nokeys -out cert.pem")
	login.Flags().StringVar(&clientKey, "client-key", "", "Unencrypted PEM private key `file` of --client-cert (default: the key in the --client-cert file); convert a PKCS#12 file with: openssl pkcs12 -in cert.p12 -nocerts -nodes -out key.pem")
	login.Flags().StringArrayVar(&headerArgs, "header", nil, "Send the `\"Name: value\"` header with every request to this instance, such as an access proxy needs; a value of $NAME or ${NAME} is read from that environment variable (repeatable)")
	login.Flags().BoolVar(&withToken, "with-token", false, "Read an access token from stdin, or ask for it on a terminal")
	login.Flags().StringVar(&sshKey, "ssh-key", "", "Sign requests with the SSH private key in `file`")
	login.Flags().StringVar(&sshAgentPrincipal, "ssh-agent-principal", "", "Sign requests with the ssh-agent certificate of `principal`")
	login.Flags().StringVar(&sshAgentKey, "ssh-agent-key", "", "Sign requests with the ssh-agent key of `fingerprint`")
	enumFlag(login, &backend, "credential-store", "", settings.StoreKeyring, "Where to keep the token", settings.StoreKeyring, settings.StoreFile)
	enumValues(login, "scopes", oauthScopes...)
	return login
}

// rejectedLogin words a 401 for the credential that gt auth login was given, so that Explain does
// not blame GITEA_TOKEN or tell the user to run the command they are running.
func rejectedLogin(err error, credential, advice string) error {
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusUnauthorized {
		return explain(err, "%s was rejected by %s; %s", credential, cmp.Or(apiErr.Instance, "the instance"), advice)
	}
	return err
}

// confirmReplace asks before a login replaces the stored credential of an account that gt already
// has, and reports whether it does.
func (a *application) confirmReplace(cmd *cobra.Command, instance, user string) (bool, error) {
	snapshot, err := a.settings()
	if err != nil {
		return false, err
	}
	configured, exists := findAccountKey(snapshot.Hosts.Hosts[instance].Users, user)
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
func (a *application) signedLogin(cmd *cobra.Command, snapshot settings.Snapshot, instance string, host settings.Host, account settings.Account, agentPrincipal, agentKey string) (settings.Account, string, error) {
	switch {
	case agentPrincipal != "":
		account = settings.Account{AuthMethod: settings.AuthSSH, SSHAgent: true, SSHCertPrincipal: agentPrincipal}
	case agentKey != "":
		account = settings.Account{AuthMethod: settings.AuthHTTPSign, SSHAgent: true, SSHKeyFingerprint: agentKey}
	default:
		path, err := settingPath(account.SSHKey)
		if err != nil {
			return account, "", err
		}
		account.SSHKey = path
		if _, err := os.Stat(account.SSHKey + "-cert.pub"); err == nil {
			account.AuthMethod = settings.AuthSSH
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
	client := a.instanceClient(instance, "", host)
	client.Signer = signer
	user, err := services.CurrentAccount(cmd.Context(), client)
	if err != nil {
		return account, "", rejectedLogin(err, "the SSH signature", "check that the key is registered for your account")
	}
	if selectedUser, _ := a.selectedUser(); !services.MatchAccount(selectedUser, user) {
		return account, "", clierr.Wrap(clierr.Usage, errors.New("authenticated account does not match --user or GITEA_CLI_USER"))
	}
	return account, user, nil
}

// settingPath stores a file path, such as an SSH key's, absolute so that it names the same file
// from every directory. An empty path stays empty, which clears the setting.
func settingPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	return filepath.Abs(terminal.ExpandHome(path))
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

// defaultToFirstInstance makes instance default_host when it is the only instance with accounts and
// none is set, so that logging in to a second instance later does not make commands ask for --host.
func (a *application) defaultToFirstInstance(cmd *cobra.Command, instance string) {
	first := func(s settings.Snapshot) bool {
		if s.Preferences.DefaultHost != "" {
			return false
		}
		for name, host := range s.Hosts.Hosts {
			if name != instance && len(host.Users) > 0 {
				return false
			}
		}
		return true
	}
	if s, err := a.deps.Settings.Load(); err != nil || !first(s) {
		return
	}
	set := false
	err := a.deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
		if set = first(*s); set {
			s.Preferences.DefaultHost = instance
		}
		return nil
	})
	switch {
	case err != nil:
		a.warn(cmd, "Could not set default_host to %s: %v", instance, err)
	case set:
		a.done(cmd, "Set default_host to %s", instance)
	}
}
