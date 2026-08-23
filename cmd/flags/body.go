// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package flags

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// stdinPiped reports whether stdin is not a terminal, e.g. when a description
// is piped from a file, command substitution, or a CI harness.
func stdinPiped() bool {
	return !term.IsTerminal(int(os.Stdin.Fd()))
}

// resolveCreateBody returns the issue/PR description for create commands.
//
// Precedence:
//  1. --description-file (read from the file, or stdin when the path is "-")
//  2. --description
//  3. piped stdin
func resolveCreateBody(description, descriptionFile string, descriptionFileSet, stdinPiped bool, stdin io.Reader) (string, error) {
	if descriptionFileSet {
		return readDescriptionSource(descriptionFile, stdin)
	}
	if description != "" {
		return description, nil
	}
	if stdinPiped {
		return readDescriptionStdin(stdin)
	}
	return "", nil
}

// resolveEditBody returns the new issue/PR body when a description flag was
// provided, or nil when the caller should leave the body unchanged.
func resolveEditBody(description string, descriptionSet bool, descriptionFile string, descriptionFileSet bool, stdin io.Reader) (*string, error) {
	if descriptionFileSet {
		body, err := readDescriptionSource(descriptionFile, stdin)
		if err != nil {
			return nil, err
		}
		return &body, nil
	}
	if descriptionSet {
		body := description
		return &body, nil
	}
	return nil, nil
}

func readDescriptionSource(source string, stdin io.Reader) (string, error) {
	if source == "-" {
		return readDescriptionStdin(stdin)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("could not read description file %q: %w", source, err)
	}
	return string(data), nil
}

func readDescriptionStdin(stdin io.Reader) (string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("could not read description from stdin: %w", err)
	}
	return string(data), nil
}
