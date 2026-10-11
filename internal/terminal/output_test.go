// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONFieldSelectionAndJQ(t *testing.T) {
	var out bytes.Buffer
	value := []map[string]any{{"number": 1, "title": "first"}, {"number": 2, "title": "second"}}
	require.NoError(t, WriteJSON(&out, value, "title", ".[0].title"))
	require.Equal(t, "first\n", out.String())
	out.Reset()
	require.NoError(t, WriteJSON(&out, []map[string]any{}, "*", ""))
	require.Equal(t, "[]\n", out.String())
	require.Error(t, WriteJSON(&out, value, "unknown", ""))
	require.NoError(t, ValidateOutput("number, title", "", []string{"number", "title"}))
	require.EqualError(t, ValidateOutput("number, foo", "", []string{"number"}), `unknown JSON field "foo"`)
}
