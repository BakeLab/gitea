// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"context"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIRetriesOnlyBodyFreeReads(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		body         io.Reader
		status       int
		calls        int
	}{
		{"get 503", http.MethodGet, nil, http.StatusServiceUnavailable, 3},
		{"head 502", http.MethodHead, nil, http.StatusBadGateway, 3},
		{"get 504", http.MethodGet, http.NoBody, http.StatusGatewayTimeout, 3},
		{"get 500", http.MethodGet, nil, http.StatusInternalServerError, 1},
		{"get with body", http.MethodGet, strings.NewReader(`{}`), http.StatusServiceUnavailable, 1},
		{"post", http.MethodPost, nil, http.StatusServiceUnavailable, 1},
		{"delete", http.MethodDelete, nil, http.StatusServiceUnavailable, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			req, err := http.NewRequest(tc.method, server.URL+"/api/v1/user", tc.body)
			require.NoError(t, err)
			resp, err := New(server.URL, "", false).API().Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tc.status, resp.StatusCode)
			require.Equal(t, tc.calls, calls)
		})
	}
}

func TestAPIRetriesOnlyTransientFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		calls int
	}{
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, 3},
		{"reset", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, 3},
		{"dropped", io.EOF, 3},
		{"lookup failed", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "server misbehaving", Name: "gitea.test", IsTemporary: true}}, 3},
		{"no such host", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "gitea.test", IsNotFound: true}}, 1},
		{"no answer", fmt.Errorf("no answer within 1s: %w", context.DeadlineExceeded), 1},
		{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, 1},
		{"untrusted", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, 1},
		{"rejected certificate", &net.OpError{Op: "remote error", Err: errors.New("tls: certificate required")}, 1},
		{"unreadable certificate", fmt.Errorf("cannot load the client certificate a.pem with key a.pem: %w", os.ErrNotExist), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			retry := retryTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, tc.err }), retries: 2}
			req, err := http.NewRequest(http.MethodGet, "https://example.test/api/v1/user", nil)
			require.NoError(t, err)
			response, err := retry.RoundTrip(req)
			require.ErrorIs(t, err, tc.err)
			if response != nil {
				defer response.Body.Close()
			}
			require.Equal(t, tc.calls, calls)
		})
	}
}

func TestAPIDoesNotRetryAReadWithoutAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		client := bubbleClient(t, func(_ http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			<-r.Context().Done()
		})
		response, err := client.WithResponseTimeout(time.Second).Do(context.Background(), http.MethodGet, "user", nil, nil)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		if response != nil {
			defer response.Body.Close()
		}
		require.Equal(t, int32(1), calls.Load())
	})
}

func TestAPIRetryRecoversAndStopsOnCancellation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := New(server.URL, "", false)
	resp, err := client.API().Get(server.URL + "/api/v1/user")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, calls)

	calls = 0
	ctx, cancel := context.WithCancel(context.Background())
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, unavailable.URL+"/api/v1/user", nil)
	require.NoError(t, err)
	canceledResponse, err := New(unavailable.URL, "", false).API().Do(req)
	require.ErrorIs(t, err, context.Canceled)
	if canceledResponse != nil {
		defer canceledResponse.Body.Close()
	}
	require.Equal(t, 1, calls)
}

func TestAPIAuthenticatesEveryAttempt(t *testing.T) {
	type seen struct{ authorization, sudo string }
	requests := []seen{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, seen{r.Header.Get("Authorization"), r.Header.Get("Sudo")})
		if len(requests) == 1 {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	client.Sudo = "bob"
	resp, err := client.API().Get(server.URL + "/api/v1/user")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, []seen{{"token secret", "bob"}, {"token secret", "bob"}}, requests)
}

type bodySigner struct{ bodies, digests []string }

func (s *bodySigner) SignRequest(r *http.Request, contents []byte) error {
	s.bodies = append(s.bodies, string(contents))
	s.digests = append(s.digests, r.Header.Get("Digest"))
	r.Header.Set("Signature", "signed")
	return nil
}

func TestAPISignsRequestBodies(t *testing.T) {
	received := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "signed", r.Header.Get("Signature"))
		require.Empty(t, r.Header.Get("Authorization"))
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		received = append(received, string(data))
	}))
	defer server.Close()
	signer := &bodySigner{}
	client := New(server.URL, "", false)
	client.Signer = signer
	for _, body := range []io.Reader{strings.NewReader(`{"replayable":true}`), io.MultiReader(strings.NewReader(`{"streamed":true}`)), nil} {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/user/keys", body)
		require.NoError(t, err)
		resp, err := client.API().Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	// A body that can be read again is hashed into the Digest header rather than buffered.
	digest := sha512.Sum512([]byte(`{"replayable":true}`))
	require.Equal(t, []string{"", `{"streamed":true}`, ""}, signer.bodies)
	require.Equal(t, []string{"SHA-512=" + base64.StdEncoding.EncodeToString(digest[:]), "", ""}, signer.digests)
	require.Equal(t, []string{`{"replayable":true}`, `{"streamed":true}`, ""}, received)
}

// readCounter counts the bytes read through it.
type readCounter struct {
	io.Reader
	n *int
}

func (r readCounter) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	*r.n += n
	return n, err
}

