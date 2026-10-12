// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

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

// rawWindow is listWindow over a raw JSON collection endpoint; it sets limit and page on query.
func rawWindow[T any](ctx context.Context, client *transport.Client, endpoint string, query url.Values, limit, page int) ([]T, bool, error) {
	if query == nil {
		query = url.Values{}
	}
	return listWindow(limit, page, func(limit, page int) ([]T, http.Header, error) {
		query.Set("limit", strconv.Itoa(limit))
		query.Set("page", strconv.Itoa(page))
		var response []T
		headers, err := rawJSON(ctx, client, http.MethodGet, endpoint+"?"+query.Encode(), nil, &response)
		return response, headers, err
	})
}

func rawJSON(ctx context.Context, client *transport.Client, method, endpoint string, body io.Reader, out any) (http.Header, error) {
	resp, err := client.Do(ctx, method, endpoint, body, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, err = io.Copy(io.Discard, resp.Body)
		return resp.Header, err
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errInvalidJSON
	}
	return resp.Header, nil
}
