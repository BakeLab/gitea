// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import "github.com/spf13/cobra"

func (a *application) openBrowser(command *cobra.Command, resource string) error {
	preferences, err := a.preferences()
	if err != nil {
		return err
	}
	return a.deps.Browser.Open(command.Context(), resource, preferences.Browser)
}
