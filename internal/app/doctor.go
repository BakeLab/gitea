// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"

	"gitea.dev/tea/internal/clierr"
	"github.com/spf13/cobra"
)

func (a *application) doctorCommand() *cobra.Command {
	return &cobra.Command{Use: "doctor", Short: "Inspect resolved instance and repository context", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		// Repository discovery is best-effort: doctor must also work outside a clone.
		// Only discovery (usage) failures fall back; authentication errors are reported.
		target, err := a.resolve(cmd.Context(), true)
		if clierr.ExitCode(err) == clierr.Usage && a.options.repo == "" && a.deps.Getenv("TEA_REPO") == "" {
			target, err = a.resolve(cmd.Context(), false)
		}
		if err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, target)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "instance\t%s\t%s\naccount\t%s\t%s\nrepository\t%s\t%s\n", target.Host, target.HostSource, target.User, target.UserSource, target.Repo, target.RepoSource)
		return err
	}}
}
