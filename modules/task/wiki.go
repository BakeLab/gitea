// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"

	"gitea.dev/sdk"
	teaContext "gitea.dev/tea/modules/context"
)

// WikiWriteClient defines the wiki write methods required by task helpers.
type WikiWriteClient interface {
	CreatePage(ctx stdctx.Context, owner, repo string, opt gitea.CreateWikiPageOptions) (*gitea.WikiPage, *gitea.Response, error)
	EditPage(ctx stdctx.Context, owner, repo, pageName string, opt gitea.CreateWikiPageOptions) (*gitea.WikiPage, *gitea.Response, error)
	DeletePage(ctx stdctx.Context, owner, repo, pageName string) (*gitea.Response, error)
}

// CreateWikiPage creates a wiki page and returns the created page.
func CreateWikiPage(ctx *teaContext.TeaContext, client WikiWriteClient, opt gitea.CreateWikiPageOptions) (*gitea.WikiPage, error) {
	if opt.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if opt.ContentBase64 == "" {
		return nil, fmt.Errorf("content is required")
	}
	page, _, err := client.CreatePage(stdctx.Background(), ctx.Owner, ctx.Repo, opt)
	if err != nil {
		return nil, fmt.Errorf("could not create wiki page: %s", err)
	}
	return page, nil
}

// EditWikiPage edits a wiki page and returns the updated page.
func EditWikiPage(ctx *teaContext.TeaContext, client WikiWriteClient, pageName string, opt gitea.CreateWikiPageOptions) (*gitea.WikiPage, error) {
	if pageName == "" {
		return nil, fmt.Errorf("page name is required")
	}
	if opt.ContentBase64 == "" {
		return nil, fmt.Errorf("content is required")
	}
	page, _, err := client.EditPage(stdctx.Background(), ctx.Owner, ctx.Repo, pageName, opt)
	if err != nil {
		return nil, fmt.Errorf("could not edit wiki page: %s", err)
	}
	return page, nil
}

// DeleteWikiPage deletes a wiki page.
func DeleteWikiPage(ctx *teaContext.TeaContext, client WikiWriteClient, pageName string) error {
	if pageName == "" {
		return fmt.Errorf("page name is required")
	}
	_, err := client.DeletePage(stdctx.Background(), ctx.Owner, ctx.Repo, pageName)
	if err != nil {
		return fmt.Errorf("could not delete wiki page: %s", err)
	}
	return nil
}
