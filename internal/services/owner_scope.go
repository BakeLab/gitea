// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strconv"

	"gitea.dev/cli/internal/transport"
)

// OwnerScope selects the webhooks or Actions runners of one repository, organization, account or
// the instance.
type OwnerScope struct {
	Repository, Organization string
	Account, Global          bool
}

// String names the scope for people: "alice/x", "organization acme", "your account" or "the instance".
func (s OwnerScope) String() string {
	switch {
	case s.Global:
		return "the instance"
	case s.Account:
		return "your account"
	case s.Organization != "":
		return "organization " + s.Organization
	}
	return s.Repository
}

// listError names the repository or organization of the scope when a list answers 404, which can
// only mean that it is missing or invisible.
func (s OwnerScope) listError(err error) error {
	switch {
	case s.Global, s.Account:
		return err
	case s.Organization != "":
		return Classify(err, "organization", strconv.Quote(s.Organization), "")
	}
	return Classify(err, "repository", s.Repository, "")
}

// itemError names kind id in the failure of a request about it, or the repository or organization
// of the scope when that is what is missing: Gitea answers both with the same 404.
func (s OwnerScope) itemError(ctx context.Context, client *transport.Client, err error, kind string, id int64) error {
	key := strconv.FormatInt(id, 10)
	switch {
	case s.Global, s.Account:
		return Classify(err, kind, key, "")
	case s.Organization != "":
		if IsNotFound(err) {
			if _, isOrg, orgErr := organization(ctx, client, s.Organization); orgErr == nil && !isOrg {
				return s.listError(err)
			}
		}
		return Classify(err, kind, key, s.Organization)
	}
	return repositoryItemError(ctx, client, s.Repository, err, kind, key)
}
