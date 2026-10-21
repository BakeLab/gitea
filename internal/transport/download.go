// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Download opens an instance-scoped file URL, following redirects only within the instance API.
func (c *Client) Download(ctx context.Context, raw string) (*http.Response, error) {
	_, target, err := c.downloadTarget(raw)
	if err != nil {
		return nil, err
	}
	return c.get(ctx, target, false)
}

// downloadTarget returns the parsed instance URL and raw, which must lie within its base path.
func (c *Client) downloadTarget(raw string) (*url.URL, *url.URL, error) {
	base, target, err := c.scoped(raw, "download URL")
	if err != nil {
		return nil, nil, err
	}
	if !within(base, target, "/") {
		return nil, nil, errors.New("download URL must belong to the selected instance and base path")
	}
	return base, target, nil
}

func (c *Client) get(ctx context.Context, target *url.URL, allowRedirect bool) (*http.Response, error) {
	ctx, failure := CaptureErrors(ctx)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("cannot construct download request")
	}
	response, err := c.API().Do(request)
	if err != nil {
		return nil, RequestError(ctx, "download failed", err)
	}
	if (allowRedirect && redirect(response.StatusCode)) || (response.StatusCode >= 200 && response.StatusCode < 300) {
		return response, nil
	}
	_ = response.Body.Close()
	return nil, failure()
}

func redirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// DownloadSigned opens an instance-scoped file URL and follows at most one redirect without
// credentials, such as Gitea's redirect to signed object-storage URLs. The redirect target may be
// another host, but must use HTTPS or the instance's scheme. Automatic redirects remain disabled,
// including on an injected HTTP client.
func (c *Client) DownloadSigned(ctx context.Context, raw string) (*http.Response, error) {
	base, target, err := c.downloadTarget(raw)
	if err != nil {
		return nil, err
	}
	response, err := c.get(ctx, target, true)
	if err != nil || !redirect(response.StatusCode) {
		return response, err
	}
	_ = response.Body.Close()
	location, err := response.Location()
	if err != nil {
		return nil, errors.New("download returned an invalid redirect")
	}
	if location.Scheme != "https" && location.Scheme != base.Scheme {
		return nil, errors.New("download redirect must use HTTPS or the instance's scheme")
	}
	unsigned := *c
	unsigned.Token, unsigned.Signer, unsigned.Sudo = "", nil, ""
	response, err = unsigned.get(ctx, location, false)
	if failure, ok := errors.AsType[*APIError](err); ok && !strings.EqualFold(location.Host, base.Host) {
		failure.Storage = location.Host
	}
	return response, err
}
