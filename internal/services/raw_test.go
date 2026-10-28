// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

// upload sends a small attachment upload, the request gt still sends without the SDK.
func upload(ctx context.Context, client *transport.Client, endpoint string) error {
	_, err := uploadAttachment(ctx, client, endpoint, "a.txt", strings.NewReader("a"))
	return err
}

func TestRawBodyCancellationPreservesContextError(t *testing.T) {
	started := make(chan struct{})
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"incomplete":`))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	require.ErrorIs(t, upload(ctx, client, "resource"), context.Canceled)
}

func TestRawBodyThatBreaksOffIsARequestFailure(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stalled":
			_, _ = w.Write([]byte(`{"incomplete":`))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		case "/api/v1/dropped":
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte(`{"incomplete":`))
		case "/api/v1/invalid":
			_, _ = w.Write([]byte(`<html>`))
		}
	}))
	client = client.WithResponseTimeout(50 * time.Millisecond)
	err := upload(context.Background(), client, "stalled")
	failure, ok := errors.AsType[*transport.RequestFailure](err)
	require.True(t, ok, "a stalled body is a request failure, not invalid JSON: %v", err)
	require.ErrorIs(t, err, transport.ErrTimeout)
	require.Equal(t, client.BaseURL, failure.Server)
	err = upload(context.Background(), client, "dropped")
	require.ErrorAs(t, err, new(*transport.RequestFailure))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.ErrorIs(t, upload(context.Background(), client, "invalid"), errInvalidJSON)
	require.ErrorIs(t, upload(context.Background(), client, "empty"), errInvalidJSON)
}
