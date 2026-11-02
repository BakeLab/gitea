// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gt "charm.land/bubbletea/v2"
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
		prompt   *askingPrompt
		code     int
		message  string
		rejected []string
		defaults []bool
	}{
		{name: "accepted by flag", c: deleteLabel, yes: true, prompt: &askingPrompt{}},
		{name: "names the action without a terminal", c: deleteLabel, prompt: &askingPrompt{}, code: clierr.Usage, message: `confirmation required to delete label "bug" from alice/x; rerun with --yes`},
		{name: "names the prompted action without a terminal", c: Confirmation{Prompt: "Delete alice/x? It becomes read-only."}, prompt: &askingPrompt{}, code: clierr.Usage, message: "confirmation required to delete alice/x; rerun with --yes"},
		{name: "keeps a quoted question mark", c: Confirmation{Prompt: `Delete label "why? not" from alice/x?`}, prompt: &askingPrompt{}, code: clierr.Usage, message: `confirmation required to delete label "why? not" from alice/x; rerun with --yes`},
		{name: "keeps the question mark of a URL", c: Confirmation{Prompt: "Delete webhook 3 (https://hooks.test/x?…) from alice/x?"}, prompt: &askingPrompt{}, code: clierr.Usage, message: "confirmation required to delete webhook 3 (https://hooks.test/x?…) from alice/x; rerun with --yes"},
		{name: "drops escape sequences from server names", c: Confirmation{Prompt: "Delete attachment \"x\x1b]0;PWNED\x07\"?"}, prompt: &askingPrompt{}, code: clierr.Usage, message: `confirmation required to delete attachment "x]0;PWNED"; rerun with --yes`},
		{name: "without a terminal or action", c: Confirmation{Prompt: "Go?", DefaultYes: true}, prompt: &askingPrompt{}, code: clierr.Usage, message: "confirmation required; rerun with --yes"},
		{name: "accepted", c: deleteLabel, streams: terminal, prompt: &askingPrompt{accept: true}, defaults: []bool{false}},
		{name: "passes the default", c: Confirmation{Prompt: "Create pull request?", DefaultYes: true}, streams: terminal, prompt: &askingPrompt{accept: true}, defaults: []bool{true}},
		{name: "declined", c: deleteLabel, streams: terminal, prompt: &askingPrompt{}, code: clierr.Canceled, message: "canceled", defaults: []bool{false}},
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
			require.Equal(t, tc.rejected, tc.prompt.rejected)
			require.Equal(t, tc.defaults, tc.prompt.defaults)
		})
	}
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
	asked, err := RequireInput(context.Background(), &value, "Color", hex, errors.New("label create requires --color"), streams, policy, asker)
	require.NoError(t, err)
	require.True(t, asked)
	require.Equal(t, "d73a4a", value)
	require.Equal(t, []string{"", "red"}, asker.rejected)
	require.Equal(t, []string{"Color (required):"}, asker.titles)

	asked, err = RequireInput(context.Background(), &value, "Color", hex, errors.New("label create requires --color"), Streams{}, policy, asker)
	require.NoError(t, err, "a supplied value needs no prompt")
	require.False(t, asked)
	value = ""
	_, err = RequireInput(context.Background(), &value, "Color", hex, errors.New("label create requires --color"), Streams{}, policy, asker)
	require.EqualError(t, err, "label create requires --color")
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

func TestFormsFollowTheColorSettings(t *testing.T) {
	for _, colored := range []bool{true, false} {
		p, rendered := accessiblePrompter(t, "\n")
		p.Color = func() bool { return colored }
		_, err := p.ConfirmDefault(context.Background(), "Delete?", false)
		require.NoError(t, err)
		require.Contains(t, rendered.String(), "Delete? [y/N]")
		require.Equal(t, colored, strings.Contains(rendered.String(), "\x1b["), "accessible, colored %v: %q", colored, rendered)
	}

	// A terminal that takes 24-bit color, as the form sees it.
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	for _, colored := range []bool{true, false} {
		p, rendered, ctx := screenPrompter(t, keyStep{"Delete?", "\r"})
		p.Color = func() bool { return colored }
		_, err := p.ConfirmDefault(ctx, "Delete?", false)
		require.NoError(t, err)
		require.Equal(t, colored, strings.Contains(rendered.String(), "38;2;"), "interactive, colored %v", colored)
	}

	// Color always overrides NO_COLOR, so a form colored by the settings keeps its colors under it.
	t.Setenv("NO_COLOR", "1")
	p, rendered, ctx := screenPrompter(t, keyStep{"Delete?", "\r"})
	p.Color = func() bool { return true }
	_, err := p.ConfirmDefault(ctx, "Delete?", false)
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "38;2;", "interactive under NO_COLOR")
}

