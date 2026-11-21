// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// BodyTitle labels the markdown body of issues, pull requests and comments.
const BodyTitle = "Body (markdown):"

// maxVisibleOptions bounds how many options a multi-select shows before it scrolls.
const maxVisibleOptions = 8

// notBlank rejects empty and whitespace-only answers, which huh.ValidateNotEmpty accepts, and
// answers a field shows as nothing, such as the control characters an accessible form reads as
// typed, like Esc.
func notBlank(s string) error {
	if strings.TrimSpace(inputText(s)) == "" {
		return errors.New("input cannot be empty")
	}
	return nil
}

// requiredText is notBlank for the field holding value. In accessible mode a field validates the
// typed line and keeps value when it is empty, so an empty line is checked as the value it keeps,
// as the field shows it. An interactive field keeps value equal to its text.
func requiredText(value *string) func(string) error {
	return func(s string) error {
		if s == "" {
			s = inputText(*value)
		}
		return notBlank(s)
	}
}

// titleField is the required one-line title of an issue, pull request or similar resource.
func titleField(value *string) *lineInput {
	return requiredLineField("Title", value)
}

// requiredLineField is a one-line field marked as required, like RequireInput's prompts, that
// refuses an empty answer and an answer that a non-nil check rejects.
func requiredLineField(label string, value *string, check ...func(string) error) *lineInput {
	required := requiredText(value)
	return lineField(label+" (required):", value, false).Validate(func(s string) error {
		if err := required(s); err != nil {
			return err
		}
		// An accessible form checks the typed line, which is empty when it keeps the value.
		answer := strings.TrimSpace(cmp.Or(s, inputText(*value)))
		for _, c := range check {
			if c != nil {
				if err := c(answer); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// clearAnswer is the accessible answer that clears an optional one-line field.
const clearAnswer = "-"

// lineField is a one-line input; an optional one may be left empty.
func lineField(title string, value *string, optional bool) *lineInput {
	return &lineInput{Input: huh.NewInput().Title(title).Value(value), title: title, value: value, optional: optional}
}

// lineInput is a one-line input that, in accessible mode, shows its current value, which an empty
// answer keeps, and on an optional field takes clearAnswer to clear it: huh shows neither, and
// takes an empty answer, or one of only spaces, for the current value.
type lineInput struct {
	*huh.Input
	title    string
	value    *string
	validate func(string) error
	optional bool
}

// Validate sets the validation of the field's answer.
func (l *lineInput) Validate(validate func(string) error) *lineInput {
	l.validate = validate
	l.Input.Validate(validate)
	return l
}

// RunAccessible asks with the current value in the title, as in `Due date [2027-01-15; "-" clears]:`.
func (l *lineInput) RunAccessible(w io.Writer, r io.Reader) error {
	if current := strings.TrimSpace(SafeLine(*l.value)); current != "" {
		if l.optional {
			current += fmt.Sprintf("; %q clears", clearAnswer)
		}
		title, colon := strings.CutSuffix(l.title, ":")
		title += " [" + current + "]"
		if colon {
			title += ":"
		}
		l.Input.Title(title)
	}
	l.Input.Validate(func(s string) error {
		if l.optional && strings.TrimSpace(s) == clearAnswer || l.validate == nil {
			return nil
		}
		return l.validate(s)
	})
	err := l.Input.RunAccessible(w, r)
	if l.optional && strings.TrimSpace(*l.value) == clearAnswer {
		*l.value = ""
	}
	return err
}

// secretField is a one-line input that does not show what is typed. Its description shows only in
// an interactive form.
func secretField(title, description string, value *string, validate func(string) error) *secretInput {
	input := huh.NewInput().Title(title).Description(description).EchoMode(huh.EchoModePassword).Value(value)
	if validate != nil {
		input.Validate(validate)
	}
	return &secretInput{Input: input, title: title, value: value, validate: validate}
}

// secretInput is a password input that reads its accessible answer like other accessible fields,
// where huh reads it from the terminal itself: there Esc and Ctrl-D would not cancel the form, and
// Ctrl-C would leave the terminal without echo.
type secretInput struct {
	*huh.Input
	title    string
	value    *string
	validate func(string) error
}

// RunAccessible reads the answer as a line with the terminal's echo off, asking again until the
// validation accepts it.
func (s *secretInput) RunAccessible(w io.Writer, r io.Reader) error {
	for {
		fmt.Fprint(w, SafeLine(s.title)+" ")
		answer := readSecret(r)
		fmt.Fprintln(w) // The terminal did not show the line break either.
		if s.validate != nil {
			if err := s.validate(answer); err != nil {
				fmt.Fprintln(w, err)
				continue
			}
		}
		*s.value = answer
		return nil
	}
}

// readSecret reads one line from r, hiding it when r is a terminal.
func readSecret(r io.Reader) string {
	if file, ok := r.(fileDescriptor); ok {
		if show, err := hideInput(file.Fd()); err == nil {
			defer show()
		}
	}
	line, _ := readLine(r)
	return strings.TrimRight(string(line), "\r\n")
}

// inputText is how a one-line field shows seed: its input turns a tab or line break each into a
// space and drops other control characters.
func inputText(seed string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\r' || r == '\n':
			return ' '
		case r == utf8.RuneError || unicode.IsControl(r):
			return -1
		}
		return r
	}, seed)
}

// titleResult is the answer of a title field seeded with seed: seed itself when the user changed
// nothing, so a plain Enter does not rewrite an existing title, else the answer without surrounding space.
// An interactive field answers with seed as it showed it, an accessible one with seed itself.
func titleResult(seed, answer string) string {
	if answer == seed || answer == inputText(seed) {
		return seed
	}
	return strings.TrimSpace(answer)
}

// bodyField is an inline multi-line markdown field. It has no external editor: forms run the
// configured editor themselves, with private drafts, and never fall back to an unconfigured one.
// It seeds the field with LF line breaks, since its textarea turns CR and LF each into a line break,
// and starts with the cursor at the beginning, so an existing body shows its first lines rather
// than its end. fieldResult restores what the textarea changed in the lines the user kept.
func bodyField(title string, value *string) *bodyText {
	*value = lineBreaks(*value)
	text := huh.NewText().Title(title).Value(value).ExternalEditor(false)
	return &bodyText{Text: text, title: title, value: value, newLine: huh.NewDefaultKeyMap().Text.NewLine}
}

// withEditor lets ctrl+e open the configured editor command on the field, and says so in its
// title. Accessible mode reads the text line by line, where no key opens an editor, so there the
// field stays as it is.
func (b *bodyText) withEditor(command string) *bodyText {
	args, ok := commandArgs(command)
	if !ok || accessible() {
		return b
	}
	b.Title(strings.TrimSuffix(b.title, "):") + ", ctrl+e opens your editor):")
	b.ExternalEditor(true).Editor(args...).EditorExtension("md")
	return b
}

// bodyText is a huh text field whose cursor starts at the beginning of its value: the textarea
// leaves it at the end of the value it was seeded with. In accessible mode it reads several lines.
type bodyText struct {
	*huh.Text
	focused  bool
	title    string
	value    *string
	validate func(string) error
	newLine  key.Binding
}

// Title sets the title of the field.
func (b *bodyText) Title(title string) *huh.Text {
	b.title = title
	return b.Text.Title(title)
}

// Validate sets the validation of the field's text.
func (b *bodyText) Validate(validate func(string) error) *huh.Text {
	b.validate = validate
	return b.Text.Validate(validate)
}

// RunAccessible reads the text line by line, where huh would read a single line and replace the
// whole text with it. It shows the current text, keeps it on an empty first line, and otherwise
// takes the lines typed up to one holding only ".". When the input ends first, it changes nothing.
func (b *bodyText) RunAccessible(w io.Writer, r io.Reader) error {
	fmt.Fprintln(w, SafeLine(b.title))
	keep := "leaves it empty"
	if current := strings.TrimRight(SafeText(*b.value), "\n"); current != "" {
		fmt.Fprintf(w, "Current text:\n%s\n", current)
		keep = "keeps the current text"
	}
	lines := bufio.NewScanner(r)
	for {
		fmt.Fprintf(w, "Type the text and end it with a line holding only \".\"; an empty first line %s.\n", keep)
		text, typed, ok := readText(lines)
		if !ok {
			return nil
		}
		previous := *b.value
		if typed {
			*b.value = text
		}
		if b.validate == nil {
			return nil
		}
		err := b.validate(*b.value)
		if err == nil {
			return nil
		}
		*b.value = previous
		fmt.Fprintln(w, err)
	}
}

// readText reads lines up to one holding only ".", reporting whether text was typed: an empty first
// line types none. It reports false when the input ended first.
func readText(lines *bufio.Scanner) (text string, typed, ok bool) {
	var read []string
	for lines.Scan() {
		switch line := lines.Text(); {
		case line == "" && len(read) == 0:
			return "", false, true
		case line == ".":
			return strings.Join(read, "\n"), true, true
		default:
			read = append(read, line)
		}
	}
	return "", false, false
}

// Focus focuses the field and, the first time, moves the cursor to the beginning of the value.
func (b *bodyText) Focus() tea.Cmd {
	cmd := b.Text.Focus()
	if !b.focused {
		b.focused = true
		b.Text.Update(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	}
	return cmd
}

// WithKeyMap sets the keys of the field.
func (b *bodyText) WithKeyMap(k *huh.KeyMap) huh.Field {
	b.newLine = k.Text.NewLine
	b.Text.WithKeyMap(k)
	return b
}

// Update updates the field and keeps the wrapper in the group, which stores the returned model.
// The textarea stops taking typed line breaks once the text has 99 lines, but takes pasted ones,
// so a typed line break is inserted as pasted.
func (b *bodyText) Update(msg tea.Msg) (huh.Model, tea.Cmd) {
	if press, ok := msg.(tea.KeyPressMsg); ok && key.Matches(press, b.newLine) {
		msg = tea.PasteMsg{Content: "\n"}
	}
	_, cmd := b.Text.Update(msg)
	return b, cmd
}

// lineBreaks turns CRLF and lone CR line endings into LF.
func lineBreaks(s string) string {
	return strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
}

// fieldText is how a multi-line field shows seed: bodyField seeds it with LF line breaks, and its
// textarea turns a tab into four spaces and drops other control characters.
func fieldText(seed string) string {
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n') {
			return -1
		}
		return r
	}, strings.ReplaceAll(lineBreaks(seed), "\t", "    "))
}

