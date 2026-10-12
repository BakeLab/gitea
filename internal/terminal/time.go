// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"fmt"
	"time"
)

// RelativeTime describes t relative to now like gh: "less than a minute ago", "about 3 hours ago",
// and "in about 2 months" for future times such as due dates.
func RelativeTime(now, t time.Time) string {
	d, prefix, suffix := now.Sub(t), "", " ago"
	if d < 0 {
		d, prefix, suffix = -d, "in ", ""
	}
	const day = 24 * time.Hour
	var n time.Duration
	var unit string
	switch {
	case d < time.Minute:
		return prefix + "less than a minute" + suffix
	case d < time.Hour:
		n, unit = d/time.Minute, "minute"
	case d < day:
		n, unit = d/time.Hour, "hour"
	case d < 30*day:
		n, unit = d/day, "day"
	case d < 365*day:
		n, unit = d/(30*day), "month"
	default:
		n, unit = d/(365*day), "year"
	}
	return prefix + "about " + Plural(int(n), unit) + suffix
}

// Timestamp formats t for scripts: RFC3339 in UTC at second precision.
func Timestamp(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// ParseTime parses an API timestamp, returning the zero time for an empty or malformed value.
func ParseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Plural counts n of a unit, as in "1 fork" or "3 forks".
func Plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
