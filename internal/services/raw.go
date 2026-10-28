// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"gitea.dev/cli/internal/transport"
)

var errInvalidJSON = errors.New("Gitea returned an invalid JSON response")

// decodeError classifies a failure to decode the JSON body of resp: a body that is not JSON of
// the expected shape is invalid, and one that could not be read, such as from a connection that
// stalled or dropped, is a request failure.
func decodeError(ctx context.Context, resp *http.Response, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) || err == io.EOF {
		return errInvalidJSON
	}
	return transport.ResponseError(ctx, resp, err)
}
