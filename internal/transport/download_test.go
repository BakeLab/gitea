// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDownloadScopeAndRedirects(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "token sensitive", r.Header.Get("Authorization"))
		if r.URL.Path == "/base/redirect" {
			http.Redirect(w, r, "/outside", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("binary bytes"))
	}))
	defer server.Close()
	client := New(server.URL+"/base", "sensitive", false)
	// Even a substituted HTTP client cannot re-enable automatic redirects.
	client.HTTP = server.Client()
	for _, raw := range []string{server.URL + "/outside", server.URL + "/base/../outside", server.URL + "/base/%2e%2e/outside", "https://example.test/base/file"} {
		_, err := client.Download(context.Background(), raw)
		require.Error(t, err)
	}
	require.Zero(t, requests)
	_, err := client.Download(context.Background(), server.URL+"/base/redirect")
	require.Error(t, err)
	require.Equal(t, 1, requests)
	response, err := client.Download(context.Background(), server.URL+"/base/file")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "binary bytes", string(body))
}

func TestSignedDownloadsFollowOneRedirectWithoutCredentials(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Sudo"))
		_, _ = w.Write([]byte("archive"))
	}))
	defer storage.Close()
	for _, tc := range []struct {
		destination string
		status      int
		ok          bool
		requests    int
	}{
		{"/base/signed?signature=private-value", http.StatusFound, true, 1},
		{"/outside?signature=private-value", http.StatusFound, true, 1},
		{storage.URL + "/bucket/file?signature=private-value", http.StatusSeeOther, true, 0},
		{storage.URL + "/bucket/file?signature=private-value", http.StatusTemporaryRedirect, true, 0},
		{"/base/again", http.StatusFound, false, 1},
		{"ftp://storage.example.test/file?signature=private-value", http.StatusFound, false, 0},
	} {
		t.Run(tc.destination, func(t *testing.T) {
			signedRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/base/archive" {
					require.Equal(t, "token sensitive", r.Header.Get("Authorization"))
					require.Equal(t, "bob", r.Header.Get("Sudo"))
					http.Redirect(w, r, tc.destination, tc.status)
					return
				}
				signedRequests++
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Sudo"))
				if r.URL.Path == "/base/again" {
					http.Redirect(w, r, "/base/signed", http.StatusFound)
					return
				}
				_, _ = w.Write([]byte("archive"))
			}))
			defer server.Close()
			client := New(server.URL+"/base", "sensitive", false)
			client.Sudo = "bob"
			client.HTTP = server.Client() // The implementation must still suppress automatic redirects.
			response, err := client.DownloadSigned(context.Background(), server.URL+"/base/archive")
			require.Equal(t, tc.requests, signedRequests)
			if !tc.ok {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-value")
				return
			}
			require.NoError(t, err)
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "archive", string(data))
		})
	}
}

type fakeSigner struct{ calls int }

func (s *fakeSigner) SignRequest(r *http.Request, _ []byte) error {
	s.calls++
	r.Header.Set("Signature", "signed")
	return nil
}

func TestDownloadsSignOnlyInstanceRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/base/archive" {
			require.Equal(t, "signed", r.Header.Get("Signature"))
			http.Redirect(w, r, "/base/signed?signature=private-value", http.StatusFound)
			return
		}
		require.Empty(t, r.Header.Get("Signature"))
		require.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("archive"))
	}))
	defer server.Close()
	signer := &fakeSigner{}
	client := New(server.URL+"/base", "", false)
	client.HTTP = server.Client()
	client.Signer = signer
	response, err := client.DownloadSigned(context.Background(), server.URL+"/base/archive")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 1, signer.calls)
}

func TestURLAndDownloadShareScopeRejections(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++ }))
	defer server.Close()
	client := New(server.URL+"/base", "sensitive", false)
	client.HTTP = server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	for _, raw := range []string{
		"http://user:pass@" + host + "/base/api/v1/user", server.URL + "/base/api/v1/user#fragment", server.URL + "/base/api/v1/a\\b",
		server.URL + "/base/api/v1/./user", server.URL + "/base/api/v1/%2e%2e/user", server.URL + "/basement/api/v1/user", "//" + host + "/base/api/v1/user",
	} {
		_, err := client.URL(raw)
		require.Error(t, err, raw)
		_, err = client.Download(context.Background(), raw)
		require.Error(t, err, raw)
	}
	require.Zero(t, requests)
}

func TestSignedDownloadRejectsInvalidInstanceURL(t *testing.T) {
	_, err := New("http://[invalid", "sensitive", false).DownloadSigned(context.Background(), "http://example.test/file")
	require.ErrorContains(t, err, "invalid instance URL")
}

func TestDownloadsOutlastTheRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for range 10 {
			w.(http.Flusher).Flush()
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte("x"))
		}
	}))
	defer server.Close()
	// The download takes 300ms, but data never stops arriving for the timeout.
	client := New(server.URL, "", false).WithResponseTimeout(100 * time.Millisecond)
	response, err := client.Download(context.Background(), server.URL+"/attachments/x")
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "xxxxxxxxxx", string(data))
}

