// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAliasCompletionUsesBuiltInCommandsAndFlags(t *testing.T) {
	deps, out, _ := testDeps(t)
	for _, definition := range [][]string{{"pulls", "pr"}, {"listing", "pulls list"}, {"inspect", "pr view $1"}} {
		require.NoError(t, Execute(context.Background(), deps, append([]string{"alias", "set"}, definition...)))
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "pulls", "ch"}, "checkout"},
		{[]string{"__complete", "listing"}, "listing"},
		{[]string{"__complete", "listing", "--auth"}, "--author"},
		{[]string{"__completeNoDesc", "--no-input", "listing", "--ba"}, "--base"},
		{[]string{"__complete", "inspect", "7", "--j"}, "--json"},
	} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, tc.args))
		require.Contains(t, out.String(), tc.want)
	}
}
