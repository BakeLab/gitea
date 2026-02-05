// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package httputil

import (
	"fmt"
	"net/http"
	"runtime"

	"code.gitea.io/tea/modules/version"
)

// UserAgent returns the standard User-Agent string for tea.
func UserAgent() string {
	ua := fmt.Sprintf("tea/%s (%s/%s)", version.Version, runtime.GOOS, runtime.GOARCH)
	if version.SDK != "" {
		ua += fmt.Sprintf(" go-sdk/%s", version.SDK)
	}
	return ua
}

// WrapTransport wraps an http.RoundTripper to add the User-Agent header.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &userAgentTransport{base: base}
}

type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", UserAgent())
	return t.base.RoundTrip(req)
}
