// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import "testing"

func TestGetDefaultPRTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Add new feature", want: "Add New Feature"},
		{input: "update-docs: Fix typo", want: "Fix Typo"},
		{input: "remove_long-string", want: "Remove Long String"},
		{input: "Replace_Underscores_With_Spaces", want: "Replace Underscores With Spaces"},
		{input: "  leading-and-trailing-spaces ", want: "Leading And Trailing Spaces"},
		{input: "-----No--Upper--Case-----", want: "No Upper Case"},
		{input: "", want: ""},
	}
	for _, test := range tests {
		got := GetDefaultPRTitle(test.input)
		if got != test.want {
			t.Errorf("GetDefaultPRTitle(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
