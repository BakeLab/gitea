// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strconv"
	"strings"

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

// selectionSubject names the selected items in a confirmation: `label "bug"`, or
// `2 labels ("bug", "wontfix")`.
func selectionSubject[T any](kind string, items []T, name func(T) string) string {
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = strconv.Quote(name(item))
	}
	if len(items) == 1 {
		return kind + " " + names[0]
	}
	return fmt.Sprintf("%d %ss (%s)", len(items), kind, strings.Join(names, ", "))
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
// usage error naming --flag, or the argument when flag is not a listed flag. It reports whether it
// prompted.
func (a *application) requireInput(cmd *cobra.Command, value *string, flag, label string, validate func(string) error) (bool, error) {
	s, err := a.settings()
	if err != nil {
		return false, err
	}
	missing := fmt.Errorf("%s requires --%s", commandName(cmd), flag)
	if named := cmd.Flags().Lookup(flag); named == nil || named.Hidden {
		missing = fmt.Errorf("%s requires a %s argument", commandName(cmd), lowerWords(label))
	}
	return terminal.RequireInput(cmd.Context(), value, label, validate, missing, a.deps.Streams, a.policy(s), a.deps.Prompter)
}

// lowerWords lowercases label for use mid-sentence, keeping acronyms such as URL.
func lowerWords(label string) string {
	words := strings.Fields(label)
	for i, word := range words {
		if word != strings.ToUpper(word) {
			words[i] = strings.ToLower(word)
		}
	}
	return strings.Join(words, " ")
}
