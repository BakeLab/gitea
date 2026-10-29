// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
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

// uploadAttachment streams a multipart upload to an attachment collection endpoint without
// buffering the file in memory.
// TODO(sdk): the attachment uploads stream through a pipe, which HTTP signing has to buffer.
func uploadAttachment(ctx context.Context, client *transport.Client, endpoint, name string, input io.Reader) (ReleaseAsset, error) {
	body, contentType, err := fileUpload(name, input)
	if err != nil {
		return ReleaseAsset{}, err
	}
	if body == nil {
		reader, writer := io.Pipe()
		multipartWriter := multipart.NewWriter(writer)
		defer reader.Close()
		go func() {
			part, err := multipartWriter.CreateFormFile("attachment", name)
			if err == nil {
				_, err = io.Copy(part, input)
			}
			if err == nil {
				err = multipartWriter.Close()
			}
			_ = writer.CloseWithError(err)
		}()
		body, contentType = reader, multipartWriter.FormDataContentType()
	}
	response, err := client.Do(ctx, http.MethodPost, endpoint, body, http.Header{"Content-Type": {contentType}})
	if err != nil {
		return ReleaseAsset{}, err
	}
	defer response.Body.Close()
	var attachment gitea.Attachment
	if err := json.NewDecoder(response.Body).Decode(&attachment); err != nil {
		return ReleaseAsset{}, decodeError(ctx, response, err)
	}
	return assetFrom(&attachment), nil
}

// rebaseBranch rebases the head branch of pull request n onto its base branch.
// TODO(sdk): PullRequestsService.UpdatePullRequest has no option for style=rebase.
func (s Pulls) rebaseBranch(ctx context.Context, n int64) error {
	owner, name, err := splitRepository(s.Repository)
	if err != nil {
		return err
	}
	resp, err := s.Client.Do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/%s/pulls/%d/update?style=rebase", url.PathEscape(owner), url.PathEscape(name), n), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return transport.ResponseError(ctx, resp, err)
	}
	return nil
}
