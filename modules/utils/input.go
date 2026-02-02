// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// ReadValueOptions contains options for reading a value from various sources
type ReadValueOptions struct {
	// ResourceName is the name of the resource (e.g., "secret", "variable")
	ResourceName string
	// PromptMsg is the message to display when prompting interactively
	PromptMsg string
	// Hidden determines if the input should be hidden (for secrets/passwords)
	Hidden bool
	// AllowEmpty determines if empty values are allowed
	AllowEmpty bool
}

// ReadValue reads a value from various sources in the following priority order:
// 1. From a file specified by --file flag
// 2. From stdin if --stdin flag is set
// 3. From command arguments (second argument)
// 4. Interactive prompt
func ReadValue(cmd *cli.Command, opts ReadValueOptions) (string, error) {
	var value string

	// 1. Read from file
	if filePath := cmd.String("file"); filePath != "" {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", err)
		}
		value = strings.TrimSpace(string(content))
	} else if cmd.Bool("stdin") {
		// 2. Read from stdin
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		value = strings.TrimSpace(string(content))
	} else if cmd.Args().Len() >= 2 {
		// 3. Use provided argument
		value = cmd.Args().Get(1)
	} else {
		// 4. Interactive prompt
		if opts.PromptMsg == "" {
			opts.PromptMsg = fmt.Sprintf("Enter %s value", opts.ResourceName)
		}
		fmt.Printf("%s: ", opts.PromptMsg)

		if opts.Hidden {
			// Hidden input for secrets/passwords
			byteValue, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				return "", fmt.Errorf("failed to read %s value: %w", opts.ResourceName, err)
			}
			fmt.Println() // Add newline after hidden input
			value = string(byteValue)
		} else {
			// Regular visible input - read entire line including spaces
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				return "", fmt.Errorf("failed to read %s value: %w", opts.ResourceName, err)
			}
			value = strings.TrimSpace(input)
		}
	}

	// Validate non-empty if required
	if !opts.AllowEmpty && value == "" {
		return "", fmt.Errorf("%s value cannot be empty", opts.ResourceName)
	}

	return value, nil
}
