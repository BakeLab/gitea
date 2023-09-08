// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package flags

import (
	"github.com/urfave/cli/v2"
)

// LoginFlag provides flag to specify tea login profile
var LoginFlag = cli.StringFlag{
	Name:    "login",
	Aliases: []string{"l"},
	Usage:   "Use a different Gitea Login. Optional",
}

// RepoFlag provides flag to specify repository
var RepoFlag = cli.StringFlag{
	Name:    "repo",
	Aliases: []string{"r"},
	Usage:   "Override local repository path or gitea repository slug to interact with. Optional",
}

// RemoteFlag provides flag to specify remote repository
var RemoteFlag = cli.StringFlag{
	Name:    "remote",
	Aliases: []string{"R"},
	Usage:   "Discover Gitea login from remote. Optional",
}

// OutputFlag provides flag to specify output type
var OutputFlag = cli.StringFlag{
	Name:    "output",
	Aliases: []string{"o"},
	Usage:   "Output format. (simple, table, csv, tsv, yaml, json)",
}

// PaginationPageFlag provides flag for pagination options
var PaginationPageFlag = cli.StringFlag{
	Name:    "page",
	Aliases: []string{"p"},
	Usage:   "specify page, default is 1",
}

// PaginationLimitFlag provides flag for pagination options
var PaginationLimitFlag = cli.StringFlag{
	Name:    "limit",
	Aliases: []string{"lm"},
	Usage:   "specify limit of items per page",
}

// LoginOutputFlags defines login and output flags that should
// added to all subcommands and appended to the flags of the
// subcommand to work around issue and provide --login and --output:
// https://github.com/urfave/cli/issues/585
var LoginOutputFlags = []cli.Flag{
	&LoginFlag,
	&OutputFlag,
}

// LoginRepoFlags defines login and repo flags that should
// be used for all subcommands and appended to the flags of
// the subcommand to work around issue and provide --login and --repo:
// https://github.com/urfave/cli/issues/585
var LoginRepoFlags = []cli.Flag{
	&LoginFlag,
	&RepoFlag,
	&RemoteFlag,
}

// AllDefaultFlags defines flags that should be available
// for all subcommands working with dedicated repositories
// to work around issue and provide --login, --repo and --output:
// https://github.com/urfave/cli/issues/585
var AllDefaultFlags = append([]cli.Flag{
	&RepoFlag,
	&RemoteFlag,
}, LoginOutputFlags...)

// NotificationFlags defines flags that should be available on notifications.
var NotificationFlags = append([]cli.Flag{
	NotificationStateFlag,
	&cli.BoolFlag{
		Name:    "mine",
		Aliases: []string{"m"},
		Usage:   "Show notifications across all your repositories instead of the current repository only",
	},
	&PaginationPageFlag,
	&PaginationLimitFlag,
}, AllDefaultFlags...)

// NotificationStateFlag is a csv flag applied to all notification subcommands as filter
var NotificationStateFlag = NewCsvFlag(
	"states",
	"notification states to filter by",
	[]string{"s"},
	[]string{"pinned", "unread", "read"},
	[]string{"unread", "pinned"},
)

// FieldsFlag generates a flag selecting printable fields.
// To retrieve the value, use f.GetValues()
func FieldsFlag(availableFields, defaultFields []string) *CsvFlag {
	return NewCsvFlag("fields", "fields to print", []string{"f"}, availableFields, defaultFields)
}
