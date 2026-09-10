// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"io"

	gitea "gitea.dev/sdk"
	"github.com/urfave/cli/v3"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/task"
)

// CmdIssuesCreate represents a sub command of issues to create issue
var CmdIssuesCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create an issue on repository",
	Description: `Create an issue on repository`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runIssuesCreate,
	Flags:       flags.IssuePRCreateFlags,
}

func runIssuesCreate(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.IsInteractiveMode() {
		err := interact.CreateIssue(requestCtx, ctx.Login, ctx.Owner, ctx.Repo)
		if err != nil && !interact.IsQuitting(err) {
			return err
		}
		return nil
	}

	opts, err := flags.GetIssuePRCreateFlags(requestCtx, ctx)
	if err != nil {
		return err
	}

	issue, err := task.CreateIssue(requestCtx, ctx.Login,
		ctx.Owner,
		ctx.Repo,
		*opts,
	)
	if err != nil {
		return err
	}

	if ctx.IsSet("output") {
		switch ctx.String("output") {
		case "json":
			return writeCreatedIssueAsJSON(ctx.Writer, issue)
		}
	}

	print.IssueDetails(issue, nil)

	fmt.Println(issue.HTMLURL)

	return nil
}

// createdIssueJSON is the machine-readable representation of a freshly
// created issue, mirroring the create-PR equivalent in cmd/pulls/create.go
// (createdPullJSON).
type createdIssueJSON struct {
	Index int64           `json:"index"`
	Title string          `json:"title"`
	URL   string          `json:"url"`
	State gitea.StateType `json:"state"`
}

func writeCreatedIssueAsJSON(w io.Writer, issue *gitea.Issue) error {
	return json.NewEncoder(w).Encode(createdIssueJSON{
		Index: issue.Index,
		Title: issue.Title,
		URL:   issue.HTMLURL,
		State: issue.State,
	})
}
