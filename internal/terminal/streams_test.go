// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"charm.land/huh/v2"
	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

// testPrompter returns a form prompter reading in and the buffer its forms render to.
func testPrompter(in io.Reader) (*FormPrompter, *bytes.Buffer) {
	rendered := &bytes.Buffer{}
	return &FormPrompter{Streams: Streams{In: in, Out: &bytes.Buffer{}, ErrOut: rendered, InTTY: true, ErrTTY: true}}, rendered
}

// accessiblePrompter is testPrompter in accessible mode, which renders field titles as plain text.
func accessiblePrompter(t *testing.T, input string) (*FormPrompter, *bytes.Buffer) {
	t.Helper()
	t.Setenv("TERM", "dumb")
	return testPrompter(strings.NewReader(input))
}

func noEdit(context.Context, string) (Draft, error) { return Draft{}, nil }

func TestPromptPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy   Policy
		streams  Streams
		expected bool
	}{
		{Policy{Enabled: true}, Streams{InTTY: true, ErrTTY: true}, true},
		{Policy{Enabled: true}, Streams{InTTY: true, OutTTY: true}, false},
		{Policy{Enabled: true, NoInput: true}, Streams{InTTY: true, ErrTTY: true}, false},
		{Policy{Enabled: true, Machine: true}, Streams{InTTY: true, ErrTTY: true}, false},
		{Policy{Enabled: true, Machine: true, Interactive: true}, Streams{InTTY: true, ErrTTY: true}, true},
	} {
		require.Equal(t, tc.expected, tc.policy.CanPrompt(tc.streams))
	}
	prompt := &askingPrompt{}
	value := "supplied"
	asked, err := RequireInput(context.Background(), &value, "title", "title", nil, Streams{}, Policy{}, prompt)
	require.NoError(t, err)
	require.False(t, asked)
	require.Empty(t, prompt.titles)
	value = ""
	_, err = RequireInput(context.Background(), &value, "title", "title", nil, Streams{}, Policy{}, prompt)
	require.ErrorContains(t, err, "--title")
}

// askingPrompt answers Asker prompts from scripted answers. Ask skips answers that validate rejects,
// as an inline re-prompt does, and cancels when none is left.
type askingPrompt struct {
	answers, rejected, titles []string
	accept                    bool
	defaults                  []bool
}

func (p *askingPrompt) Ask(_ context.Context, title string, validate func(string) error) (string, error) {
	p.titles = append(p.titles, title)
	for len(p.answers) > 0 {
		answer := p.answers[0]
		p.answers = p.answers[1:]
		if validate == nil || validate(answer) == nil {
			return answer, nil
		}
		p.rejected = append(p.rejected, answer)
	}
	return "", ErrCanceled
}

func (p *askingPrompt) SecretInput(context.Context, string) (string, error) { return "", nil }

func (p *askingPrompt) AskText(_ context.Context, title, initial string) (string, error) {
	p.titles = append(p.titles, title)
	return initial + " edited", nil
}

func (p *askingPrompt) ConfirmDefault(_ context.Context, title string, defaultYes bool) (bool, error) {
	p.titles = append(p.titles, title)
	p.defaults = append(p.defaults, defaultYes)
	return p.accept, nil
}

