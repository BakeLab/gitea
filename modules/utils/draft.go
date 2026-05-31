// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import "regexp"

// draftPrefixRe matches the prefixes Gitea recognizes as marking a pull
// request as a draft: a leading "WIP:" or "[WIP]" (case-insensitive),
// followed by any whitespace.
var draftPrefixRe = regexp.MustCompile(`(?i)^(wip:\s*|\[wip\]\s*)`)

// HasDraftPrefix reports whether title already starts with a draft marker.
func HasDraftPrefix(title string) bool {
	return draftPrefixRe.MatchString(title)
}

// AddDraftPrefix returns title with a "WIP: " prefix, or title unchanged
// if it already carries a recognized draft prefix.
func AddDraftPrefix(title string) string {
	if HasDraftPrefix(title) {
		return title
	}
	return "WIP: " + title
}

// StripDraftPrefix returns title with any recognized leading draft prefix
// removed, or title unchanged if no prefix is present.
func StripDraftPrefix(title string) string {
	return draftPrefixRe.ReplaceAllString(title, "")
}
