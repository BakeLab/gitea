// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/utils"

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
	ymlPath := config.GetConfigPath()
	if e, ok := os.LookupEnv("EDITOR"); ok && e != "" {
		cmd := exec.Command(e, ymlPath)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if exist, _ := utils.FileExist(ymlPath); !exist {
		fmt.Printf("Config file does not exist, please run login add first\n")
		return nil
	}
	return open.Start(ymlPath)
}
