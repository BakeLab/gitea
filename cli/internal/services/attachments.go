// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
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
		ID: attachment.ID, Name: attachment.Name, Size: attachment.Size, URL: assetURL(attachment),
		CreatedAt: timestamp(attachment.Created), DownloadCount: attachment.DownloadCount,
	}
}

// assetURL returns the URL an attachment is downloaded from at its instance,
// .../attachments/<uuid>. Gitea lists the assets of a release with the URL
// .../owner/repo/releases/download/<tag>/<name>, which breaks once the tag or the asset is
// renamed, but answers every other endpoint, such as an upload, with the attachment URL, so that
// one is used throughout. Other URLs, such as of an asset stored elsewhere, are kept.
func assetURL(attachment *gitea.Attachment) string {
	index := strings.LastIndex(attachment.DownloadURL, "/releases/download/")
	if index < 0 || attachment.UUID == "" {
		return attachment.DownloadURL
	}
	// Drop owner/repo to keep the instance URL.
	instance := attachment.DownloadURL[:index]
	for range 2 {
		slash := strings.LastIndex(instance, "/")
		if slash < 0 {
			return attachment.DownloadURL
		}
		instance = instance[:slash]
	}
	if !strings.Contains(instance, "://") {
		return attachment.DownloadURL
	}
	return instance + "/attachments/" + url.PathEscape(attachment.UUID)
}

// fileUpload returns the multipart upload of input when it is a regular file, read in place from
// its current offset and readable again from the start, so that signing it for HTTPSign hashes
// the file in a first pass instead of buffering it, and a nil body for any other input.
func fileUpload(name string, input io.Reader) (io.Reader, string, error) {
	file, ok := input.(*os.File)
	if !ok {
		return nil, "", nil
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", nil
	}
	start, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, "", nil
	}
	var buffer bytes.Buffer
	multipartWriter := multipart.NewWriter(&buffer)
	if _, err := multipartWriter.CreateFormFile("attachment", name); err != nil {
		return nil, "", err
	}
	head := bytes.Clone(buffer.Bytes())
	buffer.Reset()
	if err := multipartWriter.Close(); err != nil {
		return nil, "", err
	}
	tail, size := buffer.Bytes(), info.Size()-start
	return transport.Replayable(func() io.Reader {
		return io.MultiReader(bytes.NewReader(head), io.NewSectionReader(file, start, size), bytes.NewReader(tail))
	}), multipartWriter.FormDataContentType(), nil
}

// downloadAttachment opens the content of an attachment read from its owning release or issue,
// following one signed storage redirect without credentials.
func downloadAttachment(ctx context.Context, client *transport.Client, attachment *gitea.Attachment) (io.ReadCloser, error) {
	if attachment.UUID == "" || strings.ContainsAny(attachment.UUID, "/\\.") {
		return nil, errors.New("invalid attachment identifier from Gitea")
	}
	response, err := client.DownloadSigned(ctx, strings.TrimRight(client.BaseURL, "/")+"/attachments/"+url.PathEscape(attachment.UUID))
	if failure, ok := errors.AsType[*transport.APIError](err); ok && client.Signer != nil && failure.Status == http.StatusNotFound && failure.Storage == "" {
		return nil, errAttachmentSignature
	}
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}

// errAttachmentSignature explains the 404 that Gitea answers an SSH key account with for an
// attachment it just listed: its download route ignores HTTP signatures, so private files stay hidden.
var errAttachmentSignature = errors.New("Gitea does not accept SSH key accounts for attachment downloads from private repositories; log in with a token: gt auth login --with-token")

// downloadAPI opens the content an API endpoint serves, following one signed storage redirect,
// such as Gitea's to object storage that serves files directly, without credentials. The SDK's
// readers send that redirect's request through the instance client, which refuses another host.
func downloadAPI(ctx context.Context, client *transport.Client, endpoint string) (io.ReadCloser, error) {
	resolved, err := client.URL(endpoint)
	if err != nil {
		return nil, err
	}
	response, err := client.DownloadSigned(ctx, resolved)
	if err != nil {
		return nil, err
	}
	return response.Body, nil
}
