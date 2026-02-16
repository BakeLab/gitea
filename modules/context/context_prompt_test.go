// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"testing"

	"code.gitea.io/tea/modules/config"
)

func TestShouldPromptFallbackLogin(t *testing.T) {
	tests := []struct {
		name      string
		login     *config.Login
		canPrompt bool
		expected  bool
	}{
		{
			name:      "no login",
			login:     nil,
			canPrompt: true,
			expected:  false,
		},
		{
			name:      "default login",
			login:     &config.Login{Default: true},
			canPrompt: true,
			expected:  false,
		},
		{
			name:      "non-default no prompt",
			login:     &config.Login{Default: false},
			canPrompt: false,
			expected:  false,
		},
		{
			name:      "non-default prompt",
			login:     &config.Login{Default: false},
			canPrompt: true,
			expected:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldPromptFallbackLogin(test.login, test.canPrompt); got != test.expected {
				t.Fatalf("expected %v, got %v", test.expected, got)
			}
		})
	}
}