func TestConfirmAction(t *testing.T) {
	terminal := Streams{InTTY: true, ErrTTY: true}
	deleteLabel := Confirmation{Prompt: `Delete label "bug" from alice/x?`, NonTTY: `delete label "bug" from alice/x`}
	typed := Confirmation{Prompt: "Delete alice/x?", Typed: "alice/x"}
	for _, tc := range []struct {
		name     string
		c        Confirmation
		yes      bool
		streams  Streams
		prompt   Prompter
		code     int
		message  string
		rejected []string
	}{
		{name: "accepted by flag", c: deleteLabel, yes: true, prompt: &askingPrompt{}},
		{name: "names the action without a terminal", c: deleteLabel, prompt: &askingPrompt{}, code: clierr.Usage, message: `confirmation required to delete label "bug" from alice/x; rerun with --yes`},
		{name: "names the prompted action without a terminal", c: Confirmation{Prompt: "Delete alice/x? It becomes read-only."}, prompt: &askingPrompt{}, code: clierr.Usage, message: "confirmation required to delete alice/x; rerun with --yes"},
		{name: "without a terminal or action", c: Confirmation{Prompt: "Go?", DefaultYes: true}, prompt: &askingPrompt{}, code: clierr.Usage, message: "confirmation required; rerun with --yes"},
		{name: "accepted", c: deleteLabel, streams: terminal, prompt: &askingPrompt{accept: true}},
		{name: "declined", c: deleteLabel, streams: terminal, prompt: &askingPrompt{}, code: clierr.Canceled, message: "canceled"},
		{name: "re-prompts until the name is typed", c: typed, streams: terminal, prompt: &askingPrompt{answers: []string{"alice/y", " Alice/X "}}, rejected: []string{"alice/y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ConfirmAction(context.Background(), tc.c, tc.yes, tc.streams, Policy{Enabled: true}, tc.prompt)
			require.Equal(t, tc.code, clierr.ExitCode(err), "%v", err)
			if tc.message != "" {
				require.EqualError(t, err, tc.message)
			}
			if tc.code == clierr.Canceled {
				require.ErrorIs(t, err, ErrCanceled)
			}
			if asker, ok := tc.prompt.(*askingPrompt); ok {
				require.Equal(t, tc.rejected, asker.rejected)
			}
		})
	}
}

func TestConfirmActionPassesTheDefault(t *testing.T) {
	prompt := &askingPrompt{accept: true}
	streams := Streams{InTTY: true, ErrTTY: true}
	require.NoError(t, ConfirmAction(context.Background(), Confirmation{Prompt: "Create pull request?", DefaultYes: true}, false, streams, Policy{Enabled: true}, prompt))
	require.NoError(t, ConfirmAction(context.Background(), Confirmation{Prompt: "Delete branch?"}, false, streams, Policy{Enabled: true}, prompt))
	require.Equal(t, []bool{true, false}, prompt.defaults)
	require.Equal(t, []string{"Create pull request?", "Delete branch?"}, prompt.titles)
}

func TestRequireInput(t *testing.T) {
	streams, policy := Streams{InTTY: true, ErrTTY: true}, Policy{Enabled: true}
	hex := func(s string) error {
		if len(s) != 6 {
			return errors.New("use six hex digits")
		}
		return nil
	}

	asker := &askingPrompt{answers: []string{"", "red", "d73a4a"}}
	value := ""
	asked, err := RequireInput(context.Background(), &value, "color", "Color (hex)", hex, streams, policy, asker)
	require.NoError(t, err)
	require.True(t, asked)
	require.Equal(t, "d73a4a", value)
	require.Equal(t, []string{"", "red"}, asker.rejected)
	require.Equal(t, []string{"Color (hex)"}, asker.titles)

	value = ""
	_, err = RequireInput(context.Background(), &value, "color", "Color (hex)", hex, Streams{}, policy, asker)
	require.EqualError(t, err, "missing required input: supply --color")
}

func TestFormPrompterConfirmDefault(t *testing.T) {
	for _, defaultYes := range []bool{true, false} {
		p, _ := accessiblePrompter(t, "\n")
		accepted, err := p.ConfirmDefault(context.Background(), "Submit?", defaultYes)
		require.NoError(t, err)
		require.Equal(t, defaultYes, accepted)
	}
}

func TestFormPrompterAskRepromptsInline(t *testing.T) {
	p, rendered := accessiblePrompter(t, "\n  bug  \n")
	value, err := p.Ask(context.Background(), "Label name", func(s string) error {
		if s == "" {
			return errors.New("a value is required")
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "bug", value)
	require.Contains(t, rendered.String(), "a value is required")
}

func TestPromptAbortIsCanceled(t *testing.T) {
	err := promptError(context.Background(), huh.ErrUserAborted)
	require.ErrorIs(t, err, ErrCanceled)
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
}
