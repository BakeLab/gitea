// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strings"

	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// openBrowser opens resource in the configured browser, telling people on a terminal what opens.
func (a *application) openBrowser(command *cobra.Command, resource string) error {
	preferences, err := a.preferences()
	if err != nil {
		return err
	}
	if a.deps.Streams.OutTTY {
		fmt.Fprintf(command.ErrOrStderr(), "Opening %s in your browser.\n", displayURL(resource))
	}
	return a.deps.Browser.Open(command.Context(), resource, preferences.Browser)
}

// displayURL shows a URL to people without its scheme, like gh.
func displayURL(url string) string {
	return terminal.SafeLine(strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"))
}
