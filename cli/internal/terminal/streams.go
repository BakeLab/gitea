// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"gitea.dev/cli/internal/clierr"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// Streams records injected IO and independently detected terminal capabilities.
type Streams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
	InTTY  bool
	OutTTY bool
	ErrTTY bool
	// OutPlain and ErrPlain mark a terminal that shows escape sequences as text, such as a Windows
	// console without virtual terminal processing: output on it stays uncolored.
	OutPlain, ErrPlain bool
	// Width is the number of columns human output on Out fits in; 0 means unlimited.
	Width int
	// restore undoes, in order, what System changed on the terminals.
	restore []func()
}

// System binds streams to the current process and detects each terminal, enabling escape sequences
// on the terminals that need it. Restore undoes that.
func System() Streams {
	s := Streams{
		In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr,
		InTTY: term.IsTerminal(int(os.Stdin.Fd())), OutTTY: term.IsTerminal(int(os.Stdout.Fd())), ErrTTY: term.IsTerminal(int(os.Stderr.Fd())),
		Width: terminalWidth(int(os.Stdout.Fd()), os.Getenv),
	}
	s.OutPlain = s.OutTTY && !s.enableEscapes(os.Stdout)
	s.ErrPlain = s.ErrTTY && !s.enableEscapes(os.Stderr)
	return s
}

// enableEscapes enables escape sequences on the terminal behind file, remembering how to restore it.
func (s *Streams) enableEscapes(file *os.File) bool {
	enabled, restore := enableEscapes(file)
	if restore != nil {
		s.restore = slices.Insert(s.restore, 0, restore)
	}
	return enabled
}

// Restore undoes what System changed on the terminals, such as the mode of a Windows console,
// which would otherwise keep the change after gt exits.
func (s Streams) Restore() {
	for _, restore := range s.restore {
		restore()
	}
}

// OutEscapes reports whether Out is a terminal that interprets escape sequences.
func (s Streams) OutEscapes() bool { return s.OutTTY && !s.OutPlain }

// ErrEscapes reports whether ErrOut is a terminal that interprets escape sequences.
func (s Streams) ErrEscapes() bool { return s.ErrTTY && !s.ErrPlain }

// OutColor resolves the color preference for Out with ColorEnabled. A terminal that shows escape
// sequences as text stays uncolored even when color is forced, as color would only garble it.
func (s Streams) OutColor(preference string, getenv func(string) string) bool {
	return !s.OutPlain && ColorEnabled(preference, s.OutEscapes(), getenv)
}

// ErrColor is OutColor for ErrOut.
func (s Streams) ErrColor(preference string, getenv func(string) string) bool {
	return !s.ErrPlain && ColorEnabled(preference, s.ErrEscapes(), getenv)
}

// terminalWidth returns the width of the terminal at fd, then $COLUMNS, then 80. A pty without a
// window size, such as `script` run without a terminal, reports a width of 0.
func terminalWidth(fd int, getenv func(string) string) int {
	if width, _, err := term.GetSize(fd); err == nil && width > 0 {
		return width
	}
	if width, err := strconv.Atoi(getenv("COLUMNS")); err == nil && width > 0 {
		return width
	}
	return 80
}

// Policy controls input interaction independently of supplied flag count.
type Policy struct{ Enabled, NoInput, Machine, Interactive bool }

// CanPrompt requires terminal input and diagnostics, without requiring terminal stdout.
func (p Policy) CanPrompt(s Streams) bool {
	return p.Enabled && !p.NoInput && (!p.Machine || p.Interactive) && s.InTTY && s.ErrTTY
}

// Validate rejects an explicit interactive request incompatible with terminal policy.
func (p Policy) Validate(s Streams) error {
	if p.Interactive && !p.CanPrompt(s) {
		return clierr.Wrap(clierr.Usage, errors.New("--interactive requires terminal stdin/stderr, enabled prompting, and no --no-input"))
	}
	return nil
}

// ErrCanceled reports a declined confirmation or an aborted prompt; it exits with code 3.
var ErrCanceled = clierr.Wrap(clierr.Canceled, errors.New("canceled"))

