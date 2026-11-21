// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"io"
	"strings"
	"sync"
)

// ReadCache lets a System spawn each repository probe once per command, since a Git process is the
// largest local cost of one. Any Git command outside the probes it knows empties it, so that a
// command that changes the repository reads the change back.
type ReadCache struct {
	mu      sync.Mutex
	results map[readKey]readResult
	configs map[string]map[string]string
}

type readKey struct{ dir, args string }

type readResult struct {
	output string
	err    error
}

func (c *ReadCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results, c.configs = nil, nil
}

// cacheableRead reports whether args only read what the cache holds; other forms of these
// subcommands can write.
func cacheableRead(args []string) bool {
	switch len(args) {
	case 1:
		return args[0] == "remote"
	case 3:
		return args[0] == "symbolic-ref" && args[1] == "--quiet" && args[2] == "HEAD" ||
			args[0] == "remote" && args[1] == "get-url" ||
			args[0] == "config" && args[1] == "--get"
	}
	return false
}

// read answers args, a cacheableRead, from the cache or run. All of a directory's config keys
// come from one listing; a listing that fails leaves each key to its own run.
func (c *ReadCache) read(ctx context.Context, dir string, args []string, run func(...string) (string, error)) (string, error) {
	if args[0] == "config" {
		if values, ok := c.config(ctx, dir, run); ok {
			name, known := configKey(args[2])
			if known {
				if value, set := values[name]; set {
					return value, nil
				}
				return "", &CommandError{ExitCode: 1}
			}
		}
	}
	key := readKey{dir, strings.Join(args, "\x00")}
	c.mu.Lock()
	result, ok := c.results[key]
	c.mu.Unlock()
	if ok {
		return result.output, result.err
	}
	output, err := run(args...)
	if ctx.Err() != nil {
		return output, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.results == nil {
		c.results = map[readKey]readResult{}
	}
	c.results[key] = readResult{output, err}
	return output, err
}

func (c *ReadCache) config(ctx context.Context, dir string, run func(...string) (string, error)) (map[string]string, bool) {
	c.mu.Lock()
	values, ok := c.configs[dir]
	c.mu.Unlock()
	if ok {
		return values, values != nil
	}
	listing, err := run("config", "--list", "-z")
	if ctx.Err() != nil {
		return nil, false
	}
	if err == nil {
		values = parseConfigList(listing)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.configs == nil {
		c.configs = map[string]map[string]string{}
	}
	c.configs[dir] = values
	return values, values != nil
}

// parseConfigList reads git config --list -z into values as config --get answers them: the last
// value of a key wins, trimmed like Output, and a key without a value is empty.
func parseConfigList(listing string) map[string]string {
	values := map[string]string{}
	for entry := range strings.SplitSeq(listing, "\x00") {
		if entry != "" {
			key, value, _ := strings.Cut(entry, "\n")
			values[key] = strings.TrimSpace(value)
		}
	}
	return values
}

// configKey returns name as git config --list prints it: section and variable in lower case, and
// the subsection between them as given. It reports false for a name that has no variable.
func configKey(name string) (string, bool) {
	first, last := strings.IndexByte(name, '.'), strings.LastIndexByte(name, '.')
	if first < 0 {
		return "", false
	}
	return strings.ToLower(name[:first]) + name[first:last] + strings.ToLower(name[last:]), true
}

// Warm reads the probes of repository discovery in dir concurrently, so that together they cost
// about one Git process, for a System with a ReadCache that probes silently.
func (s System) Warm(ctx context.Context, dir string) {
	if s.Reads == nil || s.ErrOut != io.Discard {
		return
	}
	var wg sync.WaitGroup
	for _, args := range [][]string{{"config", "--get", "gt.repository"}, {"remote"}, {"symbolic-ref", "--quiet", "HEAD"}} {
		wg.Go(func() { _, _ = s.Output(ctx, dir, false, args...) })
	}
	wg.Wait()
}
