// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
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

// apiLong documents gt api like gh api: endpoints, placeholders, fields, pagination and output.
const apiLong = `Make an authenticated request to the Gitea API of the selected instance and print
the response.

The endpoint is a path under /api/v1, such as repos/{owner}/{repo}/issues, or a URL of
the instance's API. The placeholders {owner} and {repo} take the repository of the
current directory or --repo, and {branch} the checked-out Git branch.

The method is GET, or POST when fields or --input supply a request body; -X selects
another. Fields become query parameters of GET and HEAD requests and a JSON body
otherwise:

  -f, --raw-field name=value   a string value
  -F, --field name=value       a typed value: true, false, null, numbers, arrays and
                               objects are JSON, @FILE reads a file and @- stdin

--paginate follows the next-page links of a GET request, or asks for the next page of
a list that Gitea counts in X-Total-Count without links, and prints every page on its
own line. --jq filters each JSON response with a jq expression and prints strings
without quotes; jq does not need to be installed.

On a terminal, JSON responses are indented and colored. A response outside 2xx prints
its body, then fails with the HTTP status.`

const apiExamples = `$ gt api user --jq .login
# Create an issue: -f sends strings, -F typed values
$ gt api repos/{owner}/{repo}/issues -f title="Crash on start" -F labels='[3]'
$ gt api user/repos --paginate --jq '.[].full_name'`

// apiMethods are the HTTP methods the Gitea API serves.
var apiMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}

type apiFlags struct {
	method, input, output              string
	raw, typed, headerValues           []string
	include, verbose, paginate, silent bool
	maxPages                           int
}

func (a *application) apiCommand() *cobra.Command {
	var f apiFlags
	cmd := &cobra.Command{Use: "api <endpoint>", Short: "Make an authenticated Gitea API request", Long: apiLong, Example: apiExamples, Annotations: map[string]string{"gt.text": "api returns raw responses", "gt.rawjson": "true"}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.runAPI(cmd, args, &f)
	}}
	cmd.Flags().StringVarP(&f.method, "method", "X", "GET", "HTTP `method`")
	cmd.Flags().StringVar(&f.input, "input", "", "Read the request body from a `file`, or stdin with -")
	cmd.Flags().StringArrayVarP(&f.headerValues, "header", "H", nil, "Add an HTTP request header in `name:value` format")
	cmd.Flags().StringArrayVarP(&f.raw, "raw-field", "f", nil, "Add a string parameter in `key=value` format")
	cmd.Flags().StringArrayVarP(&f.typed, "field", "F", nil, "Add a typed parameter in `key=value` format; @FILE or @- reads a file or stdin")
	cmd.Flags().StringVar(&f.output, "output-file", "", "Write the complete response to a new `file`")
	cmd.Flags().BoolVarP(&f.include, "include", "i", false, "Print the response status and headers to stderr")
	cmd.Flags().BoolVar(&f.verbose, "verbose", false, "Print the request and the response status and headers to stderr")
	cmd.Flags().BoolVar(&f.silent, "silent", false, "Do not print the response body")
	cmd.Flags().BoolVar(&f.paginate, "paginate", false, "Follow next-page links and print every page")
	cmd.Flags().IntVar(&f.maxPages, "max-pages", 1000, "Stop --paginate after this many pages; reaching the default fails")
	return cmd
}

