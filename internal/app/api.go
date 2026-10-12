// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func apiFields(raw, typed []string) (map[string]any, error) {
	fields := map[string]any{}
	for kind, arguments := range [][]string{raw, typed} {
		for _, argument := range arguments {
			key, value, ok := strings.Cut(argument, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return nil, errors.New("API fields require name=value")
			}
			if _, exists := fields[key]; exists {
				return nil, errors.New("API field names must be unique")
			}
			var parsed any = value
			if kind == 1 {
				decoder := json.NewDecoder(strings.NewReader(value))
				decoder.UseNumber()
				var candidate any
				if err := decoder.Decode(&candidate); err == nil {
					var extra any
					if decoder.Decode(&extra) == io.EOF {
						parsed = candidate
					}
				}
			}
			fields[key] = parsed
		}
	}
	return fields, nil
}

func apiFieldFiles(fields map[string]any, typed []string, stdin io.Reader) error {
	consumers := 0
	for _, argument := range typed {
		_, value, _ := strings.Cut(argument, "=")
		if value == "@" {
			return errors.New("file-backed API fields require @PATH or @-")
		}
		if value == "@-" {
			consumers++
		}
	}
	if consumers > 1 {
		return errors.New("only one API field may read stdin")
	}
	for _, argument := range typed {
		key, value, _ := strings.Cut(argument, "=")
		if !strings.HasPrefix(value, "@") {
			continue
		}
		filename := strings.TrimPrefix(value, "@")
		if filename == "" {
			return errors.New("file-backed API fields require @PATH or @-")
		}
		data, err := readFileOrStdin(stdin, filename)
		if err != nil {
			return err
		}
		fields[key] = string(data)
	}
	return nil
}

func apiHeaders(values []string) (http.Header, error) {
	headers := http.Header{}
	for _, argument := range values {
		name, value, ok := strings.Cut(argument, ":")
		if !ok || name == "" {
			return nil, errors.New("API headers require Name: value")
		}
		for _, char := range name {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", char)) {
				return nil, errors.New("invalid API header name")
			}
		}
		for _, char := range value {
			if (char < 32 && char != '\t') || char == 127 {
				return nil, errors.New("invalid API header value")
			}
		}
		switch strings.ToLower(name) {
		case "authorization", "proxy-authorization", "host", "content-length", "transfer-encoding", "connection", "upgrade", "trailer", "te":
			return nil, errors.New("API headers cannot override authentication, host, or transport framing")
		}
		headers.Add(name, strings.TrimSpace(value))
	}
	return headers, nil
}

