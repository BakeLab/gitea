// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"testing"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureReturnsRequirementErrors(t *testing.T) {
	tests := []struct {
		name    string
		ctx     TeaContext
		req     CtxRequirement
		wantErr string
	}{
		{
			name:    "missing local repo",
			ctx:     TeaContext{},
			req:     CtxRequirement{LocalRepo: true},
			wantErr: "local repository required",
		},
		{
			name:    "missing remote repo",
			ctx:     TeaContext{},
			req:     CtxRequirement{RemoteRepo: true},
			wantErr: "remote repository required",
		},
		{
			name:    "missing org",
			ctx:     TeaContext{},
			req:     CtxRequirement{Org: true},
			wantErr: "organization required",
		},
		{
			name:    "missing global scope",
			ctx:     TeaContext{},
			req:     CtxRequirement{Global: true},
			wantErr: "global scope required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ctx.Ensure(tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestEnsureSucceedsWhenRequirementsMet(t *testing.T) {
	ctx := TeaContext{
		LocalRepo: &git.TeaRepo{},
		RepoSlug:  "owner/repo",
		Owner:     "owner",
		Repo:      "repo",
		Org:       "myorg",
		IsGlobal:  true,
	}
	err := ctx.Ensure(CtxRequirement{
		LocalRepo:  true,
		RemoteRepo: true,
		Org:        true,
		Global:     true,
	})
	require.NoError(t, err)
}

func TestEnsureSucceedsWithNoRequirements(t *testing.T) {
	ctx := TeaContext{}
	err := ctx.Ensure(CtxRequirement{})
	require.NoError(t, err)
}

func TestGetRemoteRepoHTMLURL(t *testing.T) {
	t.Run("requires remote repo", func(t *testing.T) {
		ctx := &TeaContext{}
		_, err := ctx.GetRemoteRepoHTMLURL()
		require.ErrorContains(t, err, "remote repository required")
	})

	t.Run("returns repo url when context is complete", func(t *testing.T) {
		ctx := &TeaContext{
			Login:    &config.Login{URL: "https://gitea.example.com"},
			RepoSlug: "owner/repo",
			Owner:    "owner",
			Repo:     "repo",
		}

		url, err := ctx.GetRemoteRepoHTMLURL()
		require.NoError(t, err)
		assert.Equal(t, "https://gitea.example.com/owner/repo", url)
	})

	t.Run("trims trailing slash from login URL", func(t *testing.T) {
		ctx := &TeaContext{
			Login:    &config.Login{URL: "https://gitea.example.com/"},
			RepoSlug: "owner/repo",
			Owner:    "owner",
			Repo:     "repo",
		}

		url, err := ctx.GetRemoteRepoHTMLURL()
		require.NoError(t, err)
		assert.Equal(t, "https://gitea.example.com/owner/repo", url)
	})
}
