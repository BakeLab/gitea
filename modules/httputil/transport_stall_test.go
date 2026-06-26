// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package httputil

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// TestWrapTransportTimeouts verifies the transport returned by WrapTransport
// carries tea's standard timeout values, so a stalled server can't make tea
// hang forever (issue #1018).
func TestWrapTransportTimeouts(t *testing.T) {
	rt := WrapTransport(nil)
	uat, ok := rt.(*userAgentTransport)
	if !ok {
		t.Fatalf("WrapTransport returned %T, want *userAgentTransport", rt)
	}
	tr, ok := uat.base.(*http.Transport)
	if !ok {
		t.Fatalf("underlying base is %T, want *http.Transport", uat.base)
	}
	if tr.TLSHandshakeTimeout != TLSHandshakeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, want %v", tr.TLSHandshakeTimeout, TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout != ResponseHeaderTimeout {
		t.Errorf("ResponseHeaderTimeout = %v, want %v", tr.ResponseHeaderTimeout, ResponseHeaderTimeout)
	}
	if tr.DialContext == nil {
		t.Error("DialContext is nil, want a dialer with DialTimeout")
	}
}

// newStallListener returns a listener that accepts connections, reads the
// request, then goes silent without ever sending response headers — the
// "server accepts the connection but never responds" case ResponseHeaderTimeout
// guards against. The returned closer stops the listener.
func newStallListener(t *testing.T) (addr string, closer func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				_, _ = c.Read(buf) // drain the request, then never respond
				<-done             // hold the connection open until the test ends
				c.Close()
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		close(done)
		ln.Close()
	}
}

// TestResponseHeaderTimeoutFires proves a request to a server that accepts the
// connection and request but never sends response headers aborts via
// ResponseHeaderTimeout rather than hanging. It builds the transport the same
// way WrapTransport does, with a short ResponseHeaderTimeout so the test is fast.
func TestResponseHeaderTimeoutFires(t *testing.T) {
	addr, closer := newStallListener(t)
	defer closer()

	tr := timeoutTransport(nil)
	tr.ResponseHeaderTimeout = 2 * time.Second
	client := &http.Client{Transport: &userAgentTransport{base: tr}}

	start := time.Now()
	_, err := client.Get("http://" + addr + "/")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from stalled server, got nil")
	}
	if elapsed > 10*time.Second {
		t.Errorf("request took %v; ResponseHeaderTimeout did not fire", elapsed)
	}
	t.Logf("request failed as expected after %v: %v", elapsed, err)
}
