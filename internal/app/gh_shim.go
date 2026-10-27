// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"gitea.dev/cli/internal/clierr"
)

// ExecuteGH answers the gh commands an extension installed with --compat-gh runs as the matching
// gt commands: gh api, gh repo view with gt's JSON fields and gh auth token. GH_TOKEN and GH_HOST
// stand in for GITEA_TOKEN and GITEA_CLI_HOST when those are not set.
func ExecuteGH(ctx context.Context, deps Dependencies, args []string) error {
	supported := len(args) > 0 && args[0] == "api" || len(args) > 1 && (args[0] == "repo" && args[1] == "view" || args[0] == "auth" && args[1] == "token")
	if !supported {
		command := "gh"
		for _, arg := range args[:min(len(args), 2)] {
			command += " " + arg
		}
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is not supported by the gh shim of gt; it answers gh api, gh repo view and gh auth token", command))
	}
	if args[0] == "repo" {
		// gh's --json always takes fields, which gt must not read as the repository.
		args = slices.Clone(args)
		if at := slices.Index(args, "--json"); at >= 0 && at+1 < len(args) {
			args = slices.Concat(args[:at], []string{"--json=" + args[at+1]}, args[at+2:])
		}
	}
	getenv := deps.Getenv
	deps.Getenv = func(key string) string {
		value := getenv(key)
		switch {
		case value != "":
		case key == "GITEA_TOKEN":
			value = cmp.Or(getenv("GH_TOKEN"), getenv("GH_ENTERPRISE_TOKEN"))
		case key == "GITEA_CLI_HOST" && getenv("GITEA_INSTANCE_URL") == "" && getenv("GH_HOST") != "":
			value = "https://" + getenv("GH_HOST")
		}
		return value
	}
	deps.LookupEnv = nil
	return Execute(ctx, deps, args)
}
