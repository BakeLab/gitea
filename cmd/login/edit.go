// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"log"
	"os"
	"os/exec"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/config"

	"github.com/skratchdot/open-golang/open"
	"github.com/urfave/cli/v3"
)

// CmdLoginEdit represents to login a gitea server.
var CmdLoginEdit = cli.Command{
	Name:        "edit",
	Aliases:     []string{"e"},
	Usage:       "Edit Gitea logins",
	Description: `Edit Gitea logins`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runLoginEdit,
	Flags:       []cli.Flag{&flags.OutputFlag},
}

func runLoginEdit(_ context.Context, _ *cli.Command) error {
	if e, ok := os.LookupEnv("EDITOR"); ok && e != "" {
		cmd := exec.Command(e, config.GetConfigPath())
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal(err.Error())
		}
	}
	return open.Start(config.GetConfigPath())
}
