// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import "charm.land/huh/v2"

// BodyTitle labels the markdown body of issues, pull requests and comments.
const BodyTitle = "Body (markdown):"

// maxVisibleOptions bounds how many options a multi-select shows before it scrolls.
const maxVisibleOptions = 8

// titleField is the required one-line title of an issue, pull request or similar resource.
func titleField(value *string) *huh.Input {
	return huh.NewInput().Title("Title (required):").Value(value).Validate(huh.ValidateNotEmpty())
}

// bodyField is an inline multi-line markdown field. It has no external editor: forms run the
// configured editor themselves, with private drafts, and never fall back to an unconfigured one.
func bodyField(title string, value *string) *huh.Text {
	return huh.NewText().Title(title).Value(value).ExternalEditor(false)
}

// multiSelect lists every option, scrolling past maxVisibleOptions. Without an explicit height,
// huh sizes the list to its options minus the title line and hides the last option.
func multiSelect[T comparable](title string, options []huh.Option[T], value *[]T) *huh.MultiSelect[T] {
	return huh.NewMultiSelect[T]().Title(title).Options(options...).Value(value).Height(min(len(options), maxVisibleOptions) + 1)
}