// screen is the terminal a form draws on, which a test reads while the form runs.
type screen struct {
	mu   sync.Mutex
	text strings.Builder
}

func (s *screen) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.Write(b)
}

func (s *screen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String()
}

// csiSequence matches the escape sequences, such as colors from the environment, that split the
// text a step waits for.
var csiSequence = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")

// newestReader serves keys to the latest Read caller. A finished form's read cannot be canceled on
// every platform, such as a pipe on Windows, and would otherwise swallow the keys of the next form.
type newestReader struct {
	mu     sync.Mutex
	buf    []byte
	newest int
	closed bool
}

func (r *newestReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	r.newest++
	id := r.newest
	r.mu.Unlock()
	for {
		r.mu.Lock()
		switch {
		case id != r.newest:
			if r.closed {
				r.mu.Unlock()
				return 0, io.EOF
			}
		case len(r.buf) > 0:
			n := copy(p, r.buf)
			r.buf = r.buf[n:]
			r.mu.Unlock()
			return n, nil
		case r.closed:
			r.mu.Unlock()
			return 0, io.EOF
		}
		r.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
}

func (r *newestReader) WriteString(s string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, s...)
	return len(s), nil
}

func (r *newestReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// keyStep sends key once the form drew show after the previous step's key.
type keyStep struct{ show, key string }

// screenPrompter is a prompter whose terminal sends the key of each step once the form shows the
// step's text, so the form handled the keys before, and the focus or form change they cause,
// whatever the speed of the machine. Its input gives keys to the newest reader only, so the next
// form gets the keys that follow. Its context ends a form left waiting for keys that never come.
func screenPrompter(t *testing.T, steps ...keyStep) (*FormPrompter, *screen, context.Context) {
	t.Helper()
	in := &newestReader{}
	out := in
	t.Cleanup(func() { _ = in.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	drawn := &screen{}
	go func() {
		shown := 0
		for _, step := range steps {
			for !strings.Contains(csiSequence.ReplaceAllString(drawn.String()[shown:], ""), step.show) {
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Millisecond):
				}
			}
			shown = len(drawn.String())
			_, _ = out.WriteString(step.key)
		}
	}()
	size := func() (int, int, error) { return 80, 24, nil }
	return &FormPrompter{Streams: Streams{In: in, Out: &bytes.Buffer{}, ErrOut: drawn, InTTY: true, ErrTTY: true}, size: size}, drawn, ctx
}

func TestAccessibleFormsEndOnInterrupt(t *testing.T) {
	t.Setenv("TERM", "dumb")
	in, out, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = in.Close(), out.Close() })
	drawn := &screen{}
	p := &FormPrompter{Streams: Streams{In: in, Out: &bytes.Buffer{}, ErrOut: drawn, InTTY: true, ErrTTY: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.Ask(ctx, "Title (required):", nil)
		done <- err
	}()
	require.Eventually(t, func() bool { return strings.Contains(drawn.String(), "Title (required):") }, 10*time.Second, 5*time.Millisecond)
	cancel() // Ctrl-C, while the form waits for a line
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.True(t, strings.HasSuffix(drawn.String(), "\n"), "the prompt line ends after ^C: %q", drawn)
	case <-time.After(10 * time.Second):
		t.Fatal("the form kept waiting for an answer after Ctrl-C")
	}
}

func TestAccessibleFormsCancelOnEndOfInputOrEsc(t *testing.T) {
	for _, input := range []string{"", "\x1b\n", "\x1b\r\n"} {
		p, rendered := accessiblePrompter(t, input)
		values, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
		require.ErrorIs(t, err, ErrCanceled, "%q", input)
		require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
		require.Empty(t, values.Title)
		require.NotContains(t, rendered.String(), BodyTitle, "%q: no prompt follows the end", input)
		require.True(t, strings.HasSuffix(rendered.String(), "\n"), "%q: the prompt line ends", input)
	}
}

func TestAccessibleSecretsCancelOnEndOfInputOrEsc(t *testing.T) {
	// huh read secrets from the terminal itself, where Esc became the secret and Ctrl-D was ignored.
	for _, input := range []string{"", "\x1b\n", "\x1b\r\n"} {
		p, rendered := accessiblePrompter(t, input)
		value, err := p.SecretInput(context.Background(), "Paste the value of TOKEN:")
		require.ErrorIs(t, err, ErrCanceled, "%q", input)
		require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
		require.Empty(t, value)
		require.True(t, strings.HasSuffix(rendered.String(), "\n"), "%q: the prompt line ends", input)
	}
}

