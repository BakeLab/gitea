// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// ReleaseAsset is the public metadata of a release attachment.
type ReleaseAsset struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Size          int64  `json:"size"`
	URL           string `json:"url"`
	CreatedAt     string `json:"created_at"`
	DownloadCount int64  `json:"download_count"`
}

// IssueAttachment describes an attachment owned by an issue or pull request.
type IssueAttachment ReleaseAsset

func assetFrom(attachment *gitea.Attachment) ReleaseAsset {
	return ReleaseAsset{
		ID: attachment.ID, Name: attachment.Name, Size: attachment.Size, URL: attachment.DownloadURL,
		CreatedAt: timestamp(attachment.Created), DownloadCount: attachment.DownloadCount,
	}
}

// uploadAttachment streams a multipart upload to an attachment collection endpoint without
// buffering the file in memory.
// TODO(sdk): CreateReleaseAttachment and CreateIssueAttachment buffer the whole file.
func uploadAttachment(ctx context.Context, client *transport.Client, endpoint, name string, input io.Reader) (ReleaseAsset, error) {
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
	response, err := client.Do(ctx, http.MethodPost, endpoint, reader, http.Header{"Content-Type": {multipartWriter.FormDataContentType()}})
	if err != nil {
		return ReleaseAsset{}, err
	}
	defer response.Body.Close()
	var attachment gitea.Attachment
	if err := json.NewDecoder(response.Body).Decode(&attachment); err != nil {
		if ctx.Err() != nil {
			return ReleaseAsset{}, ctx.Err()
		}
		return ReleaseAsset{}, errInvalidJSON
	}
	return assetFrom(&attachment), nil
}

// downloadAttachment opens the content of an attachment read from its owning release or issue,
// following one signed storage redirect without credentials.
func downloadAttachment(ctx context.Context, client *transport.Client, attachment *gitea.Attachment) (io.ReadCloser, error) {
	if attachment.UUID == "" || strings.ContainsAny(attachment.UUID, "/\\.") {
		return nil, errors.New("invalid attachment identifier from Gitea")
	}
	response, err := client.DownloadSigned(ctx, strings.TrimRight(client.BaseURL, "/")+"/attachments/"+url.PathEscape(attachment.UUID))
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}
