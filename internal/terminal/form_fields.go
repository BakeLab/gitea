// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/huh/v2"
)

// BodyTitle labels the markdown body of issues, pull requests and comments.
const BodyTitle = "Body (markdown):"

// maxVisibleOptions bounds how many options a multi-select shows before it scrolls.
const maxVisibleOptions = 8

// notBlank rejects empty and whitespace-only answers, which huh.ValidateNotEmpty accepts.
func notBlank(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("input cannot be empty")
	}
	return nil
}

// titleField is the required one-line title of an issue, pull request or similar resource.
func titleField(value *string) *huh.Input {
	return huh.NewInput().Title("Title (required):").Value(value).Validate(notBlank)
}

// bodyField is an inline multi-line markdown field. It has no external editor: forms run the
// configured editor themselves, with private drafts, and never fall back to an unconfigured one.
func bodyField(title string, value *string) *huh.Text {
	return huh.NewText().Title(title).Value(value).ExternalEditor(false)
}

// fieldText is how a multi-line field shows seed: its textarea turns every CR and LF into a line
// break and a tab into four spaces, and drops other control characters.
func fieldText(seed string) string {
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n') {
			return -1
		}
		return r
	}, strings.NewReplacer("\r", "\n", "\t", "    ").Replace(seed))
}

// fieldResult is the answer of a multi-line field seeded with seed: seed itself when the user
// changed nothing, so a plain Enter does not rewrite existing text such as CRLF line endings.
func fieldResult(seed, answer string) string {
	if answer == fieldText(seed) {
		return seed
	}
	return answer
}

// multiSelect lists every option, scrolling past maxVisibleOptions. Without an explicit height,
// huh sizes the list to its options minus the title line and hides the last option.
func multiSelect[T comparable](title string, options []huh.Option[T], value *[]T) *huh.MultiSelect[T] {
	return huh.NewMultiSelect[T]().Title(title).Options(options...).Value(value).Height(min(len(options), maxVisibleOptions) + 1)
}