// ErrInterrupted reports a form left with Ctrl-C, which exits with code 130 as Ctrl-C does
// elsewhere. It is an ErrCanceled, so callers handle it as any aborted prompt.
var ErrInterrupted = clierr.Wrap(clierr.Interrupted, ErrCanceled)

// Prompter abstracts cancellable input collection and confirmation.
type Prompter interface {
	// Ask collects one line, re-prompting inline until validate, when set, accepts it.
	Ask(ctx context.Context, title string, validate func(string) error) (string, error)
	// AskText collects multi-line markdown, starting from initial.
	AskText(ctx context.Context, title, initial string) (string, error)
	// SecretInput collects a value without displaying it.
	SecretInput(ctx context.Context, title string) (string, error)
	// ConfirmDefault asks a yes/no question that Enter answers with defaultYes.
	ConfirmDefault(ctx context.Context, title string, defaultYes bool) (bool, error)
}

// FormPrompter uses cancellable terminal forms with explicitly injected streams.
type FormPrompter struct {
	Streams Streams
	// Color reports whether forms are colored, following gt's color settings for ErrOut; nil colors
	// them as the terminal allows.
	Color func() bool
	// size reports the size of the terminal that forms render on; nil queries ErrOut.
	size func() (width, height int, err error)
}

func promptError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCanceled
	}
	return err
}

// errNoTerminalSize refuses interactive forms on a terminal without a size, such as a pty that
// `script` opens: a zero size panics in bubbles. Accessible forms read line by line and need no size.
var errNoTerminalSize = clierr.Wrap(clierr.Usage, errors.New("terminal size unknown; pass the values as flags, or set TERM=dumb to answer line by line"))

func (p *FormPrompter) runForm(ctx context.Context, form *huh.Form) error {
	size := p.size
	if file, ok := p.Streams.ErrOut.(*os.File); ok && size == nil {
		size = func() (int, int, error) { return term.GetSize(int(file.Fd())) }
	}
	var interrupted atomic.Bool
	options := []tea.ProgramOption{tea.WithFilter(escapeCancels(form, &interrupted))}
	if size != nil {
		width, height, err := size()
		if err == nil && (width <= 0 || height <= 0) && !accessible() {
			return errNoTerminalSize
		}
		if err == nil && p.size != nil {
			// The program finds the size of a terminal itself, but not of the one p.size describes.
			options = append(options, tea.WithWindowSize(width, height))
		}
	}
	out := p.Streams.ErrOut
	if p.Color != nil && !p.Color() {
		// An accessible form writes its prompts to out itself, so strip their styles there; an
		// interactive one draws through the program, which drops colors but keeps bold and reverse
		// text for the focus.
		if accessible() {
			out = &colorprofile.Writer{Forward: out, Profile: colorprofile.NoTTY}
		}
		options = append(options, tea.WithColorProfile(colorprofile.Ascii))
	} else if p.Color != nil {
		// gt's settings color the form, so NO_COLOR, which color always overrides, must not make
		// the program drop the colors.
		options = append(options, tea.WithColorProfile(colorprofile.Detect(out, withoutNoColor(os.Environ()))))
	}
	form = form.WithProgramOptions(options...)
	if accessible() {
		return p.runAccessible(ctx, form, out)
	}
	err := promptError(ctx, form.WithInput(p.Streams.In).WithOutput(out).RunWithContext(ctx))
	if errors.Is(err, ErrCanceled) && interrupted.Load() {
		return ErrInterrupted
	}
	return err
}

// withoutNoColor is environ without NO_COLOR, which gt's color settings have already weighed.
func withoutNoColor(environ []string) []string {
	return slices.DeleteFunc(environ, func(variable string) bool { return strings.HasPrefix(variable, "NO_COLOR=") })
}

