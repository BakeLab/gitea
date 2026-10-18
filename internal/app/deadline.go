// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"gitea.dev/cli/internal/clierr"
	"github.com/araddon/dateparse"
)

// numericDate matches dates such as 03/04/2026, which read as March 4 in the US and April 3
// elsewhere.
var numericDate = regexp.MustCompile(`^\d{1,2}[/.]\d{1,2}[/.]\d{2,4}(?:[ T].*)?$`)

// parseDeadline parses a --deadline value; an empty value requests clearing.
// Supplied timezone offsets are kept and Unix timestamps are UTC.
func parseDeadline(value string) (*time.Time, *bool, error) {
	if value == "" {
		return nil, new(true), nil
	}
	if numericDate.MatchString(value) {
		return nil, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --deadline %q: the order of day and month is ambiguous; use YYYY-MM-DD", value))
	}
	date, err := dateparse.ParseAny(value)
	if _, numericError := strconv.ParseInt(value, 10, 64); numericError == nil {
		date = date.UTC()
	}
	if err != nil || date.IsZero() || date.Unix() == 0 || date.Year() < 1 || date.Year() > 9999 {
		return nil, nil, clierr.Wrap(clierr.Usage, errors.New("invalid --deadline; use YYYY-MM-DD, RFC3339 or a Unix timestamp"))
	}
	return &date, nil, nil
}
