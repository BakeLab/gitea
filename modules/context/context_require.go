// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"fmt"
	"os"
)

// Ensure checks if requirements on the context are set, and terminates otherwise.
func (ctx *TeaContext) Ensure(req CtxRequirement) {
	if req.LocalRepo && ctx.LocalRepo == nil {
		fmt.Println("Local repository required: Execute from a repo dir, or specify a path with --repo.")
		os.Exit(1)
	}

	if req.RemoteRepo && len(ctx.RepoSlug) == 0 {
		fmt.Println("Remote repository required: Specify ID via --repo or execute from a local git repo.")
		os.Exit(1)
	}

	if req.Org && len(ctx.Org) == 0 {
		fmt.Println("Organization required: Specify organization via --org.")
		os.Exit(1)
	}

	if req.Global && !ctx.IsGlobal {
		fmt.Println("Global scope required: Specify --global.")
		os.Exit(1)
	}
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