// runAccessible runs form in accessible mode, where huh reads each answer without watching ctx and
// takes the end of input for an empty answer, or panics on it at a choice. It returns as soon as
// ctx is done, leaving the read to end with gt, and cancels the form when the input ends, such as
// with Ctrl-D, or when a line holds only Esc, as Esc cancels an interactive form. It restores the
// terminal, whose echo a secret field may have left off.
func (p *FormPrompter) runAccessible(ctx context.Context, form *huh.Form, out io.Writer) error {
	answers := &answerReader{ctx: ctx, in: p.Streams.In, ended: make(chan struct{})}
	var in io.Reader = answers
	if file, ok := p.Streams.In.(fileDescriptor); ok {
		// A secret field hides its answer on the terminal.
		in = struct {
			*answerReader
			fileDescriptor
		}{answers, file}
		if state, err := term.GetState(int(file.Fd())); err == nil {
			defer func() { _ = term.Restore(int(file.Fd()), state) }()
		}
	}
	done := make(chan error, 1)
	go func() { done <- form.WithInput(in).WithOutput(out).RunWithContext(ctx) }()
	select {
	case err := <-done:
		return promptError(ctx, err)
	case <-answers.ended:
		// Ends the line of the prompt the input ended at.
		fmt.Fprintln(out)
		return ErrCanceled
	case <-ctx.Done():
		// Ends the line of the prompt Ctrl-C left, after the ^C the terminal shows.
		fmt.Fprintln(out)
		return ctx.Err()
	}
}

// fileDescriptor is a terminal a secret field can hide its answer on.
type fileDescriptor interface{ Fd() uintptr }

// answerReader hands an accessible form one line per read and reads no further, so every prompt
// gets the line typed for it. Once the input ended, or a line held only Esc, it closes ended, and
// from then on, as once ctx is done, a read never returns: the form left running takes no more
// answers and writes nothing more.
type answerReader struct {
	ctx   context.Context
	in    io.Reader
	line  []byte
	ended chan struct{}
}

func (r *answerReader) Read(b []byte) (int, error) {
	if len(r.line) == 0 {
		line, ended := readLine(r.in)
		if ended {
			close(r.ended)
		}
		r.line = line
	}
	if len(r.line) == 0 || r.ctx.Err() != nil {
		select {}
	}
	n := copy(b, r.line)
	r.line = r.line[n:]
	return n, nil
}

// readLine reads up to and including the next line break a byte at a time, so it takes nothing
// from the input past it. It reports the end of the answers at the end of the input or for a line
// holding only Esc.
func readLine(in io.Reader) ([]byte, bool) {
	var line []byte
	var c [1]byte
	for {
		n, err := in.Read(c[:])
		if n == 1 {
			line = append(line, c[0])
			if c[0] == '\n' {
				break
			}
		}
		if err != nil {
			return line, len(line) == 0
		}
	}
	if strings.TrimRight(string(line), "\r\n") == "\x1b" {
		return nil, true
	}
	return line, false
}

// accessible reports whether forms run in huh's accessible mode, which reads answers line by line
// instead of drawing the form: huh uses it on a dumb terminal, as screen readers need.
func accessible() bool { return os.Getenv("TERM") == "dumb" }

// escapeCancels turns Esc into Ctrl-C, so it cancels form like Ctrl-C does, unless the focused
// field uses Esc itself, such as a select leaving its filter. It sets interrupted when Ctrl-C
// itself is pressed, which the terminal sends as a key while a form runs instead of interrupting gt.
func escapeCancels(form *huh.Form, interrupted *atomic.Bool) func(tea.Model, tea.Msg) tea.Msg {
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		press, ok := msg.(tea.KeyPressMsg)
		if ok && press.String() == "ctrl+c" {
			interrupted.Store(true)
		}
		if !ok || press.String() != "esc" {
			return msg
		}
		if field := form.GetFocusedField(); field != nil {
			for _, binding := range field.KeyBinds() {
				if key.Matches(press, binding) {
					return msg
				}
			}
		}
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
}

// Ask collects one line, re-prompting inline until validate accepts the trimmed answer.
func (p *FormPrompter) Ask(ctx context.Context, title string, validate func(string) error) (string, error) {
	var value string
	input := huh.NewInput().Title(title).Value(&value)
	if validate != nil {
		input = input.Validate(func(s string) error { return validate(strings.TrimSpace(s)) })
	}
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(input)))
	return strings.TrimSpace(value), err
}

