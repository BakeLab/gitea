// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package wiki

import (
	"encoding/base64"
	"fmt"

	stdctx "context"

	"gitea.dev/sdk"
	"gitea.dev/tea/cmd/flags"
	teaContext "gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/task"
	"github.com/urfave/cli/v3"
)

var wikiTitleFlag = &cli.StringFlag{
	Name:    "title",
	Aliases: []string{"t"},
	Usage:   "wiki page title",
}

var wikiContentFlag = &cli.StringFlag{
	Name:    "content",
	Aliases: []string{"c"},
	Usage:   "wiki page content",
}

var wikiMessageFlag = &cli.StringFlag{
	Name:    "message",
	Aliases: []string{"m"},
	Usage:   "commit message for the wiki change",
}

var wikiDeleteConfirmFlag = &cli.BoolFlag{
	Name:    "confirm",
	Aliases: []string{"y"},
	Usage:   "confirm deletion without prompting",
}

var wikiWriteFlags = append([]cli.Flag{wikiTitleFlag, wikiContentFlag, wikiMessageFlag}, flags.LoginRepoFlags...)

// CmdWikiCreate represents the create subcommand for wiki pages.
var CmdWikiCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create a wiki page",
	Description: "Create a wiki page",
	ArgsUsage:   " ",
	Action:      RunWikiCreate,
	Flags:       wikiWriteFlags,
}

// CmdWikiEdit represents the edit subcommand for wiki pages.
var CmdWikiEdit = cli.Command{
	Name:        "edit",
	Aliases:     []string{"e"},
	Usage:       "Edit a wiki page",
	Description: "Edit a wiki page",
	ArgsUsage:   "<page>",
	Action:      RunWikiEdit,
	Flags:       wikiWriteFlags,
}

// CmdWikiDelete represents the delete subcommand for wiki pages.
var CmdWikiDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete a wiki page",
	Description: "Delete a wiki page",
	ArgsUsage:   "<page>",
	Action:      RunWikiDelete,
	Flags:       append([]cli.Flag{wikiDeleteConfirmFlag}, flags.LoginRepoFlags...),
}

func getWikiCreateOptions(cmd *cli.Command) (gitea.CreateWikiPageOptions, error) {
	return buildWikiWriteOptions(cmd.String("title"), cmd.String("content"), cmd.String("message"), true)
}

func getWikiEditOptions(cmd *cli.Command) (gitea.CreateWikiPageOptions, error) {
	return buildWikiWriteOptions(cmd.String("title"), cmd.String("content"), cmd.String("message"), false)
}

func buildWikiWriteOptions(title, content, message string, titleRequired bool) (gitea.CreateWikiPageOptions, error) {
	if content == "" {
		return gitea.CreateWikiPageOptions{}, fmt.Errorf("content is required")
	}
	if titleRequired && title == "" {
		return gitea.CreateWikiPageOptions{}, fmt.Errorf("title is required")
	}
	return gitea.CreateWikiPageOptions{
		Title:         title,
		ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
		Message:       message,
	}, nil
}

// RunWikiCreate creates a wiki page.
func RunWikiCreate(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := initWikiWriteContext(cmd)
	if err != nil {
		return err
	}
	return runWikiCreateWithClient(ctx, ctx.Login.Client().Wiki, cmd)
}

// RunWikiEdit edits a wiki page.
func RunWikiEdit(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := initWikiWriteContext(cmd)
	if err != nil {
		return err
	}
	return runWikiEditWithClient(ctx, cmd.Args().First(), ctx.Login.Client().Wiki, cmd)
}

// RunWikiDelete deletes a wiki page.
func RunWikiDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := initWikiWriteContext(cmd)
	if err != nil {
		return err
	}
	return runWikiDeleteWithClient(ctx, cmd.Args().First(), ctx.Login.Client().Wiki, cmd.Bool("confirm"))
}

func runWikiCreateWithClient(ctx *teaContext.TeaContext, client task.WikiWriteClient, cmd *cli.Command) error {
	opts, err := getWikiCreateOptions(cmd)
	if err != nil {
		return err
	}
	page, err := task.CreateWikiPage(ctx, client, opts)
	if err != nil {
		return err
	}
	return print.WikiPageDetails(page, ctx.Output)
}

func runWikiEditWithClient(ctx *teaContext.TeaContext, pageName string, client task.WikiWriteClient, cmd *cli.Command) error {
	if pageName == "" {
		return fmt.Errorf("page name is required")
	}
	opts, err := getWikiEditOptions(cmd)
	if err != nil {
		return err
	}
	if opts.Title == "" {
		opts.Title = pageName
	}
	page, err := task.EditWikiPage(ctx, client, pageName, opts)
	if err != nil {
		return err
	}
	return print.WikiPageDetails(page, ctx.Output)
}

func runWikiDeleteWithClient(ctx *teaContext.TeaContext, pageName string, client task.WikiWriteClient, confirm bool) error {
	if pageName == "" {
		return fmt.Errorf("page name is required")
	}
	if !confirm {
		return fmt.Errorf("deletion requires --confirm")
	}
	return task.DeleteWikiPage(ctx, client, pageName)
}

func initWikiWriteContext(cmd *cli.Command) (*teaContext.TeaContext, error) {
	return initWikiContext(cmd)
}
