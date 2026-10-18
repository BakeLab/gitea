// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// confirmAction asks c under the prompt policy unless --yes was supplied. Constructive actions set
// DefaultYes, destructive ones keep the default no, and irreversible ones set Typed. A declined
// confirmation returns terminal.ErrCanceled.
func (a *application) confirmAction(cmd *cobra.Command, c terminal.Confirmation) error {
	s, err := a.settings()
	if err != nil {
		return err
	}
	return terminal.ConfirmAction(cmd.Context(), c, a.options.yes, a.deps.Streams, a.policy(s), a.deps.Prompter)
}

// canPrompt reports whether gt may ask questions on this terminal.
func (a *application) canPrompt() bool {
	s, err := a.settings()
	return err == nil && a.policy(s).CanPrompt(a.deps.Streams)
}

// chooser returns the prompter's selection prompt when prompts are allowed.
func (a *application) chooser() (terminal.Chooser, bool) {
	chooser, ok := a.deps.Prompter.(terminal.Chooser)
	return chooser, ok && a.canPrompt()
}

// deleteConfirmed asks to delete subject, such as `label "bug" from alice/x`, deletes it with del
// and reports "Deleted <subject>".
func (a *application) deleteConfirmed(cmd *cobra.Command, subject string, del func() error) error {
	if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + "?"}); err != nil {
		return err
	}
	if err := del(); err != nil {
		return err
	}
	a.done(cmd, "Deleted %s", subject)
	return nil
}

// requireInput fills a missing value from a prompt titled label, such as "Label name", re-prompting
// until the answer is non-empty and validate, when set, accepts it. Without prompts it returns a
// usage error naming --flag. It reports whether it prompted.
func (a *application) requireInput(cmd *cobra.Command, value *string, flag, label string, validate func(string) error) (bool, error) {
	s, err := a.settings()
	if err != nil {
		return false, err
	}
	if hidden := cmd.Flags().Lookup(flag); *value == "" && hidden != nil && hidden.Hidden && !a.policy(s).CanPrompt(a.deps.Streams) {
		// --help does not list the flag, so point at the argument it stands in for.
		return false, clierr.Wrap(clierr.Usage, fmt.Errorf("missing required input: %s (pass it as an argument)", label))
	}
	return terminal.RequireInput(cmd.Context(), value, flag, label, validate, a.deps.Streams, a.policy(s), a.deps.Prompter)
}
