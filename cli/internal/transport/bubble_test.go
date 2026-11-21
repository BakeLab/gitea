// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// pipeListener hands out in-memory connections. Unlike those of a socket, a synctest bubble can
// tell that they are idle, so its fake clock decides when a timeout runs out, not the machine's load.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener(t *testing.T) *pipeListener {
	l := &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

// client returns a client whose connections go to the listener.
func (l *pipeListener) client(t *testing.T) *Client {
	client := New("http://gitea.test", "", false)
	transport, ok := replaceHTTPTransport(client.HTTP.Transport, func(tr *http.Transport) http.RoundTripper {
		tr.Proxy = nil
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			near, far := net.Pipe()
			select {
			case l.conns <- far:
				return near, nil
			case <-l.closed:
				return nil, net.ErrClosed
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return tr
	})
	require.True(t, ok)
	client.HTTP.Transport = transport
	t.Cleanup(client.HTTP.CloseIdleConnections) // the bubble ends only once their goroutines have
	return client
}

// bubbleClient returns a client of a server that runs handler in the synctest bubble of t.
func bubbleClient(t *testing.T, handler http.HandlerFunc) *Client {
	listener := newPipeListener(t)
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.client(t)
}
