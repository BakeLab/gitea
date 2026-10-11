// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strconv"
	"time"

	"gitea.dev/tea/internal/clierr"
	"github.com/araddon/dateparse"
)

// parseDeadline parses a --deadline value; an empty value requests clearing.
// Supplied timezone offsets are kept and Unix timestamps are UTC.
func parseDeadline(value string) (*time.Time, *bool, error) {
	if value == "" {
		return nil, new(true), nil
	}
	date, err := dateparse.ParseAny(value)
	if _, numericError := strconv.ParseInt(value, 10, 64); numericError == nil {
		date = date.UTC()
	}
	if err != nil || date.IsZero() || date.Unix() == 0 || date.Year() < 1 || date.Year() > 9999 {
		return nil, nil, clierr.Wrap(clierr.Usage, errors.New("invalid --deadline; use YYYY-MM-DD, RFC3339, a Unix timestamp, or an empty value to clear"))
	}
	return &date, nil, nil
}
