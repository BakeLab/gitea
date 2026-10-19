// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
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
missing or rejected. With --json it exits 0 either way; read credential_available, verified and error.`,
		Example: `$ gt auth status
# Check the credentials against their instances
$ gt auth status --verify
$ gt auth status --host gitea.com --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.settings()
			if err != nil {
				return err
			}
			if s.Hosts.Hosts, err = a.environmentHosts(s.Hosts.Hosts); err != nil {
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
	return jsonOutput(status, jsonFields[authStatusRow]())
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
			err = nil
			if account.AuthMethod == settings.AuthOAuth {
				// An expired access token is not a broken credential: commands refresh it before use.
				if credential, err = a.refreshedCredential(ctx, instance, user, host); err == nil && !credential.ExpiresAt.IsZero() {
					row.ExpiresAt = &credential.ExpiresAt
				}
			}
			if err == nil {
				row.Scopes, err = a.verifyAccount(ctx, a.policy(s), instance, user, host, account, credential.AccessToken)
			}
		}
		verified := err == nil
		row.Verified = &verified
		if err != nil {
			row.Error = err.Error()
		}
	}
	return row
}

// refreshedCredential loads the OAuth credential of an account, refreshing it first when it expires.
func (a *application) refreshedCredential(ctx context.Context, instance, user string, host settings.Host) (auth.Credential, error) {
	manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
	if err != nil {
		return auth.Credential{}, err
	}
	return manager.Refresh(ctx, instance, user, a.instanceClient(instance, "", host).HTTP, false, a.deps.Now())
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
		row.Available, row.Error = false, "GITEA_TOKEN needs GITEA_CLI_HOST or GITEA_INSTANCE_URL naming its instance"
		return row, true
	}
	row.Host = instance
	if verify {
		client := a.instanceClient(instance, token, s.Hosts.Hosts[instance])
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
