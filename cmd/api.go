// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"bytes"
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

// apiFlags returns a fresh set of flag instances for the api command.
// This is a factory function so that each invocation gets independent flag
// objects, avoiding shared hasBeenSet state across tests.
func apiFlags() []cli.Flag {
	return []cli.Flag{
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
		&cli.StringFlag{
			Name:    "data",
			Aliases: []string{"d"},
			Usage:   "Raw JSON request body (use @file to read from file, @- for stdin)",
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
	}
}

// CmdApi represents the api command
var CmdApi = cli.Command{
	Name:                      "api",
	Category:                  catHelpers,
	DisableSliceFlagSeparator: true,
	Usage:                     "Make an authenticated API request",
	Description: `Makes an authenticated HTTP request to the Gitea API and prints the response.

The endpoint argument is the path to the API endpoint, which will be prefixed
with /api/v1/ if it doesn't start with /api/ or http(s)://.

Placeholders like {owner} and {repo} in the endpoint will be replaced with
values from the current repository context.

Use -f for string fields and -F for typed fields (numbers, booleans, null).
With -F, prefix value with @ to read from file (@- for stdin). Values starting
with [ or { are parsed as JSON arrays/objects. Wrap values in quotes to force
string type (e.g., -F key="null" for literal string "null").

Use -d/--data to send a raw JSON body. Use @file to read from a file, or @-
to read from stdin. The -d flag cannot be combined with -f or -F.

When a request body is provided via -f, -F, or -d, the method defaults to POST
unless explicitly set with -X/--method.

Note: if your endpoint contains ? or &, quote it to prevent shell expansion
(e.g., '/repos/{owner}/{repo}/issues?state=open').`,
	ArgsUsage: "<endpoint>",
	Action:    runApi,
	Flags:     append(apiFlags(), flags.LoginRepoFlags...),
}

type preparedAPIRequest struct {
	Method   string
	Endpoint string
	Headers  map[string]string
	Body     []byte
}

func runApi(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	request, err := prepareAPIRequest(cmd, ctx)
	if err != nil {
		return err
	}

	var body io.Reader
	if request.Body != nil {
		body = bytes.NewReader(request.Body)
	}

	// Create API client and make request
	client := api.NewClient(ctx.Login)
	resp, err := client.Do(request.Method, request.Endpoint, body, request.Headers)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close response body: %v\n", closeErr)
		}
	}()

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
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to close output file: %v\n", closeErr)
			}
		}()
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

func prepareAPIRequest(cmd *cli.Command, ctx *context.TeaContext) (*preparedAPIRequest, error) {
	var err error

	// Get the endpoint argument
	if cmd.NArg() < 1 {
		return nil, fmt.Errorf("endpoint argument required")
	}
	endpoint := cmd.Args().First()

	// Expand placeholders in endpoint
	endpoint = expandPlaceholders(endpoint, ctx)

	// Parse headers
	headers := make(map[string]string)
	for _, h := range cmd.StringSlice("header") {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header format: %q (expected key:value)", h)
		}
		headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	// Build request body from fields
	var bodyBytes []byte
	stringFields := cmd.StringSlice("field")
	typedFields := cmd.StringSlice("Field")
	dataRaw := cmd.String("data")

	if dataRaw != "" && (len(stringFields) > 0 || len(typedFields) > 0) {
		return nil, fmt.Errorf("--data/-d cannot be combined with --field/-f or --Field/-F")
	}

	if dataRaw != "" {
		var dataBytes []byte
		var dataSource string
		if strings.HasPrefix(dataRaw, "@") {
			filename := dataRaw[1:]
			if filename == "-" {
				dataBytes, err = io.ReadAll(os.Stdin)
				dataSource = "stdin"
			} else {
				dataBytes, err = os.ReadFile(filename)
				dataSource = filename
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read %q: %w", dataRaw, err)
			}
		} else {
			dataBytes = []byte(dataRaw)
		}
		if !json.Valid(dataBytes) {
			if dataSource != "" {
				return nil, fmt.Errorf("--data/-d value from %s is not valid JSON", dataSource)
			}
			return nil, fmt.Errorf("--data/-d value is not valid JSON")
		}
		bodyBytes = dataBytes
	} else if len(stringFields) > 0 || len(typedFields) > 0 {
		bodyMap := make(map[string]any)

		// Process string fields (-f)
		for _, f := range stringFields {
			parts := strings.SplitN(f, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid field format: %q (expected key=value)", f)
			}
			key := parts[0]
			if key == "" {
				return nil, fmt.Errorf("field key cannot be empty in %q", f)
			}
			if _, exists := bodyMap[key]; exists {
				return nil, fmt.Errorf("duplicate field key %q", key)
			}
			bodyMap[key] = parts[1]
		}

		// Process typed fields (-F)
		for _, f := range typedFields {
			parts := strings.SplitN(f, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid field format: %q (expected key=value)", f)
			}
			key := parts[0]
			if key == "" {
				return nil, fmt.Errorf("field key cannot be empty in %q", f)
			}
			if _, exists := bodyMap[key]; exists {
				return nil, fmt.Errorf("duplicate field key %q", key)
			}
			value := parts[1]

			parsedValue, err := parseTypedValue(value)
			if err != nil {
				return nil, fmt.Errorf("failed to parse field %q: %w", key, err)
			}
			bodyMap[key] = parsedValue
		}

		bodyBytes, err = json.Marshal(bodyMap)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request body: %w", err)
		}
	}
	method := strings.ToUpper(cmd.String("method"))
	if !cmd.IsSet("method") {
		if bodyBytes != nil {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	return &preparedAPIRequest{
		Method:   method,
		Endpoint: endpoint,
		Headers:  headers,
		Body:     bodyBytes,
	}, nil
}

// parseTypedValue parses a value for -F flag, handling:
// - @filename: read content from file
// - @-: read content from stdin
// - "quoted": literal string (prevents type parsing)
// - true/false: boolean
// - null: nil
// - numbers: int or float
// - []/{}:  JSON arrays/objects
// - otherwise: string
func parseTypedValue(value string) (any, error) {
	// Handle file references.
	// Note: if multiple fields use @- (stdin), only the first will get data;
	// subsequent reads will return empty since stdin is consumed once.
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

	// Handle quoted strings (literal strings, no type parsing).
	// Uses strconv.Unquote so escape sequences like \" are handled correctly.
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return nil, fmt.Errorf("invalid quoted string %s: %w", value, err)
		}
		return unquoted, nil
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

	// Handle JSON arrays and objects
	if len(value) > 0 && (value[0] == '[' || value[0] == '{') {
		var jsonVal any
		if err := json.Unmarshal([]byte(value), &jsonVal); err == nil {
			return jsonVal, nil
		}
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
