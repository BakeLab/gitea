// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
)

// Login methods that an interactive login offers.
const (
	LoginToken  = "token"
	LoginOAuth  = "oauth"
	LoginSSHKey = "ssh-key"
)

// LoginAnswers holds the choices of an interactive login. Callers prefill the defaults.
type LoginAnswers struct {
	// Host is the instance URL; Method one of LoginToken, LoginOAuth and LoginSSHKey.
	Host, Method string
	// Token is the pasted access token, and SSHKey the private key path, for their methods.
	Token, SSHKey string
	// Store is the credential store of a token: keyring or file.
	Store string
	// SetupGit configures Git to use gt for the instance.
	SetupGit bool
}

// LoginQuestions selects what an interactive login asks. The token of the token method and the key
// of the SSH key method are always asked; Store and SetupGit only for tokens.
type LoginQuestions struct {
	Host, Method, Store, SetupGit bool
	// ResolveHost normalizes an entered instance, or explains why it is not one.
	ResolveHost func(string) (string, error)
	// TokenHint tells where to create an access token on an instance.
	TokenHint func(host string) string
}

// LoginForm collects how to log in to a Gitea instance.
type LoginForm interface {
	Login(context.Context, LoginQuestions, *LoginAnswers) error
}

// Login asks the selected questions on stderr, one at a time, so that later questions can depend
// on earlier answers.
func (p *FormPrompter) Login(ctx context.Context, q LoginQuestions, a *LoginAnswers) error {
	if q.Host {
		input := huh.NewInput().Title("Gitea instance").Description("A URL or host name, such as gitea.com").Value(&a.Host).Validate(func(s string) error {
			_, err := q.ResolveHost(strings.TrimSpace(s))
			return err
		})
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(input))); err != nil {
			return err
		}
		host, err := q.ResolveHost(strings.TrimSpace(a.Host))
		if err != nil {
			return err
		}
		a.Host = host
	}
	if q.Method {
		methods := huh.NewSelect[string]().Title("How would you like to log in?").Value(&a.Method).Options(
			huh.NewOption("Paste an access token", LoginToken),
			huh.NewOption("Log in with a web browser (OAuth)", LoginOAuth),
			huh.NewOption("Sign requests with an SSH key", LoginSSHKey),
		)
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(methods))); err != nil {
			return err
		}
	}
	var fields []huh.Field
	switch a.Method {
	case LoginToken:
		fields = append(fields, huh.NewInput().Title("Paste an access token").Description(q.TokenHint(a.Host)).EchoMode(huh.EchoModePassword).Value(&a.Token).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("paste the token")
			}
			return nil
		}))
	case LoginSSHKey:
		fields = append(fields, huh.NewInput().Title("SSH private key").Value(&a.SSHKey).Validate(func(s string) error {
			_, err := os.Stat(ExpandHome(strings.TrimSpace(s)))
			return err
		}))
	}
	if a.Method != LoginSSHKey && q.Store {
		fields = append(fields, huh.NewSelect[string]().Title("Where should gt keep the token?").Value(&a.Store).Options(
			huh.NewOption("System keyring", "keyring"),
			huh.NewOption("Private file in gt's configuration directory", "file"),
		))
	}
	if a.Method != LoginSSHKey && q.SetupGit {
		fields = append(fields, huh.NewConfirm().Title("Configure git to use gt for "+a.Host+"?").Description("Git then authenticates HTTPS pushes and fetches with this account").Value(&a.SetupGit))
	}
	if len(fields) > 0 {
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(fields...))); err != nil {
			return err
		}
	}
	a.Token, a.SSHKey = strings.TrimSpace(a.Token), ExpandHome(strings.TrimSpace(a.SSHKey))
	return nil
}

// ExpandHome replaces a leading ~/ with the home directory, as a shell would.
func ExpandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}
