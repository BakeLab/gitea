// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"gitea.dev/tea/internal/settings"
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
		helper = "TEA_CONFIG_DIR=" + shellquote.Join(configDir) + " " + helper
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
}

// SetupCredentials adds a scoped helper without removing other credential helpers.
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
	if slices.Contains(helpers, helper) {
		return nil
	}
	_, err = runner.Output(ctx, cwd, false, "config", scope, "--add", prefix+"helper", helper)
	return err
}