func TestResponseTimeoutBoundsABodyThatStalls(t *testing.T) {
	stalled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		select {
		case <-stalled:
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	defer close(stalled)
	client := New(server.URL, "", false).WithResponseTimeout(50 * time.Millisecond)
	response, err := client.Download(context.Background(), server.URL+"/attachments/x")
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "no data received within 50ms")
	require.Equal(t, "x", string(data))
	// A body that is read slowly does not run out of time while it is not read.
	response, err = client.Send(context.Background(), http.MethodGet, "version", nil, nil)
	require.NoError(t, err)
	defer response.Body.Close()
	time.Sleep(100 * time.Millisecond)
	buffer := make([]byte, 1)
	n, err := response.Body.Read(buffer)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestConfiguredHeadersReachOnlyTheInstance(t *testing.T) {
	var storageHeader string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageHeader = r.Header.Get("Cf-Access-Client-Secret")
		_, _ = w.Write([]byte("file"))
	}))
	defer storage.Close()
	var instanceHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceHeader = r.Header.Get("Cf-Access-Client-Secret")
		http.Redirect(w, r, storage.URL+"/signed", http.StatusFound)
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithHeaders(http.Header{"Cf-Access-Client-Secret": {"secret"}})
	response, err := client.DownloadSigned(context.Background(), server.URL+"/attachments/x")
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, "secret", instanceHeader)
	require.Empty(t, storageHeader)
}

func TestConfiguredHeaderValuesAreRedactedWhenEchoed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"proxy rejected secret ` + r.Header.Get("X-Proxy-Secret") + ` for tenant ` + r.Header.Get("X-Tenant") + `"}`))
	}))
	defer server.Close()
	var log bytes.Buffer
	client := New(server.URL, "", false).Debug(&log).WithHeaders(http.Header{"X-Proxy-Secret": {"hdrsecret"}, "X-Tenant": {"acme"}})
	_, err := client.Do(context.Background(), http.MethodGet, "version", nil, nil)
	require.EqualError(t, err, "permission denied by the Gitea instance: proxy rejected secret [REDACTED] for tenant acme")
	require.Contains(t, log.String(), "proxy rejected secret [REDACTED] for tenant acme")
	require.NotContains(t, log.String(), "hdrsecret")
}

func TestConfiguredHeadersYieldToTheRequestsOwn(t *testing.T) {
	var tenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant = r.Header.Get("X-Tenant")
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithHeaders(http.Header{"X-Tenant": {"configured"}})
	response, err := client.Send(context.Background(), http.MethodGet, "version", nil, http.Header{"X-Tenant": {"explicit"}})
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, "explicit", tenant)
}

func TestResponseTimeoutBoundsOnlyTheWaitForAnAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithResponseTimeout(20 * time.Millisecond)
	_, err := client.Send(context.Background(), http.MethodGet, "version", nil, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// A synchronous migration answers only once it is done.
	response, err := client.Send(UnboundedResponse(context.Background()), http.MethodGet, "version", nil, nil)
	require.NoError(t, err)
	_ = response.Body.Close()
}

// slowReader yields one byte per read after a pause, like a large upload on a slow link.
type slowReader struct {
	chunks int
	pause  time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.chunks == 0 {
		return 0, io.EOF
	}
	r.chunks--
	time.Sleep(r.pause)
	p[0] = 'x'
	return 1, nil
}

func TestResponseTimeoutStartsOnceTheBodyIsSent(t *testing.T) {
	var delay atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		time.Sleep(time.Duration(delay.Load()))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithResponseTimeout(50 * time.Millisecond)
	upload := func() (string, error) {
		response, err := client.Send(context.Background(), http.MethodPost, "repos/a/b/releases/1/assets", &slowReader{chunks: 8, pause: 20 * time.Millisecond}, nil)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		return string(data), err
	}
	// The upload takes longer than the timeout, which bounds only the wait for the answer.
	data, err := upload()
	require.NoError(t, err)
	require.Equal(t, "xxxxxxxx", data)
	delay.Store(int64(200 * time.Millisecond))
	_, err = upload()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "no answer within 50ms")
}

func TestResponseTimeoutBoundsAnUploadThatStopsGoingOut(t *testing.T) {
	// A stalled proxy accepts the connection but never reads the body.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	client := New("http://"+listener.Addr().String(), "", false).WithResponseTimeout(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(5*time.Second, cancel)
	_, err = client.Send(ctx, http.MethodPost, "repos/a/b/releases/1/assets", io.LimitReader(zeros{}, 64<<20), nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "no progress sending the request within 100ms")
}

// zeros yields zero bytes forever.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestResponseTimeoutWaitsWhileTheConnectionSendsTheBody(t *testing.T) {
	// Once the body has been read, the connection still holds 1 MB of it, which a slow link sends
	// in 900ms, three times the timeout, before the server can answer.
	const drain = 900 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(drain)
		_, _ = w.Write([]byte("done"))
	}))
	defer server.Close()
	for name, stalls := range map[string]bool{"draining": false, "stalled": true} {
		t.Run(name, func(t *testing.T) {
			var sent atomic.Int64
			defer func(original func(net.Conn) (int, bool)) { unsentBytes = original }(unsentBytes)
			unsentBytes = func(net.Conn) (int, bool) {
				if sent.CompareAndSwap(0, time.Now().UnixNano()) || stalls {
					return 1 << 20, true
				}
				left := drain - time.Since(time.Unix(0, sent.Load()))
				return int(max(0, left) / (100 * time.Millisecond) * (100 << 10)), true
			}
			client := New(server.URL, "", false).WithResponseTimeout(300 * time.Millisecond)
			response, err := client.Send(context.Background(), http.MethodPost, "repos/a/b/releases/1/assets", strings.NewReader("upload"), nil)
			if stalls {
				require.ErrorContains(t, err, "no progress sending the request within 300ms")
				return
			}
			require.NoError(t, err)
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "done", string(data))
		})
	}
}