func TestAPISignsReplayableBodiesWithoutBuffering(t *testing.T) {
	const size = 4 << 20
	var received int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		received = int(n)
	}))
	defer server.Close()
	signer := &bodySigner{}
	client := New(server.URL, "", false)
	client.Signer = signer
	opened, read := 0, 0
	body := Replayable(func() io.Reader {
		opened++
		return readCounter{io.LimitReader(zeros{}, size), &read}
	})
	resp, err := client.Send(context.Background(), http.MethodPost, "upload", body, nil)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	digest := sha512.New()
	_, _ = io.Copy(digest, io.LimitReader(zeros{}, size))
	require.Equal(t, []string{""}, signer.bodies, "the signer gets no buffered body")
	require.Equal(t, []string{"SHA-512=" + base64.StdEncoding.EncodeToString(digest.Sum(nil))}, signer.digests)
	require.Equal(t, 2, opened, "the body is read once to hash it and once to send it")
	require.Equal(t, 2*size, read)
	require.Equal(t, size, received)
}

func TestAPIDoesNotFollowRedirectsOffTheInstance(t *testing.T) {
	leaked := false
	outside := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
	defer outside.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, outside.URL, http.StatusFound)
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	client.HTTP = server.Client()
	resp, err := client.API().Get(server.URL + "/api/v1/user")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.False(t, leaked)
}

func TestCaptureErrorsSanitizesAndKeepsBodyReadable(t *testing.T) {
	long := `{"message":"` + strings.Repeat("y", 5000) + `"}`
	for _, tc := range []struct {
		name, body, message string
		status              int
	}{
		{"message", `{"message":"branch exists` + "\u202e" + `"}`, "branch exists", http.StatusUnprocessableEntity},
		{"oversized", long, "", http.StatusInternalServerError},
		{"redirect", ``, "", http.StatusMovedPermanently},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			ctx, failure := CaptureErrors(context.Background())
			req, err := http.NewRequestWithContext(ctx, http.MethodPatch, server.URL+"/api/v1/repos/a/b?token=secret", nil)
			require.NoError(t, err)
			resp, err := New(server.URL, "secret", false).API().Do(req)
			require.NoError(t, err)
			data, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tc.body, string(data))
			recorded := failure()
			require.Equal(t, tc.status, recorded.Status)
			require.Equal(t, http.MethodPatch, recorded.Method)
			require.Equal(t, "/api/v1/repos/a/b", recorded.Endpoint)
			require.Equal(t, tc.message, recorded.Message)
			require.Equal(t, server.URL, recorded.Instance)
		})
	}
}

func TestCaptureErrorsIgnoresSuccessAndUncapturedRequests(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"ignored"}`))
	}))
	defer server.Close()
	client := New(server.URL, "", false)
	ctx, failure := CaptureErrors(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/user", nil)
	require.NoError(t, err)
	resp, err := client.API().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Nil(t, failure())

	status = http.StatusNotFound
	resp, err = client.API().Get(server.URL + "/api/v1/user")
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.JSONEq(t, `{"message":"ignored"}`, string(data))
}

func TestAPIFollowsRedirectsWithinTheInstanceAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/old/issues":
			http.Redirect(w, r, "/api/v1/repos/alice/new/issues", http.StatusMovedPermanently)
		case "/api/v1/loop":
			http.Redirect(w, r, "/api/v1/loop", http.StatusMovedPermanently)
		case "/web":
			http.Redirect(w, r, "/user/login", http.StatusFound)
		default:
			require.Equal(t, "token secret", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(r.Method + " " + r.URL.Path))
		}
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/v1/repos/alice/old/issues", "GET /api/v1/repos/alice/new/issues", http.StatusOK},
		{http.MethodHead, "/api/v1/repos/alice/old/issues", "", http.StatusOK},
		{http.MethodPost, "/api/v1/repos/alice/old/issues", "", http.StatusMovedPermanently},
		{http.MethodGet, "/web", "", http.StatusFound},
		{http.MethodGet, "/api/v1/loop", "", http.StatusMovedPermanently},
	} {
		req, err := http.NewRequest(tc.method, server.URL+tc.path, nil)
		require.NoError(t, err)
		resp, err := client.API().Do(req)
		require.NoError(t, err, tc.path)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, tc.status, resp.StatusCode, tc.method+" "+tc.path)
		if tc.status == http.StatusOK {
			require.Equal(t, tc.body, string(data))
		}
	}
}

func TestAPINamesTheRepositoryAWriteWasRedirectedTo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/old/labels":
			http.Redirect(w, r, "/api/v1/repos/bob/new%20name/labels?x=1", http.StatusMovedPermanently)
		default:
			http.Redirect(w, r, "/api/v1/elsewhere", http.StatusMovedPermanently)
		}
	}))
	defer server.Close()
	client := New(server.URL, "", false)
	postResponse, err := client.Do(context.Background(), http.MethodPost, "repos/alice/old/labels", strings.NewReader(`{}`), nil)
	require.EqualError(t, err, "repository moved to bob/new name; address it by its new name to change it")
	if postResponse != nil {
		defer postResponse.Body.Close()
	}
	deleteResponse, err := client.Do(context.Background(), http.MethodDelete, "repos/alice/old/topics/x", nil, nil)
	require.EqualError(t, err, "Gitea API returned HTTP 301 for DELETE /api/v1/repos/alice/old/topics/x")
	if deleteResponse != nil {
		defer deleteResponse.Body.Close()
	}
}
