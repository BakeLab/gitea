// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// confirmAction asks c under the prompt policy unless --yes was supplied. Constructive actions set
// DefaultYes, destructive ones keep the default no, and irreversible ones set Typed. A declined
// confirmation returns terminal.ErrCanceled.
func (a *application) confirmAction(cmd *cobra.Command, c terminal.Confirmation) error {
	s, err := a.deps.Settings.Load()
	if err != nil {
		return err
	}
	return terminal.ConfirmAction(cmd.Context(), c, a.options.yes, a.deps.Streams, a.policy(s), a.deps.Prompter)
}

// requireInput fills a missing value from a prompt titled label, such as "Label name", re-prompting
// until the answer is non-empty and validate, when set, accepts it. Without prompts it returns a
// usage error naming --flag. It reports whether it prompted.
func (a *application) requireInput(cmd *cobra.Command, value *string, flag, label string, validate func(string) error) (bool, error) {
	s, err := a.deps.Settings.Load()
	if err != nil {
		return false, err
	}
	return terminal.RequireInput(cmd.Context(), value, flag, label, validate, a.deps.Streams, a.policy(s), a.deps.Prompter)
}
