// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"code.gitea.io/tea/modules/auth"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/task"
	"github.com/urfave/cli/v3"
)

// CmdLoginHelper represents to login a gitea helper.
var CmdLoginHelper = cli.Command{
	Name:        "helper",
	Aliases:     []string{"git-credential"},
	Usage:       "Git helper",
	Description: `Git helper`,
	Hidden:      true,
	Commands: []*cli.Command{
		{
			Name:        "store",
			Description: "Command drops",
			Aliases:     []string{"erase"},
			Action: func(_ context.Context, _ *cli.Command) error {
				return nil
			},
		},
		{
			Name:        "setup",
			Description: "Setup helper to tea authenticate",
			Action: func(_ context.Context, _ *cli.Command) error {
				logins, err := config.GetLogins()
				if err != nil {
					return err
				}
				for _, login := range logins {
					added, err := task.SetupHelper(login)
					if err != nil {
						return err
					} else if added {
						fmt.Printf("Added \"%s\"\n", login.Name)
					} else {
						fmt.Printf("\"%s\" has already been added!\n", login.Name)
					}
				}
				return nil
			},
		},
		{
			Name:        "get",
			Description: "Get token to auth",
			Action: func(_ context.Context, cmd *cli.Command) error {
				wants := map[string]string{}
				s := bufio.NewScanner(os.Stdin)
				for s.Scan() {
					line := s.Text()
					if line == "" {
						break
					}
					parts := strings.SplitN(line, "=", 2)
					if len(parts) < 2 {
						continue
					}
					key, value := parts[0], parts[1]
					if key == "url" {
						u, err := url.Parse(value)
						if err != nil {
							return err
						}
						wants["protocol"] = u.Scheme
						wants["host"] = u.Host
						wants["path"] = u.Path
						wants["username"] = u.User.Username()
						wants["password"], _ = u.User.Password()
					} else {
						wants[key] = value
					}
				}

				if len(wants["host"]) == 0 {
					log.Fatal("Require hostname")
				} else if len(wants["protocol"]) == 0 {
					wants["protocol"] = "http"
				}

				userConfig := config.GetLoginByHost(wants["host"])
				if userConfig == nil {
					log.Fatal("host not exists")
				} else if len(userConfig.Token) == 0 {
					log.Fatal("User no set")
				}

				host, err := url.Parse(userConfig.URL)
				if err != nil {
					return err
				}

				if userConfig.TokenExpiry > 0 && time.Now().Unix() > userConfig.TokenExpiry {
					// Token is expired, refresh it
					err = auth.RefreshAccessToken(userConfig)
					if err != nil {
						return err
					}

					// Once token is refreshed, get the latest from the updated config
					refreshedConfig := config.GetLoginByHost(wants["host"])
					if refreshedConfig != nil {
						userConfig = refreshedConfig
					}
				}

				_, err = fmt.Fprintf(os.Stdout, "protocol=%s\nhost=%s\nusername=%s\npassword=%s\n", host.Scheme, host.Host, userConfig.User, userConfig.Token)
				if err != nil {
					return err
				}

				return nil
			},
		},
	},
}