// fieldResult is the answer of a multi-line field seeded with seed: seed itself when the user
// changed nothing, so a plain Enter does not rewrite existing text such as CRLF line endings.
// Otherwise every answer line that shows a seed line unchanged gets that line back with its tabs
// and control characters, and lines end in CRLF when the seed's did, so editing one line of a
// body keeps the tabs of a Makefile or Go code block elsewhere in it.
func fieldResult(seed, answer string) string {
	if answer == fieldText(seed) {
		return seed
	}
	seedLines := strings.Split(lineBreaks(seed), "\n")
	shown := make(map[string]string, len(seedLines))
	for _, line := range slices.Backward(seedLines) {
		shown[fieldText(line)] = line
	}
	lines := strings.Split(answer, "\n")
	next := 0
	for i, line := range lines {
		// Prefer the next seed line in order, so lines that show alike keep their own original.
		if j := slices.IndexFunc(seedLines[next:], func(s string) bool { return fieldText(s) == line }); j >= 0 {
			lines[i], next = seedLines[next+j], next+j+1
		} else if original, ok := shown[line]; ok {
			lines[i] = original
		}
	}
	lineBreak := "\n"
	if strings.Contains(seed, "\r\n") {
		lineBreak = "\r\n"
	}
	return strings.Join(lines, lineBreak)
}

// singleSelect lists up to ten options before it scrolls, like the other selects.
func singleSelect[T comparable](title string, options []huh.Option[T], value *T) *huh.Select[T] {
	return huh.NewSelect[T]().Title(title).Options(options...).Height(min(len(options), 10) + 2).Value(value)
}

// multiSelect lists every option, scrolling past maxVisibleOptions. Without an explicit height,
// huh sizes the list to its options minus the title line and hides the last option.
func multiSelect[T comparable](title string, options []huh.Option[T], value *[]T) *huh.MultiSelect[T] {
	return huh.NewMultiSelect[T]().Title(title).Options(options...).Value(value).Height(min(len(options), maxVisibleOptions) + 1)
}
