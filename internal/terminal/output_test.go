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
	require.EqualError(t, ValidateOutput("number, foo", "", []string{"number", "title"}), "unknown JSON field \"foo\"\navailable fields: number, title")
	require.EqualError(t, ValidateOutput("number,,title", "", []string{"number", "title"}), "--json field names cannot be empty; use --json alone for every field")
}

func TestJSONOutputIsIndentedAndColoredForTerminals(t *testing.T) {
	var out bytes.Buffer
	value := []map[string]any{{"title": "a \u202e\x1b[31mb", "done": true, "due": nil, "number": 1}}
	require.NoError(t, JSONOutput{Fields: "number,title", Pretty: true}.Write(&out, value))
	require.Equal(t, "[\n  {\n    \"number\": 1,\n    \"title\": \"a \\u001b[31mb\"\n  }\n]\n", out.String())
	out.Reset()
	require.NoError(t, JSONOutput{Pretty: true, Color: true}.Write(&out, map[string]any{"done": true, "due": nil, "title": "x: \"y\""}))
	require.Equal(t, "{\n  \x1b[1;34m\"done\"\x1b[0m: \x1b[33mtrue\x1b[0m,\n  \x1b[1;34m\"due\"\x1b[0m: \x1b[36mnull\x1b[0m,\n  \x1b[1;34m\"title\"\x1b[0m: \x1b[32m\"x: \\\"y\\\"\"\x1b[0m\n}\n", out.String())
	out.Reset()
	// Raw jq strings on a terminal cannot carry escape sequences; piped they stay byte-exact.
	require.NoError(t, JSONOutput{Query: ".title", Pretty: true}.Write(&out, value[0]))
	require.Equal(t, "a [31mb\n", out.String())
	out.Reset()
	require.NoError(t, WriteJSON(&out, value[0], "", ".title"))
	require.Equal(t, "a \u202e\x1b[31mb\n", out.String())
}
