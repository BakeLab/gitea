// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package aliases

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpand(t *testing.T) {
	builtins := map[string]bool{"issue": true, "pr": true}
	for _, tc := range []struct {
		expansion  string
		args, want []string
	}{
		{"issue list --label 'good first issue'", []string{"bugs", "--limit", "5"}, []string{"issue", "list", "--label", "good first issue", "--limit", "5"}},
		{"issue view $2", []string{"bugs", "left", "123", "--json"}, []string{"issue", "view", "123", "left", "--json"}},
		{"issue create --title $1", []string{"bugs", "$(never execute)"}, []string{"issue", "create", "--title", "$(never execute)"}},
		{"issue list --search '!wip'", []string{"bugs"}, []string{"issue", "list", "--search", "!wip"}},
		{"issue list", []string{}, []string{}},
	} {
		got, err := Expand(tc.args, map[string]string{"bugs": tc.expansion}, builtins)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	got, err := Expand([]string{"a", "42"}, map[string]string{"a": "b", "b": "pr view"}, builtins)
	require.NoError(t, err)
	require.Equal(t, []string{"pr", "view", "42"}, got)
}

func TestRejectInvalidAliases(t *testing.T) {
	builtins := map[string]bool{"issue": true}
	for _, aliases := range []map[string]string{{"bad": ""}, {"bad": "issue '"}, {"bad": "a", "a": "bad"}, {"bad": "issue list | cat"}, {"bad": "issue list >> out"}, {"bad": "!echo hi"}, {"bad": "unknown list"}, {"bad": "$1 list"}} {
		require.Error(t, Check("bad", aliases, builtins))
		_, err := Expand([]string{"bad"}, aliases, builtins)
		require.Error(t, err)
	}
	require.Error(t, Check("issue", map[string]string{"issue": "issue list"}, builtins))
	_, err := Expand([]string{"a"}, map[string]string{"a": "issue view $1"}, builtins)
	require.Error(t, err)
	// A broken or colliding entry only fails when invoked; built-ins always win.
	broken := map[string]string{"issue": "pr list", "bad": "unknown", "ok": "issue list"}
	got, err := Expand([]string{"ok"}, broken, builtins)
	require.NoError(t, err)
	require.Equal(t, []string{"issue", "list"}, got)
	got, err = Expand([]string{"issue", "view"}, broken, builtins)
	require.NoError(t, err)
	require.Equal(t, []string{"issue", "view"}, got)
}

func TestCompletionPreservesCurrentWordAfterFixedFlags(t *testing.T) {
	definitions := map[string]string{"inspect": "pr view $1 --json number", "nested": "inspect $1"}
	builtins := map[string]bool{"pr": true}
	expanded, err := ExpandCompletion([]string{"nested", "7"}, definitions, builtins)
	require.NoError(t, err)
	require.Equal(t, []string{"pr", "view", "--json", "number", "7"}, expanded)
	expanded, err = ExpandCompletion([]string{"inspect"}, definitions, builtins)
	require.NoError(t, err)
	require.Equal(t, []string{"inspect"}, expanded)
	expanded, err = ExpandCompletion([]string{"nested", "7", "--j"}, definitions, builtins)
	require.NoError(t, err)
	require.Equal(t, []string{"pr", "view", "7", "--json", "number", "--j"}, expanded)
}
