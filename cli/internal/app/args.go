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

// number parses a resource number written as N or #N.
func number(raw string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(raw, "#"), 10, 64)
	if err != nil || n < 1 {
		return 0, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid resource number %q", raw))
	}
	return n, nil
}

// refuseRepeats rejects a selection that two arguments address, such as a title and its ID:
// found holds the item each of args selected.
func refuseRepeats[T any](kind string, args []string, found []T, id func(T) int64) error {
	for i, item := range found {
		if slices.ContainsFunc(found[:i], func(other T) bool { return id(other) == id(item) }) {
			return clierr.Wrap(clierr.Usage, errors.New(kind+" "+args[i]+" is selected more than once"))
		}
	}
	return nil
}

// resourceNumbers parses the numbers of the kind of resource, such as "workflow", that args select,
// refusing a repeated one.
func resourceNumbers(kind string, args []string) ([]int64, error) {
	result := make([]int64, len(args))
	seen := make(map[int64]bool, len(args))
	for i, arg := range args {
		n, err := number(arg)
		if err != nil {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid %s number %q", kind, arg))
		}
		if seen[n] {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("%s %d is selected more than once; supply unique numbers", kind, n))
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

// maxLimit bounds --limit; list services page through the server's smaller pages to reach it.
const maxLimit = 1000

// maxPage bounds --page so that the item offset of a page (page * limit) cannot overflow an int.
const maxPage = math.MaxInt / (2 * maxLimit)

// pageFlags registers -L/--limit and --page, which reject out-of-range values while parsing.
func pageFlags(cmd *cobra.Command, limit, page *int) {
	*limit = 30
	cmd.Flags().VarP(positiveInt{limit, maxLimit}, "limit", "L", "Maximum number of results (1..1000)")
	positiveIntVar(cmd, page, "page", 1, maxPage, "Result page")
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
	case (err != nil || n < 1) && (f.upper == math.MaxInt || f.upper == maxPage):
		return errors.New("must be a positive integer")
	case err != nil || n < 1 || n > f.upper:
		return fmt.Errorf("must be 1..%d", f.upper)
	}
	*f.value = n
	return nil
}

func parseRunTime(value string, now time.Time) (*time.Time, error) {
	if duration, err := durationAgo(value); err == nil {
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

// durationAgo parses a duration like time.ParseDuration, and also whole days and weeks: 7d, 2w.
func durationAgo(value string) (time.Duration, error) {
	for unit, size := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if count, ok := strings.CutSuffix(value, unit); ok {
			n, err := strconv.Atoi(count)
			if err != nil {
				return 0, err
			}
			return time.Duration(n) * size, nil
		}
	}
	return time.ParseDuration(value)
}

// timeRange is a pair of --since and --until time bound flags.
type timeRange struct {
	flags  *pflag.FlagSet
	values [2]string
}

// register adds the --since and --until flags to cmd.
func (r *timeRange) register(cmd *cobra.Command, sinceUsage, untilUsage string) {
	r.flags = cmd.Flags()
	r.flags.StringVar(&r.values[0], "since", "", sinceUsage)
	r.flags.StringVar(&r.values[1], "until", "", untilUsage)
}

// parse returns the bounds that were set and rejects inverted ranges.
func (r *timeRange) parse(now time.Time) (since, until *time.Time, err error) {
	var bounds [2]*time.Time
	for i, flag := range [2]string{"since", "until"} {
		if !r.flags.Changed(flag) {
			continue
		}
		if bounds[i], err = parseRunTime(r.values[i], now); err != nil {
			return nil, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --%s: %w", flag, err))
		}
	}
	if bounds[0] != nil && bounds[1] != nil && bounds[0].After(*bounds[1]) {
		return nil, nil, clierr.Wrap(clierr.Usage, errors.New("--since must not be later than --until"))
	}
	return bounds[0], bounds[1], nil
}

// renameFlag adds old as a hidden alias of the string flag name, for scripts written before the rename.
func renameFlag(cmd *cobra.Command, old, name string) {
	cmd.Flags().String(old, "", "Deprecated: use --"+name)
	_ = cmd.Flags().MarkHidden(old)
}

// renamedFlag moves a value given under the old name of a flag renamed by renameFlag to its new name.
func (a *application) renamedFlag(cmd *cobra.Command, old, name string) error {
	flags := cmd.Flags()
	if !flags.Changed(old) {
		return nil
	}
	if flags.Changed(name) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be combined with --%s", name, old))
	}
	a.warn(cmd, "--%s is deprecated; use --%s", old, name)
	return flags.Set(name, flags.Lookup(old).Value.String())
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

// usageArgs reports positional argument errors with the usage exit code, the usage line and a
// pointer to the command's help.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		err := validate(cmd, args)
		if err == nil {
			return nil
		}
		return clierr.Wrap(clierr.Usage, &argumentError{problem: argumentProblem(cmd, args, err), cmd: cmd})
	}
}

// setFlags returns the names of the flags that are set, in order.
func setFlags(names []string, set ...bool) []string {
	var result []string
	for i, name := range names {
		if set[i] {
			result = append(result, name)
		}
	}
	return result
}

