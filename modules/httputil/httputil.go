// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package httputil

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"runtime"

	"gitea.dev/tea/modules/version"
)

// UserAgent returns the standard User-Agent string for tea.
func UserAgent() string {
	ua := fmt.Sprintf("tea/%s (%s/%s)", version.Version, runtime.GOOS, runtime.GOARCH)
	if version.SDK != "" {
		ua += fmt.Sprintf(" go-sdk/%s", version.SDK)
	}
	return ua
}

// WrapTransport returns tea's standard HTTP transport: an *http.Transport
// preset with tea's connection / response-header timeouts (see timeoutTransport)
// and decorated to add the User-Agent header on every request. The supplied
// tlsConfig is attached as-is (nil is fine); callers use it for insecure /
// skip-verify logins. This is the single entry point for building a tea HTTP
// client transport, so the timeouts can't be accidentally omitted.
func WrapTransport(tlsConfig *tls.Config) http.RoundTripper {
	return &userAgentTransport{base: timeoutTransport(tlsConfig)}
}

type userAgentTransport struct {
	base http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Set the UA at the transport so every client built from WrapTransport
	// identifies itself, including the non-SDK clients (oauth2 flow, token
	// refresh) that never pass through the SDK's own SetUserAgent. For SDK
	// clients this overlaps gitea.SetUserAgent; both use httputil.UserAgent(),
	// so the duplicate Header.Set is a no-op.
	req.Header.Set("User-Agent", UserAgent())
	return t.base.RoundTrip(req)
}
