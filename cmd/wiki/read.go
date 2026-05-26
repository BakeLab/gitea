// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package wiki

import (
	stdctx "context"
	"net/http"

	"gitea.dev/sdk"
	"gitea.dev/tea/cmd/flags"
	teaContext "gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"github.com/urfave/cli/v3"
)

var (
	wikiFieldsFlag         = flags.FieldsFlag(print.WikiPageFields, []string{"title", "author", "updated", "sha"})
	wikiRevisionFieldsFlag = flags.FieldsFlag(print.WikiRevisionFields, []string{"sha", "author", "date", "message"})
)

// CmdWikiList represents the list subcommand for wiki pages.
var CmdWikiList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List wiki pages of the repository",
	Description: "List wiki pages of the repository",
	ArgsUsage:   " ",
	Action:      RunWikiList,
	Flags: append([]cli.Flag{
		wikiFieldsFlag,
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// CmdWikiView represents the view subcommand for wiki pages.
var CmdWikiView = cli.Command{
	Name:        "view",
	Usage:       "View a wiki page",
	Description: "View a wiki page",
	ArgsUsage:   "<page>",
	Action:      RunWikiView,
	Flags:       append([]cli.Flag{}, flags.AllDefaultFlags...),
}

// CmdWikiRevisions represents the revisions subcommand for wiki pages.
var CmdWikiRevisions = cli.Command{
	Name:        "revisions",
	Aliases:     []string{"history"},
	Usage:       "List revisions of a wiki page",
	Description: "List revisions of a wiki page",
	ArgsUsage:   "<page>",
	Action:      RunWikiRevisions,
	Flags: append([]cli.Flag{
		wikiRevisionFieldsFlag,
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

type wikiReadClient interface {
	ListPages(ctx stdctx.Context, owner, repo string, opt gitea.ListWikiPagesOptions) ([]*gitea.WikiPageMetaData, *gitea.Response, error)
	GetPage(ctx stdctx.Context, owner, repo, pageName string) (*gitea.WikiPage, *gitea.Response, error)
	GetPageRevisions(ctx stdctx.Context, owner, repo, pageName string, opt gitea.ListWikiPageRevisionsOptions) (*gitea.WikiCommitList, *gitea.Response, error)
}

// RunWikiList lists wiki pages.
func RunWikiList(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := initWikiContext(cmd)
	if err != nil {
		return err
	}
	fields, err := wikiFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}
	return runWikiListWithClient(ctx, ctx.Login.Client().Wiki, fields)
}

// RunWikiView shows a wiki page.
func RunWikiView(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := initWikiContext(cmd)
	if err != nil {
		return err
	}
	return runWikiViewWithClient(ctx, cmd.Args().First(), ctx.Login.Client().Wiki)
}

// RunWikiRevisions lists revisions of a wiki page.
func RunWikiRevisions(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := initWikiContext(cmd)
	if err != nil {
		return err
	}
	fields, err := wikiRevisionFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}
	return runWikiRevisionsWithClient(ctx, cmd.Args().First(), ctx.Login.Client().Wiki, fields)
}

func initWikiContext(cmd *cli.Command) (*teaContext.TeaContext, error) {
	ctx, err := teaContext.InitCommand(cmd)
	if err != nil {
		return nil, err
	}
	if err := ctx.Ensure(teaContext.CtxRequirement{RemoteRepo: true}); err != nil {
		return nil, err
	}
	return ctx, nil
}

func runWikiListWithClient(ctx *teaContext.TeaContext, client wikiReadClient, fields []string) error {
	pages, resp, err := client.ListPages(stdctx.Background(), ctx.Owner, ctx.Repo, gitea.ListWikiPagesOptions{ListOptions: flags.GetListOptions(ctx.Command)})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return print.WikiPagesList(nil, ctx.Output, fields)
		}
		return err
	}
	return print.WikiPagesList(pages, ctx.Output, fields)
}

func runWikiViewWithClient(ctx *teaContext.TeaContext, pageName string, client wikiReadClient) error {
	page, _, err := client.GetPage(stdctx.Background(), ctx.Owner, ctx.Repo, pageName)
	if err != nil {
		return err
	}
	return print.WikiPageDetails(page, ctx.Output)
}

func runWikiRevisionsWithClient(ctx *teaContext.TeaContext, pageName string, client wikiReadClient, fields []string) error {
	revisions, _, err := client.GetPageRevisions(stdctx.Background(), ctx.Owner, ctx.Repo, pageName, gitea.ListWikiPageRevisionsOptions{Page: ctx.Int("page")})
	if err != nil {
		return err
	}
	return print.WikiRevisionsList(revisions.WikiCommits, ctx.Output, fields)
}