func (a *application) runAPI(cmd *cobra.Command, args []string, f *apiFlags) error {
	usage := func(err error) error { return clierr.Wrap(clierr.Usage, err) }
	if f.silent && a.options.jq != "" {
		return usage(errors.New("--silent and --jq are mutually exclusive"))
	}
	headers, err := apiHeaders(f.headerValues)
	if err != nil {
		return usage(err)
	}
	fields, err := apiFields(f.raw, f.typed)
	if err != nil {
		return usage(err)
	}
	if cmd.Flags().Changed("input") && f.input == "" {
		return usage(errors.New("--input requires a file path or -"))
	}
	if f.input != "" && len(fields) > 0 {
		return usage(errors.New("--input conflicts with API fields"))
	}
	if !cmd.Flags().Changed("method") && (f.input != "" || len(fields) > 0) {
		f.method = http.MethodPost
	}
	f.method = strings.ToUpper(f.method)
	if !slices.Contains(apiMethods, f.method) {
		return usage(fmt.Errorf("invalid HTTP method %q; use one of %s", f.method, strings.Join(apiMethods, ", ")))
	}
	if f.paginate && (f.method != http.MethodGet || f.input != "") {
		return usage(errors.New("--paginate requires a body-free GET request"))
	}
	if f.maxPages < 1 {
		return usage(errors.New("--max-pages must be positive"))
	}
	if f.output != "" {
		if err := validateDownloadDestination(f.output); err != nil {
			return err
		}
	}
	if err := apiFieldFiles(fields, f.typed, a.deps.Streams.In); err != nil {
		return usage(err)
	}
	var body io.Reader
	if f.input == "-" {
		body = a.deps.Streams.In
	} else if f.input != "" {
		file, err := os.Open(f.input)
		if err != nil {
			return err
		}
		defer file.Close()
		body = inputBody(file)
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
		owner, name := target.ownerName()
		endpoint = strings.NewReplacer("{owner}", url.PathEscape(owner), "{repo}", url.PathEscape(name)).Replace(endpoint)
	}
	// An endpoint outside the instance's API, such as a URL of another instance, is a usage error.
	if _, err := target.client.URL(endpoint); err != nil {
		return usage(err)
	}
	if f.verbose {
		// The request lists the headers it sends; say which configured ones it leaves out.
		for _, name := range a.unsentHeaders(target.host) {
			a.warn(cmd, "%s", a.unsentHeader(target.host, name))
		}
	}
	if len(fields) > 0 {
		if f.method == http.MethodGet || f.method == http.MethodHead {
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
	return writeOutput(cmd, f.output, func(destination io.Writer) error {
		response := apiResponse{paginate: f.paginate, query: a.options.jq}
		if f.output == "" || f.output == "-" {
			response.tty = a.deps.Streams.OutTTY
			response.color = response.tty && a.colorEnabled()
		}
		err = (services.API{Client: target.client}).Visit(cmd.Context(), services.APIOptions{Endpoint: endpoint, Method: f.method, Body: body, Headers: headers, Paginate: f.paginate, MaxPages: f.maxPages}, func(r *http.Response) error {
			if f.verbose {
				if err := writeRequestHead(cmd.ErrOrStderr(), r.Request); err != nil {
					return err
				}
			}
			if f.include || f.verbose {
				if err := writeResponseHead(cmd.ErrOrStderr(), r, f.verbose); err != nil {
					return err
				}
			}
			if f.silent {
				_, err := io.Copy(io.Discard, r.Body)
				return err
			}
			return response.write(destination, r)
		})
		if errors.Is(err, services.ErrMaxPages) {
			if !cmd.Flags().Changed("max-pages") {
				// The default limit only guards against a server that never ends, so its pages are not every page.
				return fmt.Errorf("--paginate stopped after %s and more pages exist; raise --max-pages", terminal.Plural(f.maxPages, "page"))
			}
			// --max-pages asks to stop, so the pages it allows are a complete answer.
			a.warn(cmd, "Stopped after %s as --max-pages asks; more pages exist", terminal.Plural(f.maxPages, "page"))
			return nil
		}
		return err
	})
}

// inputBody returns the request body of an --input file. A regular file can be read again from the
// start, so that signing it for an SSH key account hashes it as it is read instead of holding it in
// memory.
func inputBody(file *os.File) io.Reader {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return file
	}
	return transport.Replayable(func() io.Reader { return io.NewSectionReader(file, 0, info.Size()) })
}

// apiResponse writes response bodies for gt api: filtered by --jq, indented and colored JSON on a
// terminal, otherwise as received (minus terminal controls on a terminal), ending with a newline.
type apiResponse struct {
	tty, color, paginate bool
	query                string
}

func (o apiResponse) write(w io.Writer, r *http.Response) error {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	isJSON := mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
	succeeded := r.StatusCode >= 200 && r.StatusCode < 300
	if (o.query != "" && succeeded) || (o.tty && isJSON) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		if o.query != "" && succeeded {
			if len(data) == 0 {
				return nil
			}
			if !json.Valid(data) {
				return fmt.Errorf("--jq needs a JSON response, but the response is %s", cmp.Or(mediaType, "not JSON"))
			}
			return terminal.JSONOutput{Query: o.query, Pretty: o.tty, Color: o.color}.Write(w, json.RawMessage(data))
		}
		if pretty, err := terminal.IndentJSON(data, o.color); err == nil {
			_, err = io.WriteString(w, pretty)
			return err
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
	}
	last := &lastByte{w: terminal.SafeWriter(w, o.tty, false)}
	if _, err := io.Copy(last, r.Body); err != nil {
		return err
	}
	if last.written && last.b != '\n' && (o.tty || isJSON || o.paginate) {
		_, err := io.WriteString(w, "\n")
		return err
	}
	return nil
}

// lastByte remembers the last byte written through it.
type lastByte struct {
	w       io.Writer
	b       byte
	written bool
}

func (l *lastByte) Write(p []byte) (int, error) {
	if len(p) > 0 {
		l.b, l.written = p[len(p)-1], true
	}
	return l.w.Write(p)
}

// writeResponseHead prints the status line and the sorted headers of r, then a blank line; verbose
// marks each line with "< " like curl. URLs in headers, such as a redirect to signed storage, are
// printed with their credentials and signatures redacted.
func writeResponseHead(w io.Writer, r *http.Response, verbose bool) error {
	prefix := ""
	if verbose {
		prefix = "< "
	}
	headers := http.Header{}
	for name, values := range r.Header {
		for _, value := range values {
			headers.Add(name, transport.RedactHeader(name, value))
		}
	}
	return writeHead(w, prefix, r.Proto+" "+r.Status, headers)
}

// writeRequestHead prints the method, URL without query and headers of r marked with "> ", hiding
// credentials and the values of the instance's configured headers.
func writeRequestHead(w io.Writer, r *http.Request) error {
	headers := r.Header.Clone()
	for _, name := range append([]string{"Authorization", "Proxy-Authorization", "Cookie", "Signature", "Sudo"}, transport.ConfiguredHeaders(r)...) {
		if headers.Get(name) != "" {
			headers.Set(name, "(hidden)")
		}
	}
	return writeHead(w, "> ", r.Method+" "+r.URL.Scheme+"://"+r.URL.Host+r.URL.EscapedPath(), headers)
}

func writeHead(w io.Writer, prefix, first string, headers http.Header) error {
	var b strings.Builder
	b.WriteString(prefix + terminal.SafeLine(first) + "\n")
	for _, key := range slices.Sorted(maps.Keys(headers)) {
		for _, value := range headers.Values(key) {
			b.WriteString(prefix + terminal.SafeLine(key+": "+value) + "\n")
		}
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}
