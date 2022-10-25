// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package flags

import (
	"fmt"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"

	"github.com/araddon/dateparse"
	"github.com/urfave/cli/v2"
)

// StateFlag provides flag to specify issue/pr state, defaulting to "open"
var StateFlag = cli.StringFlag{
	Name:        "state",
	Usage:       "Filter by state (all|open|closed)",
	DefaultText: "open",
}

// MilestoneFilterFlag is a CSV flag used to filter issues by milestones
var MilestoneFilterFlag = NewCsvFlag(
	"milestones",
	"milestones to match issues against",
	[]string{"m"}, nil, nil)

// LabelFilterFlag is a CSV flag used to filter issues by labels
var LabelFilterFlag = NewCsvFlag(
	"labels",
	"labels to match issues against",
	[]string{"L"}, nil, nil)

// PRListingFlags defines flags that should be available on pr listing flags.
var PRListingFlags = append([]cli.Flag{
	&StateFlag,
	&PaginationPageFlag,
	&PaginationLimitFlag,
}, AllDefaultFlags...)

// IssueListingFlags defines flags that should be available on issue listing flags.
var IssueListingFlags = append([]cli.Flag{
	&StateFlag,
	&cli.StringFlag{
		Name:        "kind",
		Aliases:     []string{"K"},
		Usage:       "Whether to return `issues`, `pulls`, or `all` (you can use this to apply advanced search filters to PRs)",
		DefaultText: "issues",
	},
	&cli.StringFlag{
		Name:    "keyword",
		Aliases: []string{"k"},
		Usage:   "Filter by search string",
	},
	LabelFilterFlag,
	MilestoneFilterFlag,
	&cli.StringFlag{
		Name:    "author",
		Aliases: []string{"A"},
	},
	&cli.StringFlag{
		Name:    "assignee",
		Aliases: []string{"a"},
	},
	&cli.StringFlag{
		Name:    "mentions",
		Aliases: []string{"M"},
	},
	&cli.StringFlag{
		Name:    "from",
		Aliases: []string{"F"},
		Usage:   "Filter by activity after this date",
	},
	&cli.StringFlag{
		Name:    "until",
		Aliases: []string{"u"},
		Usage:   "Filter by activity before this date",
	},
	&PaginationPageFlag,
	&PaginationLimitFlag,
}, AllDefaultFlags...)

// issuePRFlags defines shared flags between flags IssuePRCreateFlags and IssuePREditFlags
var issuePRFlags = append([]cli.Flag{
	&cli.StringFlag{
		Name:    "title",
		Aliases: []string{"t"},
	},
	&cli.StringFlag{
		Name:    "description",
		Aliases: []string{"d"},
	},
	&cli.StringFlag{
		Name:    "referenced-version",
		Aliases: []string{"v"},
		Usage:   "commit-hash or tag name to assign",
	},
	&cli.StringFlag{
		Name:    "milestone",
		Aliases: []string{"m"},
		Usage:   "Milestone to assign",
	},
	&cli.StringFlag{
		Name:    "deadline",
		Aliases: []string{"D"},
		Usage:   "Deadline timestamp to assign",
	},
}, LoginRepoFlags...)

// IssuePRCreateFlags defines flags for creation of issues and PRs
var IssuePRCreateFlags = append([]cli.Flag{
	&cli.StringFlag{
		Name:    "assignees",
		Aliases: []string{"a"},
		Usage:   "Comma-separated list of usernames to assign",
	},
	&cli.StringFlag{
		Name:    "labels",
		Aliases: []string{"L"},
		Usage:   "Comma-separated list of labels to assign",
	},
}, issuePRFlags...)

// GetIssuePRCreateFlags parses all IssuePREditFlags
func GetIssuePRCreateFlags(ctx *context.TeaContext) (*gitea.CreateIssueOption, error) {
	opts := gitea.CreateIssueOption{
		Title:     ctx.String("title"),
		Body:      ctx.String("description"),
		Assignees: strings.Split(ctx.String("assignees"), ","),
	}
	var err error

	date := ctx.String("deadline")
	if date != "" {
		t, err := dateparse.ParseAny(date)
		if err != nil {
			return nil, err
		}
		opts.Deadline = &t
	}

	client := ctx.Login.Client()

	labelNames := strings.Split(ctx.String("labels"), ",")
	if len(labelNames) != 0 {
		if client == nil {
			client = ctx.Login.Client()
		}
		if opts.Labels, err = task.ResolveLabelNames(client, ctx.Owner, ctx.Repo, labelNames); err != nil {
			return nil, err
		}
	}

	if milestoneName := ctx.String("milestone"); len(milestoneName) != 0 {
		if client == nil {
			client = ctx.Login.Client()
		}
		ms, _, err := client.GetMilestoneByName(ctx.Owner, ctx.Repo, milestoneName)
		if err != nil {
			return nil, fmt.Errorf("Milestone '%s' not found", milestoneName)
		}
		opts.Milestone = ms.ID
	}

	return &opts, nil
}

// IssuePREditFlags defines flags for editing properties of issues and PRs
var IssuePREditFlags = append([]cli.Flag{
	&cli.StringFlag{
		Name:    "add-assignees",
		Aliases: []string{"a"},
		Usage:   "Comma-separated list of usernames to assign",
	},
	&cli.StringFlag{
		Name:    "add-labels",
		Aliases: []string{"L"},
		Usage:   "Comma-separated list of labels to assign. Takes precedence over --remove-labels",
	},
	&cli.StringFlag{
		Name:  "remove-labels",
		Usage: "Comma-separated list of labels to remove",
	},
}, issuePRFlags...)

// GetIssuePREditFlags parses all IssuePREditFlags
func GetIssuePREditFlags(ctx *context.TeaContext) (*task.EditIssueOption, error) {
	opts := task.EditIssueOption{}
	if ctx.IsSet("title") {
		val := ctx.String("title")
		opts.Title = &val
	}
	if ctx.IsSet("description") {
		val := ctx.String("description")
		opts.Body = &val
	}
	if ctx.IsSet("referenced-version") {
		val := ctx.String("referenced-version")
		opts.Ref = &val
	}
	if ctx.IsSet("milestone") {
		val := ctx.String("milestone")
		opts.Milestone = &val
	}
	if ctx.IsSet("deadline") {
		date := ctx.String("deadline")
		if date == "" {
			opts.Deadline = &time.Time{}
		} else {
			t, err := dateparse.ParseAny(date)
			if err != nil {
				return nil, err
			}
			opts.Deadline = &t
		}
	}
	if ctx.IsSet("add-assignees") {
		val := ctx.String("add-assignees")
		opts.AddAssignees = strings.Split(val, ",")
	}
	if ctx.IsSet("add-labels") {
		val := ctx.String("add-labels")
		opts.AddLabels = strings.Split(val, ",")
	}
	if ctx.IsSet("remove-labels") {
		val := ctx.String("remove-labels")
		opts.RemoveLabels = strings.Split(val, ",")
	}
	return &opts, nil
}
