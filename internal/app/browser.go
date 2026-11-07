// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"net/url"
	"strings"

	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

// openBrowser opens resource in the configured browser, telling people on a terminal what opens.
func (a *application) openBrowser(command *cobra.Command, resource string) error {
	return a.openBrowserShowing(command, resource, displayURL(resource))
}

// openBrowserShowing is openBrowser naming resource as shown, such as the whole URL when people
// may have to copy it.
func (a *application) openBrowserShowing(command *cobra.Command, resource, shown string) error {
	preferences, err := a.preferences()
	if err != nil {
		return err
	}
	if a.deps.Streams.OutTTY {
		fmt.Fprintf(command.ErrOrStderr(), "Opening %s in your browser.\n", shown)
	}
	return a.deps.Browser.Open(command.Context(), resource, preferences.Browser)
}

// displayURL shows a URL to people without its scheme, like gh.
func displayURL(url string) string {
	return terminal.SafeLine(strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"))
}

// webURL is the page of a path on the target's instance, such as an organization's profile.
func webURL(t target, path ...string) string {
	escaped := make([]string, len(path))
	for i, segment := range path {
		escaped[i] = url.PathEscape(segment)
	}
	return strings.TrimSuffix(t.Host, "/") + "/" + strings.Join(escaped, "/")
}

// repositoryURL returns the web URL of the target's repository.
func repositoryURL(t target) string {
	return strings.TrimSuffix(t.Host, "/") + "/" + t.Repo
}
