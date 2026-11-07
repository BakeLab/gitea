// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"

	"github.com/stretchr/testify/require"
)

func TestConfirmActionFollowsThePromptPolicy(t *testing.T) {
	c := terminal.Confirmation{Prompt: `Delete label "bug" from alice/x?`, NonTTY: `delete label "bug" from alice/x`}
	deps, _, _ := testDeps(t)
	a, cmd := outputApplication(deps, "")
	err := a.confirmAction(cmd, c)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, `confirmation required to delete label "bug" from alice/x; rerun with --yes`)
	a.options.yes = true
	require.NoError(t, a.confirmAction(cmd, c))

	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	a, cmd = outputApplication(deps, "")
	err = a.confirmAction(cmd, c)
	require.ErrorIs(t, err, terminal.ErrCanceled)
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	prompt.accept = true
	require.NoError(t, a.confirmAction(cmd, terminal.Confirmation{Prompt: "Create pull request?", DefaultYes: true}))
	require.Equal(t, []string{c.Prompt, "Create pull request?"}, prompt.confirmations)
	require.Equal(t, []bool{false, true}, prompt.defaults)
}

func TestRequireInputPromptsWithTheLabel(t *testing.T) {
	prompt := &scriptedPrompter{input: "bug"}
	deps, _, _ := testDeps(t)
	deps.Prompter = prompt
	a, cmd := outputApplication(deps, "")
	name := ""
	cmd.Flags().StringVar(&name, "name", "", "")
	_, err := a.requireInput(cmd, &name, "name", "Label name", nil)
	require.EqualError(t, err, "list requires --name")
	_, err = a.requireInput(cmd, &name, "url", "Payload URL", nil)
	require.EqualError(t, err, "list requires a payload URL argument")

	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	a, cmd = outputApplication(deps, "")
	asked, err := a.requireInput(cmd, &name, "name", "Label name", nil)
	require.NoError(t, err)
	require.True(t, asked)
	require.Equal(t, "bug", name)
	require.Equal(t, []string{"Label name (required):"}, prompt.asked)
}
