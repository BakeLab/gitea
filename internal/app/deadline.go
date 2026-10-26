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
	"github.com/spf13/cobra"
)

// numericDate matches dates such as 03/04/2026, which read as March 4 in the US and April 3
// elsewhere.
var numericDate = regexp.MustCompile(`^\d{1,2}[/.]\d{1,2}[/.]\d{2,4}(?:[ T].*)?$`)

// parseDeadline parses a --deadline value into the end of its calendar day in UTC: the date as
// written, in its own offset, or in UTC for a Unix timestamp. Gitea's issue edits move a due date
// to the end of its day in the offset sent and milestone edits to the end of its day in UTC, while
// creating stores it as sent, so only the end of the UTC day reads the same everywhere. Edit
// commands treat an empty value as clearing before calling it.
func parseDeadline(value string) (*time.Time, error) {
	if value == "" {
		return nil, clierr.Wrap(clierr.Usage, errors.New("--deadline cannot be empty"))
	}
	if numericDate.MatchString(value) {
		return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --deadline %q: the order of day and month is ambiguous; use YYYY-MM-DD", value))
	}
	date, err := dateparse.ParseAny(value)
	if n, numericError := strconv.ParseInt(value, 10, 64); numericError == nil {
		date, err = unixTime(n)
	}
	if err != nil || date.IsZero() || date.Unix() == 0 || date.Year() < 1 || date.Year() > 9999 {
		return nil, clierr.Wrap(clierr.Usage, errors.New("invalid --deadline; use YYYY-MM-DD, RFC3339 or a Unix timestamp"))
	}
	date = endOfDay(date)
	return &date, nil
}

// unixTime reads n as a Unix timestamp in seconds, or by its size in milli-, micro- or
// nanoseconds. A number below 10⁸ is refused, as more likely a mistyped date than one in 1970.
func unixTime(n int64) (time.Time, error) {
	switch {
	case n < 1e8:
		return time.Time{}, errors.New("timestamp too small")
	case n < 1e12:
		return time.Unix(n, 0).UTC(), nil
	case n < 1e15:
		return time.UnixMilli(n).UTC(), nil
	case n < 1e18:
		return time.UnixMicro(n).UTC(), nil
	}
	return time.Unix(0, n).UTC(), nil
}

// endOfDay is the last second, in UTC, of the calendar day of date in its own location.
func endOfDay(date time.Time) time.Time {
	year, month, day := date.Date()
	return time.Date(year, month, day, 23, 59, 59, 0, time.UTC)
}

// editDeadline reads the --deadline of an edit command, where an empty value clears the deadline.
func editDeadline(cmd *cobra.Command, value string) (*time.Time, *bool, error) {
	switch {
	case !cmd.Flags().Changed("deadline"):
		return nil, nil, nil
	case value == "":
		return nil, new(true), nil
	}
	deadline, err := parseDeadline(value)
	return deadline, nil, err
}
