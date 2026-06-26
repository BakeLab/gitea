// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package httputil

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// Timeout values applied to every Gitea API request. These are deliberately
// connection-establishment and time-to-first-response-byte timeouts, NOT an
// overall request deadline: a large release-attachment upload can legitimately
// run for minutes, and as long as bytes keep flowing none of these fire. They
// only trip when a server accepts the connection but never (or far too slowly)
// starts responding — the "hangs forever" case from a stalled or unresponsive
// server (issue #1018).
const (
	// DialTimeout bounds establishing the TCP connection.
	DialTimeout = 10 * time.Second
	// TLSHandshakeTimeout bounds completing the TLS handshake.
	TLSHandshakeTimeout = 10 * time.Second
	// ResponseHeaderTimeout bounds the wait, after the request is written, for
	// the server to begin sending response headers. This is the only timeout
	// that protects against a server which accepts the connection but then goes
	// silent — the originally reported #1018 symptom; DialTimeout/
	// TLSHandshakeTimeout do not, because the connection already succeeded.
	//
	// The value must clear Gitea's legitimate synchronous pre-response work.
	// Profiling a self-hosted Gitea 1.24.6 (on hardware slower than gitea.com)
	// showed creating a pull request that triggers conflict detection across
	// ~1500 changed files takes ~10s before the first byte (3 runs: 10.06 /
	// 10.08 / 10.24s); clean-diff PR creation was ~1s and large attachment
	// uploads ~9ms. 120s is ~12x that measured worst case, leaving generous
	// headroom for larger repos and busier servers while still failing in two
	// minutes instead of hanging forever.
	ResponseHeaderTimeout = 120 * time.Second
)

// timeoutTransport returns an *http.Transport configured with tea's standard
// timeouts. The supplied tlsConfig is attached as-is (callers use it for
// insecure / skip-verify logins). It is a clone of http.DefaultTransport so
// connection pooling, proxy support and HTTP/2 keep working. Callers obtain it
// through WrapTransport, which also adds the User-Agent header.
func timeoutTransport(tlsConfig *tls.Config) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = (&net.Dialer{
		Timeout:   DialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = TLSHandshakeTimeout
	t.ResponseHeaderTimeout = ResponseHeaderTimeout
	t.TLSClientConfig = tlsConfig
	return t
}
