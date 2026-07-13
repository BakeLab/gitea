// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package theme

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compatPkg detects the terminal background from package-level vars, so importing
// it anywhere makes tea query the terminal before main() runs. On Windows that
// query can block forever when stdio is redirected, which hung every tea command,
// including tea --version.
//
// lipgloss.LightDark covers what we need without the package-level detection, so
// nothing in tea should depend on compat again.
const compatPkg = "charm.land/lipgloss/v2/compat"

func TestBinaryDoesNotImportLipglossCompat(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	out, err := exec.Command("go", "list", "-deps", "gitea.dev/tea").Output()
	require.NoError(t, err, "go list -deps")

	imported := slices.Contains(strings.Fields(string(out)), compatPkg)
	assert.False(t, imported,
		"%s is back in tea's import graph. It detects the terminal background from "+
			"package-level vars, so tea queries the terminal before main() runs, and on "+
			"Windows that hangs at start-up when stdio is redirected.", compatPkg)
}

// Under go test neither stdin nor stdout is a terminal, so HasDarkBackground must
// take the default and return, rather than querying and waiting for an answer.
func TestHasDarkBackgroundDoesNotBlockWithoutTTY(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		done <- HasDarkBackground()
	}()

	select {
	case got := <-done:
		assert.Equal(t, defaultDarkBackground, got)
	case <-time.After(5 * time.Second):
		t.Fatal("HasDarkBackground blocked when stdio is not a terminal")
	}
}

// The title color has to come from the isDark we are handed. It used to come from
// a process-wide value that compat detected at init, which ignored this argument.
func TestThemeHonorsIsDark(t *testing.T) {
	dark := GetTheme().Theme(true).Focused.Title.GetForeground()
	light := GetTheme().Theme(false).Focused.Title.GetForeground()

	assert.NotEqual(t, dark, light, "Theme ignored isDark when picking the title color")
}