func TestAccessibleSecretsAreReadAndValidated(t *testing.T) {
	p, rendered := accessiblePrompter(t, "s3cret\r\n")
	value, err := p.SecretInput(context.Background(), "Paste the value of TOKEN:")
	require.NoError(t, err)
	require.Equal(t, "s3cret", value)
	require.Contains(t, rendered.String(), "Paste the value of TOKEN: \n")
	require.NotContains(t, rendered.String(), "s3cret")

	p, rendered = accessiblePrompter(t, "\nabc\n")
	answers := LoginAnswers{Method: LoginToken}
	require.NoError(t, p.Login(context.Background(), LoginQuestions{TokenHint: func(string) string { return "" }}, &answers))
	require.Equal(t, "abc", answers.Token)
	require.Equal(t, 2, strings.Count(rendered.String(), "Paste an access token"), "asked again: %q", rendered)
	require.Contains(t, rendered.String(), "paste the token")
}

func TestAccessibleChoiceCancelsOnEndOfInput(t *testing.T) {
	// huh takes the end of input after an invalid answer for that answer, and fails on it.
	p, rendered := linePrompter(t, "x\n")
	_, err := p.Choose(context.Background(), "Method:", []Choice{{Label: "Token", Value: "token"}, {Label: "OAuth", Value: "oauth"}}, "")
	require.ErrorIs(t, err, ErrCanceled)
	require.Equal(t, 2, strings.Count(rendered.String(), "Enter a number"), "no prompt follows the end")
}

func TestPromptAbortIsCanceled(t *testing.T) {
	err := promptError(context.Background(), huh.ErrUserAborted)
	require.ErrorIs(t, err, ErrCanceled)
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
}

func TestEscapeCancelsFormsLikeCtrlC(t *testing.T) {
	// Esc cancels with code 3; Ctrl-C interrupts with code 130, as it does outside forms.
	for key, code := range map[string]int{"\x1b": clierr.Canceled, "\x03": clierr.Interrupted} {
		p, _ := testPrompter(strings.NewReader("abc" + key))
		_, err := p.Ask(context.Background(), "Label name", nil)
		require.ErrorIs(t, err, ErrCanceled, "%q", key)
		require.Equal(t, code, clierr.ExitCode(err), "%q", key)

		p, _ = testPrompter(strings.NewReader(key))
		_, err = p.ConfirmDefault(context.Background(), "Delete?", true)
		require.ErrorIs(t, err, ErrCanceled, "%q", key)
		require.Equal(t, code, clierr.ExitCode(err), "%q", key)
	}
}

func TestEscapeLeavesASelectFilterWithoutCanceling(t *testing.T) {
	var value string
	form := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Options(huh.NewOptions("bug", "docs")...).Value(&value)))
	view(form, 80, 24)
	esc := gt.KeyPressMsg{Code: gt.KeyEscape}
	var interrupted atomic.Bool
	filter := escapeCancels(form, &interrupted)
	require.Equal(t, gt.KeyPressMsg{Code: 'c', Mod: gt.ModCtrl}, filter(nil, esc), "Esc cancels the form")
	require.False(t, interrupted.Load(), "Esc cancels without interrupting")
	form.Update(gt.KeyPressMsg{Code: '/', Text: "/"})
	require.Equal(t, esc, filter(nil, esc), "Esc leaves the filter")
}

func TestStreamsEscapes(t *testing.T) {
	require.True(t, Streams{OutTTY: true, ErrTTY: true}.OutEscapes())
	require.True(t, Streams{OutTTY: true, ErrTTY: true}.ErrEscapes())
	require.False(t, Streams{ErrTTY: true}.OutEscapes())
	require.False(t, Streams{OutTTY: true}.ErrEscapes())
	require.False(t, Streams{OutTTY: true, OutPlain: true}.OutEscapes())
	require.False(t, Streams{ErrTTY: true, ErrPlain: true}.ErrEscapes())
}

func TestStreamsRestoreTheTerminalsSystemChanged(t *testing.T) {
	// The console mode itself changes only on Windows; this checks that Restore undoes every change.
	var undone []string
	s := Streams{restore: []func(){func() { undone = append(undone, "stderr") }, func() { undone = append(undone, "stdout") }}}
	s.Restore()
	require.Equal(t, []string{"stderr", "stdout"}, undone)
	require.NotPanics(t, Streams{}.Restore, "streams that changed nothing")
	require.NotPanics(t, System().Restore)
}

func TestStreamsNeverColorATerminalShowingEscapesAsText(t *testing.T) {
	forced := env(map[string]string{"CLICOLOR_FORCE": "1"})
	require.True(t, Streams{OutTTY: true}.OutColor("auto", forced))
	require.True(t, Streams{}.ErrColor("auto", forced), "forced color reaches redirected diagnostics")
	for _, preference := range []string{"auto", "always"} {
		require.False(t, Streams{OutTTY: true, OutPlain: true}.OutColor(preference, forced), preference)
		require.False(t, Streams{ErrTTY: true, ErrPlain: true}.ErrColor(preference, forced), preference)
	}
}
