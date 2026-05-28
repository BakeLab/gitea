// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/task"
)

// CmdLoginHelper represents to login a gitea helper.
var CmdLoginHelper = cli.Command{
	Name:    "helper",
	Aliases: []string{"git-credential"},
	Usage:   "Act as a git credential helper for stored Gitea logins",
	Description: `Speaks git's credential helper protocol so that HTTPS push and clone
operations against your configured Gitea instances authenticate silently
using the tokens tea already stores.

Typical use is automatic: 'tea login add --git-credentials' (or 'tea login
helper setup' for existing logins) registers '!tea login helper' as a
credential helper in ~/.gitconfig. Git then invokes the 'get' subcommand
when it needs credentials for a configured host.`,
	Commands: []*cli.Command{
		{
			Name:        "store",
			Aliases:     []string{"erase"},
			Usage:       "No-op (git credential protocol store/erase)",
			Description: "No-op invoked by git on credential store/erase; tea persists credentials only in its own config",
			Action: func(requestCtx context.Context, _ *cli.Command) error {
				return nil
			},
		},
		{
			Name:        "setup",
			Usage:       "Register tea as a git credential helper for every configured login",
			Description: "Register tea as a git credential helper in ~/.gitconfig for every configured login",
			Action: func(requestCtx context.Context, _ *cli.Command) error {
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
			Usage:       "Return the stored token for a URL (git credential protocol)",
			Description: "Return the stored token for a given URL in git's credential helper protocol (called by git, reads request from stdin)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "login",
					Aliases: []string{"l"},
					Usage:   "Use a specific login",
				},
			},
			Action: func(requestCtx context.Context, cmd *cli.Command) error {
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
					return fmt.Errorf("hostname is required")
				} else if len(wants["protocol"]) == 0 {
					wants["protocol"] = "http"
				}

				// Use --login flag if provided, otherwise fall back to host lookup
				var userConfig *config.Login
				if loginName := cmd.String("login"); loginName != "" {
					var lookupErr error
					userConfig, lookupErr = config.GetLoginByName(loginName)
					if lookupErr != nil {
						return lookupErr
					}
					if userConfig == nil {
						return fmt.Errorf("login '%s' not found", loginName)
					}
				} else {
					var lookupErr error
					userConfig, lookupErr = config.GetLoginByHost(wants["host"])
					if lookupErr != nil {
						return lookupErr
					}
					if userConfig == nil {
						return fmt.Errorf("no login found for host '%s'", wants["host"])
					}
				}

				if len(userConfig.GetAccessToken()) == 0 {
					return fmt.Errorf("user not set")
				}

				host, err := url.Parse(userConfig.URL)
				if err != nil {
					return err
				}

				// Refresh token if expired or near expiry (updates userConfig in place)
				if err = userConfig.RefreshOAuthTokenIfNeeded(); err != nil {
					return err
				}

				_, err = fmt.Fprintf(os.Stdout, "protocol=%s\nhost=%s\nusername=%s\npassword=%s\n", host.Scheme, host.Host, userConfig.User, userConfig.GetAccessToken())
				if err != nil {
					return err
				}

				return nil
			},
		},
	},
}
