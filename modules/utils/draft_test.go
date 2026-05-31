// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import "testing"

func TestDraftPrefix(t *testing.T) {
	cases := []struct {
		in        string
		has       bool
		stripped  string
		withDraft string
	}{
		{"plain title", false, "plain title", "WIP: plain title"},
		{"WIP: already", true, "already", "WIP: already"},
		{"wip: lowercase", true, "lowercase", "wip: lowercase"},
		{"[WIP] bracketed", true, "bracketed", "[WIP] bracketed"},
		{"[wip]  extra space", true, "extra space", "[wip]  extra space"},
		{"Draft: not recognized", false, "Draft: not recognized", "WIP: Draft: not recognized"},
		{"", false, "", "WIP: "},
	}
	for _, c := range cases {
		if got := HasDraftPrefix(c.in); got != c.has {
			t.Errorf("HasDraftPrefix(%q) = %v, want %v", c.in, got, c.has)
		}
		if got := StripDraftPrefix(c.in); got != c.stripped {
			t.Errorf("StripDraftPrefix(%q) = %q, want %q", c.in, got, c.stripped)
		}
		if got := AddDraftPrefix(c.in); got != c.withDraft {
			t.Errorf("AddDraftPrefix(%q) = %q, want %q", c.in, got, c.withDraft)
		}
	}
}
