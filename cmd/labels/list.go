// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package labels

import (
	stdctx "context"
	"log"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/task"
	"github.com/urfave/cli/v3"
)

// CmdLabelsList represents a sub command of labels to list labels
var CmdLabelsList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List labels",
	Description: "List labels",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunLabelsList,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "save",
			Aliases: []string{"s"},
			Usage:   "Save all the labels as a file",
		},
		&cli.StringFlag{
			Name:  "org",
			Usage: "List organization labels",
		},
		&cli.BoolFlag{
			Name:  "exclude-org",
			Usage: "Exclude organization labels from the list",
		},
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunLabelsList list labels.
func RunLabelsList(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if cmd.String("org") == "" && cmd.String("repo") == "" {
		if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
			return err
		}
	}

	if cmd.String("org") != "" && cmd.String("repo") != "" {
		log.Printf("Warning: Both --org and --repo flags are set. Ignoring --org and using --repo value as owner.\n")
	}

	client := ctx.Login.Client()
	labels, err := collectLabels(requestCtx, client, ctx, cmd)
	if err != nil {
		return err
	}

	if ctx.IsSet("save") {
		return task.LabelsExport(labels, ctx.String("save"))
	}

	return print.LabelsList(labels, ctx.Output)
}

// CollectLabels collects labels based on the provided command flags and context.
func collectLabels(requestCtx stdctx.Context, client *gitea.Client, ctx *context.TeaContext, cmd *cli.Command) ([]*gitea.Label, error) {
	var labels []*gitea.Label
	var err error
	owner := ctx.Owner

	// If --org is set but --repo is not, list organization labels only and skip repository labels.
	if cmd.String("org") != "" && cmd.String("repo") == "" {
		owner = ctx.Org
	} else {
		labels, _, err = client.Repositories.ListRepoLabels(requestCtx, owner, ctx.Repo, gitea.ListLabelsOptions{
			ListOptions: flags.GetListOptions(cmd),
		})
		if err != nil {
			log.Printf("Failed to list repository labels: %v", err)
		}
	}

	// If --exclude is not set and the owner is an organization, also list organization labels and append them to the list.
	if !ctx.IsSet("exclude-org") {
		if _, _, err = client.Organizations.GetOrg(requestCtx, owner); err == nil {
			orgLabels, _, err := client.Organizations.ListOrgLabels(requestCtx, owner, gitea.ListOrgLabelsOptions{
				ListOptions: flags.GetListOptions(cmd),
			})
			if err != nil {
				return nil, err
			}
			labels = append(labels, orgLabels...)
		}
	}

	return labels, nil
}
