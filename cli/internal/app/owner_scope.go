// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"

	"github.com/spf13/cobra"
)

// ownerScope holds the --org, --account and --global flags of a command group whose resources
// belong to a repository, an organization, the user's account or the instance.
type ownerScope struct {
	org             string
	account, global bool
}

func (s *ownerScope) inRepository() bool { return s.org == "" && !s.account && !s.global }

// of returns the scope that the flags select, or repository when they select none.
func (s *ownerScope) of(repository string) services.OwnerScope {
	return services.OwnerScope{Repository: repository, Organization: s.org, Account: s.account, Global: s.global}
}

// resolve selects the scope and the account that manages it.
func (s *ownerScope) resolve(a *application, cmd *cobra.Command) (target, services.OwnerScope, error) {
	if scopes := setFlags([]string{"--org", "--account", "--global", "--repo"}, s.org != "", s.account, s.global, a.options.repo != ""); len(scopes) > 1 {
		return target{}, services.OwnerScope{}, clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with %s", scopes[0], scopes[1]))
	}
	t, err := a.resolve(cmd.Context(), s.inRepository())
	return t, s.of(t.Repo), err
}

// ownerSettingsURL links a page below the settings of scope, such as hooks/3.
func ownerSettingsURL(t target, scope services.OwnerScope, path ...string) string {
	var base []string
	switch {
	case scope.Global:
		base = []string{"-", "admin"}
	case scope.Account:
		base = []string{"user", "settings"}
	case scope.Organization != "":
		base = []string{"org", scope.Organization, "settings"}
	default:
		owner, name, _ := strings.Cut(scope.Repository, "/")
		base = []string{owner, name, "settings"}
	}
	return webURL(t, append(base, path...)...)
}
