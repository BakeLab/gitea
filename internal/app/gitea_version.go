// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const giteaAnnotation = "gt.gitea"

// requireGitea makes a command, and every command below it, need a Gitea server of at least
// minimum (such as "29" or "29.1.0"), which must be newer than the Gitea 28 that gt requires anyway.
// Running it against an older server fails with a version error.
func requireGitea(cmd *cobra.Command, minimum string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[giteaAnnotation] = minimum
	cmd.Long = strings.TrimSpace(cmd.Long)
	if cmd.Long == "" {
		cmd.Long = cmd.Short + "."
	}
	cmd.Long += "\n\nRequires Gitea " + minimum + " or newer."
	return cmd
}

// requireGiteaFlag makes a flag of cmd, defined beforehand, need a Gitea server of at least minimum.
// Only a flag the user sets is checked.
func requireGiteaFlag(cmd *cobra.Command, name, minimum string) *cobra.Command {
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		panic(fmt.Sprintf("requireGiteaFlag: %s has no flag --%s", cmd.CommandPath(), name))
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[giteaAnnotation] = []string{minimum}
	flag.Usage += " (requires Gitea " + minimum + " or newer)"
	return cmd
}

// commandVersions maps the path of a command, without "gt", to the newer Gitea it needs; a group's
// entry covers its subcommands. flagVersions does the same for a flag, as "run list --workflow". gt
// requires Gitea 28 anyway, so neither lists anything below that.
var (
	commandVersions = map[string]string{"project workflow": "29"}
	flagVersions    = map[string]string{}
)

// applyGiteaVersions gates the commands and flags the tables name.
func applyGiteaVersions(root *cobra.Command) {
	find := func(path string) *cobra.Command {
		cmd, rest, err := root.Find(strings.Fields(path))
		if err != nil || len(rest) > 0 || cmd == root {
			panic("applyGiteaVersions: no command " + path)
		}
		return cmd
	}
	for path, minimum := range commandVersions {
		requireGitea(find(path), minimum)
	}
	for entry, minimum := range flagVersions {
		path, name, _ := strings.Cut(entry, " --")
		requireGiteaFlag(find(path), name, minimum)
	}
}

// giteaRequirement is a minimum server version and what asks for it.
type giteaRequirement struct{ what, minimum string }

// giteaRequirements lists what the command being run, its parents and the flags set on it require.
func giteaRequirements(cmd *cobra.Command) []giteaRequirement {
	var reqs []giteaRequirement
	for c := cmd; c != nil; c = c.Parent() {
		if minimum := c.Annotations[giteaAnnotation]; minimum != "" {
			reqs = append(reqs, giteaRequirement{cmd.CommandPath(), minimum})
		}
	}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if minimum := f.Annotations[giteaAnnotation]; len(minimum) == 1 {
			reqs = append(reqs, giteaRequirement{"--" + f.Name, minimum[0]})
		}
	})
	return reqs
}

// checkGiteaVersion fails with a usage error when the server is older than the command or one of
// its flags needs. The server is asked only when something requires a version, once per run. A
// server whose version cannot be read or parsed is given the benefit of the doubt.
func (a *application) checkGiteaVersion(ctx context.Context, t target) error {
	a.mu.Lock()
	cmd := a.command
	a.mu.Unlock()
	if cmd == nil || t.client == nil {
		return nil
	}
	reqs := giteaRequirements(cmd)
	if len(reqs) == 0 {
		return nil
	}
	version, err := a.serverVersion(ctx, t)
	if err != nil || version == "" {
		return nil
	}
	for _, req := range reqs {
		if extensions.Older(comparableVersion(version), req.minimum) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%s requires Gitea %s or newer; %s runs Gitea %s", req.what, req.minimum, t.Host, version))
		}
	}
	return nil
}

// comparableVersion returns the Gitea version a server is compatible with: Forgejo reports its own
// version followed by that one, as in 11.0.0+gitea-1.22.0.
func comparableVersion(version string) string {
	if _, gitea, ok := strings.Cut(version, "+gitea-"); ok {
		return gitea
	}
	return version
}

// serverVersion returns the version of t's instance, asking it once per run.
func (a *application) serverVersion(ctx context.Context, t target) (string, error) {
	a.mu.Lock()
	version, ok := a.serverVersions[t.Host]
	a.mu.Unlock()
	if ok {
		return version, nil
	}
	read := services.ServerVersion
	if a.deps.ServerVersion != nil {
		read = a.deps.ServerVersion
	}
	version, err := read(ctx, t.client)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.serverVersions == nil {
		a.serverVersions = map[string]string{}
	}
	a.serverVersions[t.Host] = version
	a.mu.Unlock()
	return version, nil
}