// nonBlankArgs is usageArgs(validate) that also rejects blank arguments before any request,
// naming the argument at each position by names; the last name covers further arguments.
func nonBlankArgs(validate cobra.PositionalArgs, names ...string) cobra.PositionalArgs {
	validate = usageArgs(validate)
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return err
		}
		for i, arg := range args {
			if strings.TrimSpace(arg) == "" {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be empty", names[min(i, len(names)-1)]))
			}
		}
		return nil
	}
}

// argumentError reports a positional argument problem followed by the command's usage.
type argumentError struct {
	problem string
	cmd     *cobra.Command
}

func (e *argumentError) Error() string {
	more := "for more information"
	if e.cmd.Example != "" {
		more = "for examples"
	}
	return fmt.Sprintf("%s\nUsage: %s\nRun '%s --help' %s.", e.problem, e.cmd.UseLine(), e.cmd.CommandPath(), more)
}

// argumentProblem names what is wrong with args from the arguments in the command's Use, such
// as "missing issue number", falling back to the validator's message.
func argumentProblem(cmd *cobra.Command, args []string, err error) string {
	arguments := usageArguments(cmd.Use)
	required, variadic := 0, false
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "[") {
			required++
		}
		variadic = variadic || strings.HasSuffix(strings.TrimRight(argument, "]}>"), "...")
	}
	switch {
	case len(args) < required:
		return "missing " + argumentName(cmd, arguments[len(args)])
	case len(arguments) == 0 && len(args) > 0:
		return fmt.Sprintf("unexpected argument %q", args[0])
	case !variadic && len(arguments) > 0 && len(args) > len(arguments):
		return "too many arguments"
	}
	return err.Error()
}

// usageArguments splits the arguments of a Use line, such as "view <number>", into its
// top-level groups: "<number>", "[<number> | <url>]" or "<file>...". [flags] is not one.
func usageArguments(use string) []string {
	_, rest, _ := strings.Cut(use, " ")
	var arguments []string
	depth, start := 0, -1
	for i, r := range rest {
		switch {
		case strings.ContainsRune("<[{", r):
			if depth == 0 {
				start = i
			}
			depth++
		case strings.ContainsRune(">]}", r):
			depth--
		}
		if depth == 0 && start >= 0 && (i == len(rest)-1 || rest[i+1] == ' ') {
			if argument := rest[start : i+1]; argument != "[flags]" {
				arguments = append(arguments, argument)
			}
			start = -1
		}
	}
	return arguments
}

// argumentName words a Use argument for an error: "<asset-id>" is "asset ID", and a bare
// "<number>" or "<id>" names the command group's resource, as in "issue number".
func argumentName(cmd *cobra.Command, argument string) string {
	first, _, _ := strings.Cut(argument, "|")
	name := strings.Trim(first, "<>[]{}. ")
	words := strings.Split(name, "-")
	for i, word := range words {
		if word == "id" {
			words[i] = "ID"
		}
	}
	text := strings.Join(words, " ")
	if name != "number" && name != "id" && name != "id-or-name" {
		return text
	}
	group := cmd
	for group.HasParent() && group.Parent().HasParent() {
		group = group.Parent()
	}
	if group == cmd {
		return text
	}
	noun := group.Name()
	// The numbers under `time` and `milestone issue` are issue numbers.
	if noun == "time" || slices.ContainsFunc(ancestors(cmd), func(c *cobra.Command) bool { return c.Name() == "issue" }) {
		noun = "issue"
	}
	if noun == "pr" {
		noun = "pull request"
	}
	return noun + " " + text
}

// ancestors returns the parents of cmd below the root command.
func ancestors(cmd *cobra.Command) []*cobra.Command {
	var parents []*cobra.Command
	for parent := cmd.Parent(); parent != nil && parent.HasParent(); parent = parent.Parent() {
		parents = append(parents, parent)
	}
	return parents
}

// localFlagsChanged reports whether a command-specific flag other than those in except was supplied.
func localFlagsChanged(cmd *cobra.Command, except ...string) bool {
	changed := false
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		changed = changed || flag.Changed && !slices.Contains(except, flag.Name)
	})
	return changed
}

// onlyFlagsWith fails when the flag mode, such as --web, is combined with a flag other than
// allowed; passes says what mode passes on, such as "the browser only receives the title".
func onlyFlagsWith(cmd *cobra.Command, mode, passes string, allowed ...string) error {
	var rejected string
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if rejected == "" && flag.Changed && flag.Name != mode && !slices.Contains(allowed, flag.Name) {
			rejected = flag.Name
		}
	})
	if rejected == "" {
		return nil
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be combined with --%s; %s", rejected, mode, passes))
}

// requireWebFlags is onlyFlagsWith for --web, naming the flags the browser receives.
func requireWebFlags(cmd *cobra.Command, allowed ...string) error {
	return onlyFlagsWith(cmd, "web", "the browser only receives --"+strings.Join(allowed, ", --"), allowed...)
}
