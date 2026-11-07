// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package aliases expands built-in command shortcuts without executing a shell.
package aliases

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

var (
	placeholder = regexp.MustCompile(`\$([1-9][0-9]*)`)
	namePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
)

// maxDepth bounds alias expansion even when configuration is edited by hand.
const maxDepth = 16

// validate checks names, quoting, and unsupported shell syntax.
func validate(name, expansion string, builtins map[string]bool) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid alias name %q", name)
	}
	if builtins[name] {
		return fmt.Errorf("alias %q collides with a built-in command or an extension", name)
	}
	words, err := shellquote.Split(expansion)
	if err != nil {
		return fmt.Errorf("invalid alias expansion: %w", err)
	}
	if len(words) == 0 {
		return errors.New("alias expansion cannot be empty")
	}
	if strings.HasPrefix(words[0], "!") {
		return errors.New("shell aliases are not supported")
	}
	// Expansion never runs a shell; rejecting common operators only flags misleading shortcuts.
	for _, word := range words {
		switch word {
		case "|", "||", "&", "&&", ";", "<", ">", ">>", "2>":
			return errors.New("shell aliases are not supported")
		}
	}
	return nil
}

// Check validates an alias and every alias it expands through until a built-in.
func Check(name string, aliases map[string]string, builtins map[string]bool) error {
	seen := map[string]bool{}
	for current := name; ; {
		expansion, ok := aliases[current]
		// A built-in always wins over an alias, except for the invoked name itself, which
		// validate reports as a collision.
		if !ok || (current != name && builtins[current]) {
			if !builtins[current] {
				return fmt.Errorf("alias %q targets unknown command %q", name, current)
			}
			return nil
		}
		if seen[current] {
			return fmt.Errorf("alias cycle involving %q", current)
		}
		if len(seen) >= maxDepth {
			return fmt.Errorf("alias nesting exceeds %d", maxDepth)
		}
		if err := validate(current, expansion, builtins); err != nil {
			return err
		}
		seen[current] = true
		// validate parsed this expansion without error.
		words, _ := shellquote.Split(expansion)
		current = words[0]
	}
}

// Expand substitutes arguments without re-parsing or executing user input.
// Only the invoked alias is checked, so one broken entry cannot block other commands.
func Expand(args []string, aliases map[string]string, builtins map[string]bool) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	if _, ok := aliases[args[0]]; ok && !builtins[args[0]] {
		if err := Check(args[0], aliases, builtins); err != nil {
			return nil, err
		}
	}
	for {
		expansion, exists := aliases[args[0]]
		if !exists || builtins[args[0]] {
			return args, nil
		}
		// Check validated every expansion reachable from the invoked alias.
		words, _ := shellquote.Split(expansion)
		supplied := args[1:]
		consumed := map[int]bool{}
		for i, word := range words {
			var substitutionErr error
			words[i] = placeholder.ReplaceAllStringFunc(word, func(match string) string {
				n, err := strconv.Atoi(match[1:])
				if err != nil || n > len(supplied) {
					substitutionErr = fmt.Errorf("alias requires argument %s", match)
					return ""
				}
				consumed[n-1] = true
				return supplied[n-1]
			})
			if substitutionErr != nil {
				return nil, substitutionErr
			}
		}
		for i, arg := range supplied {
			if !consumed[i] {
				words = append(words, arg)
			}
		}
		args = words
	}
}

// ExpandCompletion keeps the in-progress word last, as Cobra's completion protocol requires.
// Fixed flags after a substituted argument must not become the completion prefix.
func ExpandCompletion(args []string, definitions map[string]string, builtins map[string]bool) ([]string, error) {
	if len(args) < 2 {
		return append([]string{}, args...), nil
	}
	const marker = "\x00gt-completion\x00"
	marked := append([]string{}, args...)
	current := marked[len(marked)-1]
	marked[len(marked)-1] = marker
	expanded, err := Expand(marked, definitions, builtins)
	if err != nil {
		return nil, err
	}
	position := -1
	for index, word := range expanded {
		if strings.Contains(word, marker) {
			if word != marker || position != -1 {
				return nil, errors.New("completion of embedded or repeated placeholders is ambiguous")
			}
			position = index
		}
	}
	if position == -1 {
		return nil, errors.New("alias completion lost its current argument")
	}
	result := append([]string{}, expanded[:position]...)
	result = append(result, expanded[position+1:]...)
	return append(result, current), nil
}
