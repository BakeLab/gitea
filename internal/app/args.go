// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func number(raw string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid resource number %q", raw))
	}
	return n, nil
}

func resourceNumbers(args []string) ([]int64, error) {
	result := make([]int64, len(args))
	seen := make(map[int64]bool, len(args))
	for i, arg := range args {
		n, err := number(arg)
		if err != nil {
			return nil, err
		}
		if seen[n] {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("resource %d is selected more than once; supply unique numbers", n))
		}
		seen[n] = true
		result[i] = n
	}
	return result, nil
}

// requireNames trims name-list flag entries in place and rejects blanks; hint explains how to clear instead.
func requireNames(kind, hint string, lists ...[]string) error {
	trimNames(lists...)
	if slices.Contains(slices.Concat(lists...), "") {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s names cannot be empty%s", kind, hint))
	}
	return nil
}

// trimNames removes surrounding whitespace from each entry in place, since Gitea rejects padded names.
func trimNames(lists ...[]string) {
	for _, list := range lists {
		for i, name := range list {
			list[i] = strings.TrimSpace(name)
		}
	}
}

// labelFilter splits a comma-separated label filter, trimming entries and rejecting blanks.
func labelFilter(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	names := strings.Split(raw, ",")
	return names, requireNames("label", "", names)
}

// oneOf rejects an enum flag value outside allowed, which needs at least two entries.
func oneOf(flag, value string, allowed ...string) error {
	if slices.Contains(allowed, value) {
		return nil
	}
	last := len(allowed) - 1
	choices := strings.Join(allowed[:last], ", ")
	if last > 1 {
		choices += ","
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s must be %s or %s", flag, choices, allowed[last]))
}

// pageFlags registers --limit and --page, which reject out-of-range values while parsing.
func pageFlags(cmd *cobra.Command, limit, page *int) {
	positiveIntVar(cmd, limit, "limit", 30, 100, "Maximum results (1..100)")
	positiveIntVar(cmd, page, "page", 1, math.MaxInt, "Result page")
}

// positiveIntVar registers an int flag that rejects values outside 1..upper while parsing.
func positiveIntVar(cmd *cobra.Command, value *int, name string, initial, upper int, usage string) {
	*value = initial
	cmd.Flags().Var(positiveInt{value, upper}, name, usage)
}

type positiveInt struct {
	value *int
	upper int
}

func (f positiveInt) String() string { return strconv.Itoa(*f.value) }

func (f positiveInt) Type() string { return "int" }

func (f positiveInt) Set(raw string) error {
	n, err := strconv.Atoi(raw)
	switch {
	case (err != nil || n < 1) && f.upper == math.MaxInt:
		return errors.New("must be a positive integer")
	case err != nil || n < 1 || n > f.upper:
		return fmt.Errorf("must be 1..%d", f.upper)
	}
	*f.value = n
	return nil
}

func parseRunTime(value string, now time.Time) (*time.Time, error) {
	if duration, err := time.ParseDuration(value); err == nil {
		if duration < 0 {
			return nil, errors.New("relative duration cannot be negative")
		}
		result := now.Add(-duration)
		return &result, nil
	}
	for _, format := range []string{time.RFC3339Nano, "2006-01-02", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
		if result, err := time.ParseInLocation(format, value, now.Location()); err == nil {
			return &result, nil
		}
	}
	return nil, errors.New("use a date, RFC3339 timestamp, or duration such as 24h")
}

// timeRange is a pair of lower and upper time bound flags.
type timeRange struct {
	flags  *pflag.FlagSet
	names  [2]string
	values [2]string
}

// register adds the lower and upper bound flags to cmd.
func (r *timeRange) register(cmd *cobra.Command, lower, lowerUsage, upper, upperUsage string) {
	r.flags, r.names = cmd.Flags(), [2]string{lower, upper}
	r.flags.StringVar(&r.values[0], lower, "", lowerUsage)
	r.flags.StringVar(&r.values[1], upper, "", upperUsage)
}

// parse returns the bounds that were set and rejects inverted ranges.
func (r *timeRange) parse(now time.Time) (since, until *time.Time, err error) {
	var bounds [2]*time.Time
	for i, flag := range r.names {
		if !r.flags.Changed(flag) {
			continue
		}
		if bounds[i], err = parseRunTime(r.values[i], now); err != nil {
			return nil, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --%s: %w", flag, err))
		}
	}
	if bounds[0] != nil && bounds[1] != nil && bounds[0].After(*bounds[1]) {
		return nil, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("--%s must not be later than --%s", r.names[0], r.names[1]))
	}
	return bounds[0], bounds[1], nil
}

// openRegularFiles opens upload sources, rejecting anything but regular files.
// Callers close the returned files even on error.
func openRegularFiles(paths []string) ([]*os.File, error) {
	files := make([]*os.File, 0, len(paths))
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return files, err
		}
		files = append(files, file)
		info, err := file.Stat()
		if err != nil {
			return files, err
		}
		if !info.Mode().IsRegular() {
			return files, clierr.Wrap(clierr.Usage, fmt.Errorf("%s is not a regular file", path))
		}
	}
	return files, nil
}

func closeFiles(files []*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}

func argsExact(n int) cobra.PositionalArgs {
	return usageArgs(cobra.ExactArgs(n))
}

// usageArgs reports positional argument errors with the usage exit code.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		return clierr.Wrap(clierr.Usage, validate(cmd, args))
	}
}

// localFlagsChanged reports whether any command-specific flag was supplied.
func localFlagsChanged(cmd *cobra.Command) bool {
	changed := false
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) { changed = changed || flag.Changed })
	return changed
}