// AskText collects multi-line markdown in an inline field, starting from initial.
func (p *FormPrompter) AskText(ctx context.Context, title, initial string) (string, error) {
	value := initial
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(bodyField(title, &value))))
	return fieldResult(initial, value), err
}

// SecretInput collects a value without displaying it on the terminal.
func (p *FormPrompter) SecretInput(ctx context.Context, title string) (string, error) {
	var value string
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(secretField(title, "", &value, nil))))
	return value, err
}

// ConfirmDefault asks a yes/no question that Enter answers with defaultYes.
func (p *FormPrompter) ConfirmDefault(ctx context.Context, title string, defaultYes bool) (bool, error) {
	accepted := defaultYes
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&accepted))))
	return accepted, err
}

// RequireInput fills a missing value from a prompt titled label and marked "(required)" like the
// form fields, re-prompting inline until the answer is non-empty and validate, when set, accepts
// it. When prompts are unavailable it returns missing as a usage error. It reports whether it
// prompted.
func RequireInput(ctx context.Context, value *string, label string, validate func(string) error, missing error, streams Streams, policy Policy, prompt Prompter) (bool, error) {
	if *value != "" {
		return false, nil
	}
	if !policy.CanPrompt(streams) {
		return false, clierr.Wrap(clierr.Usage, missing)
	}
	entered, err := prompt.Ask(ctx, label+" (required):", func(s string) error {
		if s == "" {
			return errors.New("a value is required")
		}
		if validate != nil {
			return validate(s)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	*value = entered
	return true, nil
}

// Confirmation describes the question asked before an action.
type Confirmation struct {
	// Prompt asks about the action in sentence case: `Delete label "bug" from alice/x?`.
	Prompt string
	// DefaultYes lets Enter accept, for constructive actions the user just asked for, such as
	// submitting a form they filled in. Destructive actions keep the default no.
	DefaultYes bool
	// Typed, when set, must be typed to confirm, for irreversible actions such as deleting a repository.
	Typed string
	// NonTTY names the action in the error returned when no prompt is possible and --yes is
	// missing: `delete label "bug" from alice/x`. Without it, a confirmation that defaults to no
	// names the Prompt up to its question mark.
	NonTTY string
}

// questionEnd returns the index of the first question mark outside double quotes that ends the
// prompt or a sentence, or -1, so neither a quoted name such as "why? not" nor a URL query ends
// the question.
func questionEnd(prompt string) int {
	quoted := false
	for i, r := range prompt {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '?' && !quoted && (i == len(prompt)-1 || prompt[i+1] == ' '):
			return i
		}
	}
	return -1
}

// ConfirmAction asks c unless yes is set. A declined or aborted confirmation returns ErrCanceled.
func ConfirmAction(ctx context.Context, c Confirmation, yes bool, streams Streams, policy Policy, prompt Prompter) error {
	if yes {
		return nil
	}
	// Names in the question come from the server, so keep control characters off the terminal.
	c.Prompt, c.NonTTY = SafeLine(c.Prompt), SafeLine(c.NonTTY)
	if !policy.CanPrompt(streams) {
		action := c.NonTTY
		if question := questionEnd(c.Prompt); action == "" && question > 0 && !c.DefaultYes {
			action = strings.ToLower(c.Prompt[:1]) + c.Prompt[1:question]
		}
		if action != "" {
			action = " to " + action
		}
		return clierr.Wrap(clierr.Usage, fmt.Errorf("confirmation required%s; rerun with --yes", action))
	}
	if c.Typed != "" {
		_, err := prompt.Ask(ctx, c.Prompt+" Type "+c.Typed+" to confirm:", func(s string) error {
			if !strings.EqualFold(strings.TrimSpace(s), c.Typed) {
				return fmt.Errorf("type %s to confirm", c.Typed)
			}
			return nil
		})
		return err
	}
	accepted, err := prompt.ConfirmDefault(ctx, c.Prompt, c.DefaultYes)
	if err == nil && !accepted {
		err = ErrCanceled
	}
	return err
}
