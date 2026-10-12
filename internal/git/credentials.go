// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gitea.dev/cli/internal/settings"
	"github.com/kballard/go-shellquote"
)

// CredentialHelper builds a shell command for Git's helper protocol. Git itself
// supplies the final get/store/erase argument. No credential enters this command.
func CredentialHelper(instance, executable, user, configDir string) (string, error) {
	normalized, err := settings.NormalizeHost(instance)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(executable) || strings.ContainsAny(executable+user+configDir, "\x00\r\n") {
		return "", errors.New("credential helper requires a valid absolute executable path")
	}
	command := []string{executable, "auth", "git-credential", "--host", normalized}
	if user != "" {
		command = append(command, "--user", user)
	}
	helper := shellquote.Join(command...)
	if configDir != "" {
		if !filepath.IsAbs(configDir) {
			return "", errors.New("credential helper configuration directory must be absolute")
		}
		helper = "GITEA_CLI_CONFIG_DIR=" + shellquote.Join(configDir) + " " + helper
	}
	return "!" + helper, nil
}

// CredentialArguments scopes a helper to one Git operation instead of changing
// Git configuration. The empty helper resets other helpers for this instance.
func CredentialArguments(instance, executable, user, configDir string) ([]string, error) {
	helper, err := CredentialHelper(instance, executable, user, configDir)
	if err != nil {
		return nil, err
	}
	normalized, err := settings.NormalizeHost(instance)
	if err != nil {
		return nil, err
	}
	prefix := "credential." + normalized + "."
	return []string{"-c", prefix + "helper=", "-c", prefix + "helper=" + helper, "-c", prefix + "useHttpPath=true"}, nil
}

// CredentialSetup specifies an explicit persistent helper installation.
type CredentialSetup struct {
	Directory, Instance, Executable, User, ConfigDirectory string
	Local                                                  bool
	// KeepHelpers adds gt's helper after other helpers instead of resetting them for the instance.
	KeepHelpers bool
}

// SetupCredentials installs a helper scoped to the instance. Unless KeepHelpers is set, an empty
// helper first resets other helpers for the instance, so they neither answer for gt nor store its token.
func SetupCredentials(ctx context.Context, runner Runner, options CredentialSetup) error {
	normalized, err := settings.NormalizeHost(options.Instance)
	if err != nil {
		return err
	}
	helper, err := CredentialHelper(normalized, options.Executable, options.User, options.ConfigDirectory)
	if err != nil {
		return err
	}
	cwd := options.Directory
	local := options.Local
	scope := "--global"
	if local {
		scope = "--local"
	}
	prefix := "credential." + normalized + "."
	readValues := func(key string) ([]string, error) {
		data, err := runner.Output(ctx, cwd, false, "config", scope, "--null", "--get-all", prefix+key)
		if err != nil {
			if failure, ok := errors.AsType[*CommandError](err); ok && failure.ExitCode == 1 {
				return nil, nil
			}
			return nil, err
		}
		return strings.Split(strings.TrimSuffix(data, "\x00"), "\x00"), nil
	}
	helpers, err := readValues("helper")
	if err != nil {
		return err
	}
	pathValues, err := readValues("useHttpPath")
	if err != nil {
		return err
	}
	// Install path awareness first: the helper must see the installation base path.
	if len(pathValues) != 1 || pathValues[0] != "true" {
		if _, err := runner.Output(ctx, cwd, false, "config", scope, "--replace-all", prefix+"useHttpPath", "true"); err != nil {
			return err
		}
	}
	if options.KeepHelpers {
		if slices.Contains(helpers, helper) {
			return nil
		}
	} else {
		if slices.Equal(helpers, []string{"", helper}) {
			return nil
		}
		if _, err := runner.Output(ctx, cwd, false, "config", scope, "--replace-all", prefix+"helper", ""); err != nil {
			return err
		}
	}
	_, err = runner.Output(ctx, cwd, false, "config", scope, "--add", prefix+"helper", helper)
	return err
}

// HelperScope returns where Git is configured to ask gt for credentials of instance: "local" for
// the repository in dir, "global", or "" when it is not.
func HelperScope(ctx context.Context, runner Runner, dir, instance string) string {
	normalized, err := settings.NormalizeHost(instance)
	if err != nil {
		return ""
	}
	for _, scope := range []string{"local", "global"} {
		helpers, err := runner.Output(ctx, dir, false, "config", "--"+scope, "--get-all", "credential."+normalized+".helper")
		if err == nil && strings.Contains(helpers, " auth git-credential ") {
			return scope
		}
	}
	return ""
}

// InputRunner runs Git with text on its standard input.
type InputRunner interface {
	Input(ctx context.Context, dir, input string, args ...string) (string, error)
}

// Input runs Git like Output with input on its standard input, without terminal prompts.
func (s System) Input(ctx context.Context, dir, input string, args ...string) (string, error) {
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	s.Env, s.In = setEnv(env, "GIT_TERMINAL_PROMPT", "0"), strings.NewReader(input)
	return s.Output(ctx, dir, true, args...)
}

// RejectCredentials asks Git's credential helpers to erase the credentials they stored for user on
// instance, such as a copy of gt's token that another helper kept after a push. Runners that cannot
// send input reject nothing.
func RejectCredentials(ctx context.Context, runner Runner, dir, instance, user string) error {
	input, ok := runner.(InputRunner)
	if !ok {
		return nil
	}
	u, err := url.Parse(instance)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid instance URL %q", instance)
	}
	if strings.ContainsAny(user, "\x00\r\n") {
		return errors.New("invalid account name")
	}
	request := "protocol=" + u.Scheme + "\nhost=" + u.Host + "\n"
	if user != "" {
		request += "username=" + user + "\n"
	}
	_, err = input.Input(ctx, dir, request+"\n", "credential", "reject")
	return err
}
