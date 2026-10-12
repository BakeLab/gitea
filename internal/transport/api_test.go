// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	_, err = New(unavailable.URL, "", false).API().Do(req)
	require.ErrorIs(t, err, context.Canceled)
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

type bodySigner struct{ bodies []string }

func (s *bodySigner) SignRequest(r *http.Request, contents []byte) error {
	s.bodies = append(s.bodies, string(contents))
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
	require.Equal(t, []string{`{"replayable":true}`, `{"streamed":true}`, ""}, signer.bodies)
	require.Equal(t, signer.bodies, received)
}

func TestAPINeverFollowsRedirects(t *testing.T) {
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
		{"message", `{"message":"branch exists‮"}`, "branch exists", http.StatusUnprocessableEntity},
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