func (a *application) apiCommand() *cobra.Command {
	var method, input, output string
	var raw, typed, headerValues []string
	var include, paginate bool
	var maxPages int
	cmd := &cobra.Command{Use: "api <endpoint>", Short: "Make an authenticated Gitea API request", Annotations: textOutput("api returns raw responses"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		usage := func(err error) error { return clierr.Wrap(clierr.Usage, err) }
		headers, err := apiHeaders(headerValues)
		if err != nil {
			return usage(err)
		}
		fields, err := apiFields(raw, typed)
		if err != nil {
			return usage(err)
		}
		if cmd.Flags().Changed("input") && input == "" {
			return usage(errors.New("--input requires a file path or -"))
		}
		if input != "" && len(fields) > 0 {
			return usage(errors.New("--input conflicts with API fields"))
		}
		if !cmd.Flags().Changed("method") && (input != "" || len(fields) > 0) {
			method = http.MethodPost
		}
		method = strings.ToUpper(method)
		if _, err := http.NewRequest(method, "http://localhost", nil); err != nil || method == "" {
			return usage(errors.New("invalid HTTP method"))
		}
		if paginate && (method != http.MethodGet || input != "") {
			return usage(errors.New("--paginate requires a body-free GET request"))
		}
		if maxPages < 1 {
			return usage(errors.New("--max-pages must be positive"))
		}
		if output != "" {
			if err := validateDownloadDestination(output); err != nil {
				return err
			}
		}
		if err := apiFieldFiles(fields, typed, a.deps.Streams.In); err != nil {
			return usage(err)
		}
		var body io.Reader
		if input == "-" {
			body = a.deps.Streams.In
		} else if input != "" {
			file, err := os.Open(input)
			if err != nil {
				return err
			}
			defer file.Close()
			body = file
		}
		endpoint := args[0]
		if strings.ContainsAny(strings.NewReplacer("{owner}", "owner", "{repo}", "repo", "{branch}", "branch").Replace(endpoint), "{}") {
			return usage(errors.New("unsupported API placeholder; use {owner}, {repo}, and {branch}"))
		}
		if strings.Contains(endpoint, "{branch}") {
			branch, err := a.currentBranch(cmd.Context())
			if err != nil || branch == "" {
				return usage(errors.New("{branch} requires a checked-out Git branch"))
			}
			endpoint = strings.ReplaceAll(endpoint, "{branch}", url.PathEscape(branch))
		}
		requireRepo := strings.Contains(endpoint, "{owner}") || strings.Contains(endpoint, "{repo}")
		target, err := a.resolve(cmd.Context(), requireRepo)
		if err != nil {
			return err
		}
		if requireRepo {
			pieces := strings.SplitN(target.Repo, "/", 2)
			endpoint = strings.NewReplacer("{owner}", url.PathEscape(pieces[0]), "{repo}", url.PathEscape(pieces[1])).Replace(endpoint)
		}
		if len(fields) > 0 {
			if method == http.MethodGet || method == http.MethodHead {
				parsed, err := url.Parse(endpoint)
				if err != nil {
					return usage(errors.New("invalid API endpoint"))
				}
				query := parsed.Query()
				for key, value := range fields {
					if text, ok := value.(string); ok {
						query.Set(key, text)
					} else {
						data, err := json.Marshal(value)
						if err != nil {
							return err
						}
						query.Set(key, string(data))
					}
				}
				parsed.RawQuery = query.Encode()
				endpoint = parsed.String()
			} else {
				data, err := json.Marshal(fields)
				if err != nil {
					return err
				}
				body = bytes.NewReader(data)
			}
		}
		return writeOutput(cmd, output, func(destination io.Writer) error {
			return (services.API{Client: target.client}).Visit(cmd.Context(), services.APIOptions{Endpoint: endpoint, Method: method, Body: body, Headers: headers, Paginate: paginate, MaxPages: maxPages}, func(response *http.Response) error {
				if include {
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", response.Proto, response.Status); err != nil {
						return err
					}
					keys := make([]string, 0, len(response.Header))
					for key := range response.Header {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					for _, key := range keys {
						for _, value := range response.Header.Values(key) {
							if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", key, value); err != nil {
								return err
							}
						}
					}
					if _, err := fmt.Fprintln(cmd.ErrOrStderr()); err != nil {
						return err
					}
				}
				if _, err := io.Copy(destination, response.Body); err != nil {
					return err
				}
				if paginate {
					_, err := fmt.Fprintln(destination)
					return err
				}
				return nil
			})
		})
	}}
	cmd.Flags().StringVarP(&method, "method", "X", "GET", "HTTP method")
	cmd.Flags().StringVar(&input, "input", "", "Read request body from a file or stdin (-)")
	cmd.Flags().StringArrayVarP(&headerValues, "header", "H", nil, "Request header: Name: value")
	cmd.Flags().StringArrayVarP(&raw, "raw-field", "f", nil, "String request field: name=value")
	cmd.Flags().StringArrayVarP(&typed, "field", "F", nil, "JSON request field: name=value; @PATH or @- reads file text")
	cmd.Flags().StringVar(&output, "output-file", "", "Write the complete response to a new file")
	cmd.Flags().BoolVar(&include, "include", false, "Print response status and headers to stderr")
	cmd.Flags().BoolVar(&paginate, "paginate", false, "Follow scoped next-page links; separate raw pages with newlines")
	cmd.Flags().IntVar(&maxPages, "max-pages", 1000, "Maximum pages with --paginate")
	return cmd
}
