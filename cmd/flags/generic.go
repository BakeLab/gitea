// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package flags

import (
	"errors"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
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

var (
	paging gitea.ListOptions
	// ErrPage indicates that the provided page value is invalid (less than -1 or equal to 0).
	ErrPage = errors.New("page cannot be smaller than 1")
	// ErrLimit indicates that the provided limit value is invalid (negative).
	ErrLimit = errors.New("limit cannot be negative")
)

// GetListOptions returns configured paging struct
func GetListOptions() gitea.ListOptions {
	return paging
}

// PaginationFlags provides all pagination related flags
var PaginationFlags = []cli.Flag{
	&PaginationPageFlag,
	&PaginationLimitFlag,
}

// PaginationPageFlag provides flag for pagination options
var PaginationPageFlag = cli.IntFlag{
	Name:    "page",
	Aliases: []string{"p"},
	Usage:   "specify page",
	Value:   1,
	Validator: func(i int) error {
		if i < 1 && i != -1 {
			return ErrPage
		}
		return nil
	},
	Destination: &paging.Page,
}

// PaginationLimitFlag provides flag for pagination options
var PaginationLimitFlag = cli.IntFlag{
	Name:    "limit",
	Aliases: []string{"lm"},
	Usage:   "specify limit of items per page",
	Value:   30,
	Validator: func(i int) error {
		if i < 0 {
			return ErrLimit
		}
		return nil
	},
	Destination: &paging.PageSize,
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
