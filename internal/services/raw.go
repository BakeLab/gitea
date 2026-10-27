// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"gitea.dev/cli/internal/transport"
)

var errInvalidJSON = errors.New("Gitea returned an invalid JSON response")

// getJSON decodes a raw GET response into out.
func getJSON(ctx context.Context, client *transport.Client, endpoint string, out any) error {
	_, err := rawJSON(ctx, client, http.MethodGet, endpoint, nil, out)
	return err
}

// sendJSON sends payload as a JSON body, or no body when it is nil, and decodes the response
// into out unless out is nil.
func sendJSON(ctx context.Context, client *transport.Client, method, endpoint string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	_, err := rawJSON(ctx, client, method, endpoint, body, out)
	return err
}

func rawJSON(ctx context.Context, client *transport.Client, method, endpoint string, body io.Reader, out any) (http.Header, error) {
	resp, err := client.Do(ctx, method, endpoint, body, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		if _, err = io.Copy(io.Discard, resp.Body); err != nil {
			return nil, transport.ResponseError(ctx, resp, err)
		}
		return resp.Header, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return nil, decodeError(ctx, resp, err)
	}
	return resp.Header, nil
}

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

// AssetDigest returns the SHA-256 the instance recorded for a release asset when it was uploaded,
// or "" when it recorded none, as older instances and older assets do.
// TODO(sdk): Attachment lacks sha256 (https://github.com/go-gitea/gitea/pull/39954).
func (s Releases) AssetDigest(ctx context.Context, release, asset int64) (string, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return "", err
	}
	var attachment struct {
		SHA256 string `json:"sha256"`
	}
	err = getJSON(ctx, s.Client, fmt.Sprintf("repos/%s/%s/releases/%d/assets/%d", url.PathEscape(owner), url.PathEscape(repo), release, asset), &attachment)
	return attachment.SHA256, Classify(err, "release asset", fmt.Sprint(asset), s.Repository)
}
