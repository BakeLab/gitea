// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBreadboardWheelCleanupScope(t *testing.T) {
	rule := breadboardWheelCleanupRule(4, false)
	require.NoError(t, rule.CompiledPattern())
	assert.False(t, rule.Enabled)
	assert.Equal(t, 10, rule.KeepCount)
	assert.Equal(t, 30, rule.RemoveDays)
	assert.True(t, rule.MatchFullName)
	assert.True(t, rule.RemovePatternMatcher.MatchString("breadboard/2026.11.14.post1"))
	for _, name := range []string{"breadboard-asset-lock", "breadboard-guest-image", "breadboard-board-pack", "breadboard-accel-pack", "bmetal-node", "other-breadboard"} {
		assert.False(t, rule.RemovePatternMatcher.MatchString(name+"/2026.11.14"), name)
	}
	assert.True(t, breadboardWheelCleanupRule(4, true).Enabled)
}
