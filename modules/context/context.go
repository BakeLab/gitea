// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/git"
	"code.gitea.io/tea/modules/theme"
	"code.gitea.io/tea/modules/utils"

	"github.com/charmbracelet/huh"
	gogit "github.com/go-git/go-git/v5"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

var errNotAGiteaRepo = errors.New("No Gitea login found. You might want to specify --repo (and --login) to work outside of a repository")

// TeaContext contains all context derived during command initialization and wraps cli.Context
type TeaContext struct {
	*cli.Command
	Login     *config.Login // config data & client for selected login
	RepoSlug  string        // <owner>/<repo>, optional
	Owner     string        // repo owner as derived from context or provided in flag, optional
	Repo      string        // repo name as derived from context or provided in flag, optional
	Org       string        // organization name, optional
	IsGlobal  bool          // true if operating on global level
	Output    string        // value of output flag
	LocalRepo *git.TeaRepo  // is set if flags specified a local repo via --repo, or if $PWD is a git repo
}

// GetRemoteRepoHTMLURL returns the web-ui url of the remote repo,
// after ensuring a remote repo is present in the context.
func (ctx *TeaContext) GetRemoteRepoHTMLURL() string {
	ctx.Ensure(CtxRequirement{RemoteRepo: true})
	return path.Join(ctx.Login.URL, ctx.Owner, ctx.Repo)
}

// IsInteractiveMode returns true if the command is running in interactive mode
// (no flags provided and stdout is a terminal)
func (ctx *TeaContext) IsInteractiveMode() bool {
	return ctx.Command.NumFlags() == 0
}

func shouldPromptFallbackLogin(login *config.Login, canPrompt bool) bool {
	return login != nil && !login.Default && canPrompt
}

// InitCommand resolves the application context, and returns the active login, and if
// available the repo slug. It does this by reading the config file for logins, parsing
// the remotes of the .git repo specified in repoFlag or $PWD, and using overrides from
// command flags. If a local git repo can't be found, repo slug values are unset.
func InitCommand(cmd *cli.Command) *TeaContext {
	// these flags are used as overrides to the context detection via local git repo
	repoFlag := cmd.String("repo")
	loginFlag := cmd.String("login")
	remoteFlag := cmd.String("remote")
	orgFlag := cmd.String("org")
	globalFlag := cmd.Bool("global")

	var (
		c                  TeaContext
		err                error
		repoPath           string // empty means PWD
		repoFlagPathExists bool
	)

	// check if repoFlag can be interpreted as path to local repo.
	if len(repoFlag) != 0 {
		if repoFlagPathExists, err = utils.DirExists(repoFlag); err != nil {
			log.Fatal(err.Error())
		}
		if repoFlagPathExists {
			repoPath = repoFlag
		}
	}

	if len(remoteFlag) == 0 {
		remoteFlag = config.GetPreferences().FlagDefaults.Remote
	}

	if repoPath == "" {
		if repoPath, err = os.Getwd(); err != nil {
			log.Fatal(err.Error())
		}
	}

	// try to read local git repo & extract context: if repoFlag specifies a valid path, read repo in that dir,
	// otherwise attempt PWD. if no repo is found, continue with default login
	if c.LocalRepo, c.Login, c.RepoSlug, err = contextFromLocalRepo(repoPath, remoteFlag); err != nil {
		if err == errNotAGiteaRepo || err == gogit.ErrRepositoryNotExists {
			// we can deal with that, commands needing the optional values use ctx.Ensure()
		} else {
			log.Fatal(err.Error())
		}
	}

	if len(repoFlag) != 0 && !repoFlagPathExists {
		// if repoFlag is not a valid path, use it to override repoSlug
		c.RepoSlug = repoFlag
	}

	// override config user with env variable
	envLogin := GetLoginByEnvVar()
	if envLogin != nil {
		_, err := utils.ValidateAuthenticationMethod(envLogin.URL, envLogin.Token, "", "", false, "", "")
		if err != nil {
			log.Fatal(err.Error())
		}
		c.Login = envLogin
	}

	// override login from flag, or use default login if repo based detection failed
	if len(loginFlag) != 0 {
		c.Login = config.GetLoginByName(loginFlag)
		if c.Login == nil {
			log.Fatalf("Login name '%s' does not exist", loginFlag)
		}
	} else if c.Login == nil {
		if c.Login, err = config.GetDefaultLogin(); err != nil {
			if err.Error() == "No available login" {
				// TODO: maybe we can directly start interact.CreateLogin() (only if
				// we're sure we can interactively!), as gh cli does.
				fmt.Println(`No gitea login configured. To start using tea, first run
  tea login add
and then run your command again.`)
			}
			os.Exit(1)
		}

		// Only prompt for confirmation if the fallback login is not explicitly set as default
		canPrompt := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
		if shouldPromptFallbackLogin(c.Login, canPrompt) {
			fallback := false
			if err := huh.NewConfirm().
				Title(fmt.Sprintf("NOTE: no gitea login detected, whether falling back to login '%s'?", c.Login.Name)).
				Value(&fallback).
				WithTheme(theme.GetTheme()).
				Run(); err != nil {
				log.Fatalf("Get confirm failed: %v", err)
			}
			if !fallback {
				os.Exit(1)
			}
		} else if !c.Login.Default {
			fmt.Fprintf(os.Stderr, "NOTE: no gitea login detected, falling back to login '%s' in non-interactive mode.\n", c.Login.Name)
		}
	}

	// parse reposlug (owner falling back to login owner if reposlug contains only repo name)
	c.Owner, c.Repo = utils.GetOwnerAndRepo(c.RepoSlug, c.Login.User)
	c.Org = orgFlag
	c.IsGlobal = globalFlag
	c.Command = cmd
	c.Output = cmd.String("output")
	return &c
}
