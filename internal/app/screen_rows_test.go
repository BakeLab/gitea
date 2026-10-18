// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScreenRowsCountsWrappedLines(t *testing.T) {
	require.Equal(t, 2, screenRows("one\ntwo\n", 80))
	require.Equal(t, 2, screenRows("one\ntwo\n", 0))
	require.Equal(t, 4, screenRows(strings.Repeat("x", 25)+"\nab\n", 10), "25 cells take 3 rows of 10")
	require.Equal(t, 2, screenRows("\x1b[31m"+strings.Repeat("x", 10)+"\x1b[0m\n\x1b]8;;http://a/b\x1b\\link\x1b]8;;\x1b\\\n", 10), "escapes take no room")
}
