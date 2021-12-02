// Copyright 2021 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package flags

import (
	"fmt"
	"strings"

	"code.gitea.io/tea/modules/utils"
	"github.com/urfave/cli/v2"
)

// CsvFlag is a wrapper around cli.StringFlag, with an added GetValues() method
// to retrieve comma separated string values as a slice.
type CsvFlag struct {
	cli.StringFlag
	AvailableFields []string
}

// NewCsvFlag creates a CsvFlag, while setting its usage string and default values
func NewCsvFlag(name, usage string, aliases, availableValues, defaults []string) *CsvFlag {
	var availableDesc string
	if len(availableValues) != 0 {
		availableDesc = " Available values:"
	}
	return &CsvFlag{
		AvailableFields: availableValues,
		StringFlag: cli.StringFlag{
			Name:    name,
			Aliases: aliases,
			Value:   strings.Join(defaults, ","),
			Usage: fmt.Sprintf(`Comma-separated list of %s.%s
			%s
		`, usage, availableDesc, strings.Join(availableValues, ",")),
		},
	}
}

// GetValues returns the value of the flag, parsed as a commaseparated list
func (f CsvFlag) GetValues(ctx *cli.Context) ([]string, error) {
	val := ctx.String(f.Name)
	selection := strings.Split(val, ",")
	if f.AvailableFields != nil && val != "" {
		for _, field := range selection {
			if !utils.Contains(f.AvailableFields, field) {
				return nil, fmt.Errorf("Invalid field '%s'", field)
			}
		}
	}
	return selection, nil
}
