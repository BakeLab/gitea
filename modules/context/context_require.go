// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"errors"
)

// Ensure checks if requirements on the context are set.
func (ctx *TeaContext) Ensure(req CtxRequirement) error {
	if req.LocalRepo && ctx.LocalRepo == nil {
		return errors.New("local repository required: execute from a repo dir, or specify a path with --repo")
	}

	if req.RemoteRepo && len(ctx.RepoSlug) == 0 {
		return errors.New("remote repository required: specify id via --repo or execute from a local git repo")
	}

	if req.Org && len(ctx.Org) == 0 {
		return errors.New("organization required: specify organization via --org")
	}

	if req.Global && !ctx.IsGlobal {
		return errors.New("global scope required: specify --global")
	}

	return nil
}

// CtxRequirement specifies context needed for operation
type CtxRequirement struct {
	// ensures a local git repo is available & ctx.LocalRepo is set. Implies .RemoteRepo
	LocalRepo bool
	// ensures ctx.RepoSlug, .Owner, .Repo are set
	RemoteRepo bool
	// ensures ctx.Org is set
	Org bool
	// ensures ctx.IsGlobal is true
	Global bool
}
