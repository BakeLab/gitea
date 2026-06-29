// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package comments

import "io"

// resolveBody picks the comment body from the non-interactive sources, in
// precedence order:
//
//  1. the positional argument (kept first for back-compat with the historical
//     'tea comment <idx> "<body>"' shorthand),
//  2. the -d/--description flag (mirrors the body flag on 'issue create',
//     'issue edit' and 'pr create'),
//  3. piped stdin.
//
// stdin is only read when stdinPiped is true (a non-TTY stdin, e.g. CI,
// subshells or agent harnesses) and no body was supplied otherwise, so the
// command never blocks reading an interactive terminal when a body is already
// given. An empty result means the caller should fall back to the editor (when
// interactive) or error out.
func resolveBody(positional, description string, stdinPiped bool, stdin io.Reader) (string, error) {
	if len(positional) != 0 {
		return positional, nil
	}
	if len(description) != 0 {
		return description, nil
	}
	if stdinPiped {
		stdinBytes, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(stdinBytes), nil
	}
	return "", nil
}
