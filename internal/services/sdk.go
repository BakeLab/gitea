// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// sdkClient binds the SDK to the instance's API transport, which owns authentication, retries,
// redirects, and error capture. The empty version disables the SDK's version probe and gates.
func sdkClient(client *transport.Client) (*gitea.Client, error) {
	return gitea.NewClient(client.BaseURL, gitea.SetHTTPClient(client.API()), gitea.SetGiteaVersion(""))
}

// call runs one SDK operation and maps a failure onto the errors raw requests report: the
// context error, a sanitized *transport.APIError, or a network error without the request URL.
func call[T any](ctx context.Context, client *transport.Client, op func(context.Context, *gitea.Client) (T, *gitea.Response, error)) (T, *gitea.Response, error) {
	var zero T
	sdk, err := sdkClient(client)
	if err != nil {
		return zero, nil, err
	}
	ctx, captured := transport.CaptureErrors(ctx)
	result, resp, err := op(ctx, sdk)
	if err = sdkError(ctx, resp, err, captured); err != nil {
		return zero, resp, err
	}
	return result, resp, nil
}

// do is call for SDK operations that return no value.
func do(ctx context.Context, client *transport.Client, op func(context.Context, *gitea.Client) (*gitea.Response, error)) error {
	_, _, err := call(ctx, client, func(ctx context.Context, sdk *gitea.Client) (struct{}, *gitea.Response, error) {
		resp, err := op(ctx, sdk)
		return struct{}{}, resp, err
	})
	return err
}

func sdkError(ctx context.Context, resp *gitea.Response, err error, captured func() *transport.APIError) error {
	failed := resp != nil && resp.Response != nil && (resp.StatusCode < 200 || resp.StatusCode >= 300)
	if err == nil && !failed {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if apiErr := captured(); apiErr != nil {
		return apiErr
	}
	if failed {
		return &transport.APIError{Status: resp.StatusCode, Method: resp.Request.Method, Endpoint: resp.Request.URL.Path}
	}
	var urlErr *url.Error
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &urlErr):
		return transport.RequestError(ctx, "API request failed", err)
	case errors.As(err, &syntaxErr), errors.As(err, &typeErr):
		return errInvalidJSON
	}
	return err
}

// repoOperation is an SDK operation on one repository.
type repoOperation[T any] func(ctx context.Context, c *gitea.Client, owner, repo string) (T, *gitea.Response, error)

// repoCall is call for an operation on an owner/name repository.
func repoCall[T any](ctx context.Context, client *transport.Client, repository string, op repoOperation[T]) (T, error) {
	owner, repo, err := splitRepository(repository)
	if err != nil {
		var zero T
		return zero, err
	}
	result, _, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (T, *gitea.Response, error) {
		return op(ctx, c, owner, repo)
	})
	return result, err
}

// repoDo is repoCall for operations that return no value.
func repoDo(ctx context.Context, client *transport.Client, repository string, op func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error)) error {
	_, err := repoCall(ctx, client, repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (struct{}, *gitea.Response, error) {
		resp, err := op(ctx, c, owner, repo)
		return struct{}{}, resp, err
	})
	return err
}

// repoConvert is repoCall for an operation whose result maps onto a tea value.
func repoConvert[S, T any](ctx context.Context, client *transport.Client, repository string, convert func(*S) T, op repoOperation[*S]) (T, error) {
	item, err := repoCall(ctx, client, repository, op)
	if err != nil {
		var zero T
		return zero, err
	}
	return convert(item), nil
}

// splitRepository separates an owner/name repository reference for SDK calls.
func splitRepository(repository string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("invalid repository %q; use owner/name", repository)
	}
	return owner, name, nil
}

// listOptions always sets the page size, so the server never applies its own default.
func listOptions(limit, page int) gitea.ListOptions {
	return gitea.ListOptions{Page: page, PageSize: limit}
}

// headerOf returns the response headers that carry pagination hints, or none without a response.
func headerOf(resp *gitea.Response) http.Header {
	if resp == nil || resp.Response == nil {
		return http.Header{}
	}
	return resp.Header
}

// deref returns the value behind an optional field, or its zero value when the field is absent.
func deref[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}

// timestamp renders an SDK time as tea's RFC 3339 string, or "" when the server sent none.
func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

// optionalTimestamp is timestamp for nullable fields, returning nil when the time is absent.
func optionalTimestamp(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	value := timestamp(*t)
	return &value
}

// login returns the user's login, or "" for an absent user.
func login(user *gitea.User) string {
	if user == nil {
		return ""
	}
	return user.UserName
}
