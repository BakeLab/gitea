// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"fmt"

	"code.gitea.io/tea/modules/auth"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/task"

	"github.com/urfave/cli/v3"
)

// CmdLoginAdd represents to login a gitea server.
var CmdLoginAdd = cli.Command{
	Name:        "add",
	Usage:       "Add a Gitea login",
	Description: `Add a Gitea login, without args it will create one interactively`,
	ArgsUsage:   " ", // command does not accept arguments
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "name",
			Aliases: []string{"n"},
			Usage:   "Login name",
		},
		&cli.StringFlag{
			Name:    "url",
			Aliases: []string{"u"},
			Value:   "https://gitea.com",
			Sources: cli.EnvVars("GITEA_SERVER_URL"),
			Usage:   "Server URL",
		},
		&cli.BoolFlag{
			Name:    "no-version-check",
			Aliases: []string{"nv"},
			Usage:   "Do not check version of Gitea instance",
		},
		&cli.StringFlag{
			Name:    "token",
			Aliases: []string{"t"},
			Value:   "",
			Sources: cli.EnvVars("GITEA_SERVER_TOKEN"),
			Usage:   "Access token. Can be obtained from Settings > Applications",
		},
		&cli.StringFlag{
			Name:    "user",
			Value:   "",
			Sources: cli.EnvVars("GITEA_SERVER_USER"),
			Usage:   "User for basic auth (will create token)",
		},
		&cli.StringFlag{
			Name:    "password",
			Aliases: []string{"pwd"},
			Value:   "",
			Sources: cli.EnvVars("GITEA_SERVER_PASSWORD"),
			Usage:   "Password for basic auth (will create token)",
		},
		&cli.StringFlag{
			Name:    "otp",
			Sources: cli.EnvVars("GITEA_SERVER_OTP"),
			Usage:   "OTP token for auth, if necessary",
		},
		&cli.StringFlag{
			Name:    "scopes",
			Sources: cli.EnvVars("GITEA_SCOPES"),
			Usage:   "Token scopes to add when creating a new token, separated by a comma",
		},
		&cli.StringFlag{
			Name:    "ssh-key",
			Aliases: []string{"s"},
			Usage:   "Path to a SSH key/certificate to use, overrides auto-discovery",
		},
		&cli.BoolFlag{
			Name:    "insecure",
			Aliases: []string{"i"},
			Usage:   "Disable TLS verification",
		},
		&cli.StringFlag{
			Name:    "ssh-agent-principal",
			Aliases: []string{"c"},
			Usage:   "Use SSH certificate with specified principal to login (needs a running ssh-agent with certificate loaded)",
		},
		&cli.StringFlag{
			Name:    "ssh-agent-key",
			Aliases: []string{"a"},
			Usage:   "Use SSH public key or SSH fingerprint to login (needs a running ssh-agent with ssh key loaded)",
		},
		&cli.BoolFlag{
			Name:    "helper",
			Aliases: []string{"j"},
			Usage:   "Add helper",
		},
		&cli.BoolFlag{
			Name:    "oauth",
			Aliases: []string{"o"},
			Usage:   "Use interactive OAuth2 flow for authentication",
		},
		&cli.StringFlag{
			Name:  "client-id",
			Usage: "OAuth client ID (for use with --oauth)",
		},
		&cli.StringFlag{
			Name:  "redirect-url",
			Usage: "OAuth redirect URL (for use with --oauth)",
		},
	},
	Action: runLoginAdd,
}

func runLoginAdd(_ context.Context, cmd *cli.Command) error {
	// if no args create login interactive
	if cmd.NumFlags() == 0 {
		if err := interact.CreateLogin(); err != nil && !interact.IsQuitting(err) {
			return fmt.Errorf("error adding login: %w", err)
		}
		return nil
	}

	// if OAuth flag is provided, use OAuth2 PKCE flow
	if cmd.Bool("oauth") {
		opts := auth.OAuthOptions{
			Name:     cmd.String("name"),
			URL:      cmd.String("url"),
			Insecure: cmd.Bool("insecure"),
		}

		// Only set clientID if provided
		if cmd.String("client-id") != "" {
			opts.ClientID = cmd.String("client-id")
		}

		// Only set redirect URL if provided
		if cmd.String("redirect-url") != "" {
			opts.RedirectURL = cmd.String("redirect-url")
		}

		return auth.OAuthLoginWithFullOptions(opts)
	}

	sshAgent := false
	if cmd.String("ssh-agent-key") != "" || cmd.String("ssh-agent-principal") != "" {
		sshAgent = true
	}

	// else use args to add login
	return task.CreateLogin(
		cmd.String("name"),
		cmd.String("token"),
		cmd.String("user"),
		cmd.String("password"),
		cmd.String("otp"),
		cmd.String("scopes"),
		cmd.String("ssh-key"),
		cmd.String("url"),
		cmd.String("ssh-agent-principal"),
		cmd.String("ssh-agent-key"),
		cmd.Bool("insecure"),
		sshAgent,
		!cmd.Bool("no-version-check"),
		cmd.Bool("helper"),
	)
}
