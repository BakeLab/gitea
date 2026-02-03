// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/api"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// CmdApi represents the api command
var CmdApi = cli.Command{
	Name:  "api",
	Usage: "Make an authenticated API request",
	Description: `Makes an authenticated HTTP request to the Gitea API and prints the response.

The endpoint argument is the path to the API endpoint, which will be prefixed
with /api/v1/ if it doesn't start with /api/ or http(s)://.

Placeholders like {owner} and {repo} in the endpoint will be replaced with
values from the current repository context.

Use -f for string fields and -F for typed fields (numbers, booleans, null).
With -F, prefix value with @ to read from file (@- for stdin).`,
	ArgsUsage: "<endpoint>",
	Action:    runApi,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:    "method",
			Aliases: []string{"X"},
			Usage:   "HTTP method (GET, POST, PUT, PATCH, DELETE)",
			Value:   "GET",
		},
		&cli.StringSliceFlag{
			Name:    "field",
			Aliases: []string{"f"},
			Usage:   "Add a string field to the request body (key=value)",
		},
		&cli.StringSliceFlag{
			Name:    "Field",
			Aliases: []string{"F"},
			Usage:   "Add a typed field to the request body (key=value, @file, or @- for stdin)",
		},
		&cli.StringSliceFlag{
			Name:    "header",
			Aliases: []string{"H"},
			Usage:   "Add a custom header (key:value)",
		},
		&cli.BoolFlag{
			Name:    "include",
			Aliases: []string{"i"},
			Usage:   "Include HTTP status and response headers in output (written to stderr)",
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "Write response body to file instead of stdout (use '-' for stdout)",
		},
	}, flags.LoginRepoFlags...),
}

func runApi(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)

	// Get the endpoint argument
	if cmd.NArg() < 1 {
		return fmt.Errorf("endpoint argument required")
	}
	endpoint := cmd.Args().First()

	// Expand placeholders in endpoint
	endpoint = expandPlaceholders(endpoint, ctx)

	// Parse headers
	headers := make(map[string]string)
	for _, h := range cmd.StringSlice("header") {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header format: %q (expected key:value)", h)
		}
		headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	// Build request body from fields
	var body io.Reader
	stringFields := cmd.StringSlice("field")
	typedFields := cmd.StringSlice("Field")

	if len(stringFields) > 0 || len(typedFields) > 0 {
		bodyMap := make(map[string]any)

		// Process string fields (-f)
		for _, f := range stringFields {
			parts := strings.SplitN(f, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid field format: %q (expected key=value)", f)
			}
			bodyMap[parts[0]] = parts[1]
		}

		// Process typed fields (-F)
		for _, f := range typedFields {
			parts := strings.SplitN(f, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid field format: %q (expected key=value)", f)
			}
			key := parts[0]
			value := parts[1]

			parsedValue, err := parseTypedValue(value)
			if err != nil {
				return fmt.Errorf("failed to parse field %q: %w", key, err)
			}
			bodyMap[key] = parsedValue
		}

		bodyBytes, err := json.Marshal(bodyMap)
		if err != nil {
			return fmt.Errorf("failed to encode request body: %w", err)
		}
		body = strings.NewReader(string(bodyBytes))
	}

	// Create API client and make request
	client := api.NewClient(ctx.Login)
	method := strings.ToUpper(cmd.String("method"))

	resp, err := client.Do(method, endpoint, body, headers)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Print headers to stderr if requested (so redirects/pipes work correctly)
	if cmd.Bool("include") {
		fmt.Fprintf(os.Stderr, "%s %s\n", resp.Proto, resp.Status)
		for key, values := range resp.Header {
			for _, value := range values {
				fmt.Fprintf(os.Stderr, "%s: %s\n", key, value)
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	// Determine output destination
	outputPath := cmd.String("output")
	forceStdout := outputPath == "-"
	outputToStdout := outputPath == "" || forceStdout

	// Check for binary output to terminal (skip warning if user explicitly forced stdout)
	if outputToStdout && !forceStdout && term.IsTerminal(int(os.Stdout.Fd())) && !isTextContentType(resp.Header.Get("Content-Type")) {
		fmt.Fprintln(os.Stderr, "Warning: Binary output detected. Use '-o <file>' to save to a file,")
		fmt.Fprintln(os.Stderr, "or '-o -' to force output to terminal.")
		return nil
	}

	var output io.Writer = os.Stdout
	if !outputToStdout {
		file, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer file.Close()
		output = file
	}

	// Copy response body to output
	_, err = io.Copy(output, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Add newline for better terminal display
	if outputToStdout && term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println()
	}

	return nil
}

// parseTypedValue parses a value for -F flag, handling:
// - @filename: read content from file
// - @-: read content from stdin
// - true/false: boolean
// - null: nil
// - numbers: int or float
// - otherwise: string
func parseTypedValue(value string) (any, error) {
	// Handle file references
	if strings.HasPrefix(value, "@") {
		filename := value[1:]
		var content []byte
		var err error

		if filename == "-" {
			content, err = io.ReadAll(os.Stdin)
		} else {
			content, err = os.ReadFile(filename)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read %q: %w", value, err)
		}
		return strings.TrimSuffix(string(content), "\n"), nil
	}

	// Handle null
	if value == "null" {
		return nil, nil
	}

	// Handle booleans
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}

	// Handle integers
	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i, nil
	}

	// Handle floats
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f, nil
	}

	// Default to string
	return value, nil
}

// isTextContentType returns true if the content type indicates text data
func isTextContentType(contentType string) bool {
	if contentType == "" {
		return true // assume text if unknown
	}
	contentType = strings.ToLower(strings.Split(contentType, ";")[0]) // strip charset

	return strings.HasPrefix(contentType, "text/") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "toml")
}

// expandPlaceholders replaces {owner}, {repo}, and {branch} in the endpoint
func expandPlaceholders(endpoint string, ctx *context.TeaContext) string {
	endpoint = strings.ReplaceAll(endpoint, "{owner}", ctx.Owner)
	endpoint = strings.ReplaceAll(endpoint, "{repo}", ctx.Repo)

	// Get current branch if available
	if ctx.LocalRepo != nil {
		if branch, err := ctx.LocalRepo.Head(); err == nil {
			branchName := branch.Name().Short()
			endpoint = strings.ReplaceAll(endpoint, "{branch}", branchName)
		}
	}

	return endpoint
}
