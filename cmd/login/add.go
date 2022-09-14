// Copyright 2020 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package login

import (
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/task"

	"github.com/urfave/cli/v2"
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
			EnvVars: []string{"GITEA_SERVER_URL"},
			Usage:   "Server URL",
		},
		&cli.StringFlag{
			Name:    "token",
			Aliases: []string{"t"},
			Value:   "",
			EnvVars: []string{"GITEA_SERVER_TOKEN"},
			Usage:   "Access token. Can be obtained from Settings > Applications",
		},
		&cli.StringFlag{
			Name:    "user",
			Value:   "",
			EnvVars: []string{"GITEA_SERVER_USER"},
			Usage:   "User for basic auth (will create token)",
		},
		&cli.StringFlag{
			Name:    "password",
			Aliases: []string{"pwd"},
			Value:   "",
			EnvVars: []string{"GITEA_SERVER_PASSWORD"},
			Usage:   "Password for basic auth (will create token)",
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
	},
	Action: runLoginAdd,
}

func runLoginAdd(ctx *cli.Context) error {
	// if no args create login interactive
	if ctx.NumFlags() == 0 {
		return interact.CreateLogin()
	}

	sshAgent := false
	if ctx.String("ssh-agent-key") != "" || ctx.String("ssh-agent-principal") != "" {
		sshAgent = true
	}

	// else use args to add login
	return task.CreateLogin(
		ctx.String("name"),
		ctx.String("token"),
		ctx.String("user"),
		ctx.String("password"),
		ctx.String("ssh-key"),
		ctx.String("url"),
		ctx.String("ssh-agent-principal"),
		ctx.String("ssh-agent-key"),
		ctx.Bool("insecure"),
		sshAgent)
}
