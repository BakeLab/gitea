// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
)

// errorKinds names the exit codes in the JSON error report.
var errorKinds = map[int]string{
	clierr.Failure: "failure", clierr.Usage: "usage", clierr.Canceled: "canceled",
	clierr.Authentication: "authentication", clierr.Unsuccessful: "unsuccessful",
	clierr.HeadMismatch: "head_mismatch", clierr.Pending: "pending",
}

// errorFormatEnv selects how gt reports failures on stderr.
const errorFormatEnv = "GITEA_CLI_ERROR_FORMAT"

// JSONErrors reports whether failures are to be reported as JSON.
func JSONErrors(deps Dependencies) bool {
	return deps.Getenv != nil && strings.TrimSpace(deps.Getenv(errorFormatEnv)) == "json"
}

// checkErrorFormat refuses a value of GITEA_CLI_ERROR_FORMAT other than text or json.
func (a *application) checkErrorFormat() error {
	switch value := strings.TrimSpace(a.deps.Getenv(errorFormatEnv)); value {
	case "", "text", "json":
		return nil
	default:
		return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid %s %q: the format must be text or json", errorFormatEnv, value))
	}
}

// WriteErrorJSON writes the failure err with its exit code and message to w as one line of JSON:
// {"error":{"code":4,"kind":"authentication","message":"...","status":401}}. status, the HTTP status
// of the Gitea answer, and draft, where a canceled text was saved, appear only when they apply.
func WriteErrorJSON(w io.Writer, message string, err error) error {
	type report struct {
		Code    int    `json:"code"`
		Kind    string `json:"kind"`
		Message string `json:"message"`
		Status  int    `json:"status,omitempty"`
		Draft   string `json:"draft,omitempty"`
	}
	code := clierr.ExitCode(err)
	failure := report{Code: code, Kind: cmp.Or(errorKinds[code], "failure"), Message: message, Draft: DraftPath(err)}
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
		failure.Status = apiErr.Status
	}
	return terminal.JSONOutput{Safe: true}.Write(w, struct {
		Error report `json:"error"`
	}{failure})
}
