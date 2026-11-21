// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"gitea.dev/cli/internal/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstanceScopeAndRedirect(t *testing.T) {
	leaked := false
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(http.StatusOK) }))
	defer outside.Close()
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/base/api/v1/user", r.URL.Path)
		require.Equal(t, "token secret", r.Header.Get("Authorization"))
		http.Redirect(w, r, outside.URL, http.StatusFound)
	}))
	defer selected.Close()
	client := New(selected.URL+"/base", "secret", false)
	response, err := client.Do(context.Background(), http.MethodGet, "user", nil, nil)
	require.Error(t, err)
	if response != nil {
		defer response.Body.Close()
	}
	require.False(t, leaked)
	for _, endpoint := range []string{outside.URL + "/api/v1/user", strings.Replace(selected.URL, "http:", "https:", 1) + "/base/api/v1/user", selected.URL + "/api/v1/user", "../user", selected.URL + "/base/api/v1/../../other", "//other.example/api/v1/user", "repos/a\\b", "repos/a%5C..%5Cb"} {
		_, err := client.URL(endpoint)
		require.Error(t, err, endpoint)
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := New("https://example.org", "", false)
	response, err := client.Do(ctx, http.MethodGet, "user", nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	if response != nil {
		defer response.Body.Close()
	}
}

func TestURLPreservesEscapedSegments(t *testing.T) {
	client := New("https://example.test/base%20path", "", false)
	resolved, err := client.URL("repos/alice/project/releases/tags/release%2Fv1")
	require.NoError(t, err)
	require.Equal(t, "https://example.test/base%20path/api/v1/repos/alice/project/releases/tags/release%2Fv1", resolved)
	resolved, err = client.URL("repos/alice/project/wiki/page/back%5Cslash")
	require.NoError(t, err)
	require.Equal(t, "https://example.test/base%20path/api/v1/repos/alice/project/wiki/page/back%5Cslash", resolved)
}

func TestAPIErrorIncludesOnlySanitizedServerMessage(t *testing.T) {
	const merge = "Gitea API returned HTTP %d for POST /api/v1/repos/a/b/pulls/1/merge"
	long := strings.Repeat("x", 400)
	for _, tc := range []struct {
		name, body, want, contentType string
		status                        int
	}{
		{"merge refusal", `{"message":"Please try again later","url":"https://leak.example/?key=abc"}`, fmt.Sprintf(merge, 405) + ": Please try again later", "", 405},
		{"token scope", `{"message":"token does not have at least one of required scope(s): [read:user]"}`, "permission denied by the Gitea instance: token does not have at least one of required scope(s): [read:user]", "", 403},
		{"control characters", "{\"message\":\"bad\\u001b[31m\\nnews\\u202e\"}", fmt.Sprintf(merge, 422) + ": bad [31m news", "", 422},
		{"capped", `{"message":"` + long + `"}`, fmt.Sprintf(merge, 500) + ": " + long[:300] + "...", "", 500},
		{"not json", `<html>key=abc</html>`, fmt.Sprintf(merge, 500), "", 500},
		{"other fields", `{"errors":["abc"]}`, fmt.Sprintf(merge, 500), "", 500},
		{"oversized body", `{"message":"` + strings.Repeat("y", 5000) + `"}`, fmt.Sprintf(merge, 500), "", 500},
		{"token echo", `{"message":"bad token secret"}`, fmt.Sprintf(merge, 400), "", 400},
		{"authentication", `{"message":"user alice not found"}`, "authentication rejected; run gt auth login", "", 401},
		{"bare not found", `{"message":"not found","url":"https://leak.example/?key=abc"}`, "not found (HTTP 404 POST /api/v1/repos/a/b/pulls/1/merge)", "", 404},
		{"described not found", `{"message":"label does not exist [label_id: 9]"}`, "not found (HTTP 404 POST /api/v1/repos/a/b/pulls/1/merge): label does not exist [label_id: 9]", "", 404},
		{"plain text", "invalid package name\n", fmt.Sprintf(merge, 400) + ": invalid package name", "text/plain;charset=utf-8", 400},
		{"plain text token echo", "bad token secret", fmt.Sprintf(merge, 400), "text/plain", 400},
		{"plain text capped", long, fmt.Sprintf(merge, 400) + ": " + long[:300] + "...", "text/plain", 400},
		{"oversized plain text", strings.Repeat("y", 5000), fmt.Sprintf(merge, 400), "text/plain", 400},
		{"html", `<html>key=abc</html>`, fmt.Sprintf(merge, 502), "text/html", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", cmp.Or(tc.contentType, "application/json"))
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			response, err := New(server.URL, "secret", false).Do(context.Background(), http.MethodPost, "repos/a/b/pulls/1/merge?key=abc", strings.NewReader(`{}`), nil)
			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			if response != nil {
				defer response.Body.Close()
			}
			require.Equal(t, tc.status, apiErr.Status)
			require.Equal(t, tc.want, err.Error())
			require.NotContains(t, err.Error(), "abc")
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestSudoHeaderOnAPIAndDownloadRequests(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Sudo"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	for _, sudo := range []string{"", "bob"} {
		client.Sudo = sudo
		response, err := client.Do(context.Background(), http.MethodGet, "user", nil, nil)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}
	response, err := client.Download(context.Background(), server.URL+"/file")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, []string{"/api/v1/user ", "/api/v1/user bob", "/file bob"}, seen)
}

func TestTransportBoundsOnlySetupAndSendsUserAgent(t *testing.T) {
	agents := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	// A whole-request timeout would cut off long uploads and downloads that are still progressing.
	require.Zero(t, client.HTTP.Timeout)
	transport, ok := client.HTTP.Transport.(userAgentTransport)
	require.True(t, ok)
	tr, ok := transport.base.(*http.Transport)
	require.True(t, ok)
	require.Zero(t, tr.ResponseHeaderTimeout, "a synchronous migration answers only when it is done")
	require.Equal(t, 10*time.Second, tr.TLSHandshakeTimeout)
	for _, open := range []func() (*http.Response, error){
		func() (*http.Response, error) {
			return client.Do(context.Background(), http.MethodGet, "user", nil, nil)
		},
		func() (*http.Response, error) { return client.Download(context.Background(), server.URL+"/file") },
	} {
		response, err := open()
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}
	want := "gt/" + version.Current() + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
	require.Equal(t, []string{want, want}, agents)
}

func TestNetworkErrorsKeepCauseWithoutURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	client := New(server.URL, "secret", false)
	apiResponse, err := client.Do(context.Background(), http.MethodPost, "user?token=query-secret", strings.NewReader(`{}`), nil)
	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
	if apiResponse != nil {
		defer apiResponse.Body.Close()
	}
	require.ErrorContains(t, err, "API request failed: ")
	require.NotContains(t, err.Error(), "query-secret")
	var failure *RequestFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, server.URL, failure.Server)
	downloadResponse, err := client.Download(context.Background(), server.URL+"/file?signature=query-secret")
	require.ErrorAs(t, err, &opErr)
	if downloadResponse != nil {
		defer downloadResponse.Body.Close()
	}
	require.ErrorContains(t, err, "download failed: ")
	require.NotContains(t, err.Error(), "query-secret")
}

func TestDebugLogsRequestsWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/a/b/labels/8":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"label does not exist [label_id: 8]"}`))
		case "/api/v1/repos/a/b/labels/9":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"label does not exist; secret-token"}`))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	var log bytes.Buffer
	client := New(server.URL, "secret-token", false).Debug(&log)
	teapotResponse, err := client.Do(context.Background(), http.MethodGet, "repos/a/b?page=2&state=open&token=query-secret&access_token=query-secret", nil, nil)
	require.Error(t, err)
	if teapotResponse != nil {
		defer teapotResponse.Body.Close()
	}
	require.Regexp(t, `^\[debug\] GET `+regexp.QuoteMeta(server.URL)+`/api/v1/repos/a/b\?access_token=REDACTED&page=2&state=open&token=REDACTED -> 418 I'm a teapot \(\d+(\.\d+)?[µm]?s\)\n$`, log.String())
	log.Reset()
	notFoundResponse, err := client.Do(context.Background(), http.MethodGet, "repos/a/b/labels/8", nil, nil)
	require.Regexp(t, `-> 404 Not Found \(\d+(\.\d+)?[µm]?s\): label does not exist \[label_id: 8\]\n$`, log.String())
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	if notFoundResponse != nil {
		defer notFoundResponse.Body.Close()
	}
	require.Equal(t, "label does not exist [label_id: 8]", apiErr.Message, "the logged body stays readable")
	log.Reset()
	redactedResponse, err := client.Do(context.Background(), http.MethodGet, "repos/a/b/labels/9", nil, nil)
	require.Error(t, err)
	if redactedResponse != nil {
		defer redactedResponse.Body.Close()
	}
	require.Regexp(t, `-> 404 Not Found \(\d+(\.\d+)?[µm]?s\)\n$`, log.String(), "a message echoing the token is dropped")
	require.NotContains(t, log.String(), "secret")
}

func TestMalformedResponsesDoNotExposeRequestSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\n%s %s\r\n\r\n", r.Header.Get("Authorization"), r.Header.Get("X-Proxy-Secret"))
		_ = rw.Flush()
	}))
	defer server.Close()
	var log bytes.Buffer
	client := New(server.URL, "private-access-token", false).Debug(&log).WithHeaders(http.Header{"X-Proxy-Secret": {"private-proxy-secret"}})
	client.Retries = 0
	response, err := client.Do(context.Background(), http.MethodGet, "user", nil, nil)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err)
	for _, secret := range []string{"private-access-token", "private-proxy-secret"} {
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, log.String(), secret)
	}
}

func TestMalformedTrailersDoNotExposeRequestSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-Result\r\n\r\n1\r\nx\r\n0\r\n%s %s\r\n\r\n", r.Header.Get("Authorization"), r.Header.Get("X-Proxy-Secret"))
		_ = rw.Flush()
	}))
	defer server.Close()
	client := New(server.URL, "private-access-token", false).WithHeaders(http.Header{"X-Proxy-Secret": {"private-proxy-secret"}})
	response, err := client.Do(context.Background(), http.MethodGet, "user", nil, nil)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	require.Error(t, err)
	failure := ResponseError(context.Background(), response, err)
	for _, secret := range []string{"private-access-token", "private-proxy-secret"} {
		require.NotContains(t, failure.Error(), secret)
	}
}

func TestDebugLogsWhyARequestTimedOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log bytes.Buffer
		client := bubbleClient(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }).Debug(&log).WithResponseTimeout(time.Second)
		response, err := client.Do(context.Background(), http.MethodGet, "version", nil, nil)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		if response != nil {
			defer response.Body.Close()
		}
		require.ErrorIs(t, err, ErrTimeout, "told apart from the end of the caller's deadline")
		require.ErrorContains(t, err, "no answer within 1s")
		require.Regexp(t, `-> error: no answer within 1s: context deadline exceeded \(1s\)\n$`, log.String())
	})
}

func TestDebugRedactsOnlySensitiveQueryValues(t *testing.T) {
	for _, tc := range []struct {
		path, key string
		redacted  bool
	}{
		{"/api/v1/repos/a/b/issues", "limit", false},
		{"/api/v1/repos/a/b/issues", "page", false},
		{"/api/v1/repos/a/b/issues", "state", false},
		{"/api/v1/repos/a/b/issues", "labels", false},
		{"/api/v1/repos/a/b/issues", "token", true},
		{"/api/v1/repos/a/b/issues", "access_token", true},
		{"/api/v1/users", "password", true},
		{"/login/oauth/authorize", "state", true},
		{"/login/oauth/access_token", "code", true},
		{"/login/oauth/access_token", "client_secret", true},
		{"/storage/blob", "X-Amz-Signature", true},
		{"/storage/blob", "X-Amz-Credential", true},
		{"/storage/blob", "X-Amz-Security-Token", true},
		{"/storage/blob", "sig", true},
		{"/storage/blob", "X-Amz-Date", false},
	} {
		require.Equal(t, tc.redacted, sensitiveQuery(tc.path, tc.key), "%s %s", tc.path, tc.key)
	}
}

func TestDebugStripsControlsFromTheStatusLine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 OK\x1b]0;PWNED\x07\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = rw.Flush()
	}))
	defer server.Close()
	var log bytes.Buffer
	response, err := New(server.URL, "t", false).Debug(&log).Do(context.Background(), http.MethodGet, "version", nil, nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.NotContains(t, log.String(), "\x1b")
	require.NotContains(t, log.String(), "\x07")
	require.Contains(t, log.String(), "-> 200 OK")
}

func TestClientCertificatesAreLoadedForInstancesThatRequireThem(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gt"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	combined := filepath.Join(dir, "both.pem")
	require.NoError(t, os.WriteFile(combined, append(must(os.ReadFile(certFile)), must(os.ReadFile(keyFile))...), 0o600))

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.TLS.PeerCertificates[0].Subject.CommonName))
	}))
	pool := x509.NewCertPool()
	pool.AddCert(must(x509.ParseCertificate(der)))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	server.StartTLS()
	defer server.Close()

	get := func(client *Client) (string, error) {
		response, err := client.Send(context.Background(), http.MethodGet, "version", nil, nil)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		return string(data), err
	}
	_, err = get(New(server.URL, "", true))
	require.Error(t, err)
	for _, client := range []*Client{
		New(server.URL, "", true).WithClientCertificate(certFile, keyFile),
		New(server.URL, "", true).WithClientCertificate(combined, ""),
		// --debug wraps the transport before the certificate is configured.
		New(server.URL, "", true).Debug(io.Discard).WithHeaders(http.Header{"X-Tenant": {"a"}}).WithClientCertificate(certFile, keyFile),
	} {
		name, err := get(client)
		require.NoError(t, err)
		require.Equal(t, "gt", name)
	}
	_, err = get(New(server.URL, "", true).WithClientCertificate(filepath.Join(dir, "missing.pem"), ""))
	require.ErrorContains(t, err, "cannot load the client certificate")
	// A transport that cannot present the certificate fails instead of connecting without it.
	custom := New(server.URL, "", true)
	custom.HTTP.Transport = roundTripFunc(http.DefaultTransport.RoundTrip)
	_, err = get(custom.WithClientCertificate(certFile, keyFile))
	require.ErrorContains(t, err, "cannot present the client certificate "+certFile)

	// A host that a download redirects to never sees the certificate.
	presented := true
	storage := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = len(r.TLS.PeerCertificates) > 0
	}))
	storage.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	storage.StartTLS()
	defer storage.Close()
	redirecting := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+"/signed", http.StatusFound)
	}))
	redirecting.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	redirecting.StartTLS()
	defer redirecting.Close()
	client := New(redirecting.URL, "", true).WithClientCertificate(certFile, keyFile)
	trustOtherHosts(t, client, storage)
	response, err := client.DownloadSigned(context.Background(), redirecting.URL+"/attachments/x")
	require.NoError(t, err)
	_ = response.Body.Close()
	require.False(t, presented)
}

func TestExpiredClientCertificatesAreRejected(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	expired := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: expired.AddDate(-1, 0, 0), NotAfter: expired}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "both.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	data = append(data, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: must(x509.MarshalECPrivateKey(key))})...)
	require.NoError(t, os.WriteFile(file, data, 0o600))
	_, err = LoadClientCertificate(file, "")
	require.EqualError(t, err, "the client certificate "+file+" expired on 2020-01-02")
}

func TestInsecureSkipsVerificationOnlyForTheInstance(t *testing.T) {
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("file"))
	}))
	defer storage.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+"/signed", http.StatusFound)
	}))
	defer server.Close()
	client := New(server.URL, "", true)
	response, err := client.Send(context.Background(), http.MethodGet, "version", nil, nil)
	require.NoError(t, err)
	_ = response.Body.Close()
	untrustedResponse, err := client.DownloadSigned(context.Background(), server.URL+"/attachments/x")
	var untrusted *tls.CertificateVerificationError
	require.ErrorAs(t, err, &untrusted)
	if untrustedResponse != nil {
		defer untrustedResponse.Body.Close()
	}
	trustOtherHosts(t, client, storage)
	response, err = client.DownloadSigned(context.Background(), server.URL+"/attachments/x")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "file", string(must(io.ReadAll(response.Body))))
}

func TestInstanceHostMatchesIgnoringCase(t *testing.T) {
	var tenant string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant = r.Header.Get("X-Tenant")
	}))
	defer server.Close()
	port := server.URL[strings.LastIndex(server.URL, ":"):]
	client := New("https://localhost"+port, "", true).WithHeaders(http.Header{"X-Tenant": {"acme"}})
	// A link or redirect from a ROOT_URL that differs only in case still goes to the instance,
	// which is exempt from certificate verification and gets the configured headers.
	response, err := client.Send(context.Background(), http.MethodGet, "https://LOCALHOST"+port+"/api/v1/version", nil, nil)
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, "acme", tenant)
}

// trustOtherHosts makes the hosts other than the instance of an insecure client trust the
// certificate of server.
func trustOtherHosts(t *testing.T, client *Client, server *httptest.Server) {
	t.Helper()
	transport, ok := client.HTTP.Transport.(userAgentTransport)
	require.True(t, ok)
	host, ok := transport.base.(hostTransport)
	require.True(t, ok)
	other, ok := host.other.(*http.Transport)
	require.True(t, ok)
	serverTransport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)
	other.TLSClientConfig.RootCAs = serverTransport.TLSClientConfig.RootCAs
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func TestSignedDownloadNamesTheStorageHostThatRefusesIt(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/attachments/gone" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.Redirect(w, r, storage.URL+"/signed?X-Amz-Signature=bad", http.StatusSeeOther)
	}))
	defer server.Close()
	client := New(server.URL, "", false)
	storageResponse, err := client.DownloadSigned(context.Background(), server.URL+"/attachments/x")
	storageURL, _ := url.Parse(storage.URL)
	require.EqualError(t, err, "storage host "+storageURL.Host+" refused the signed download with HTTP 403; the signed URL may have expired, so try again")
	if storageResponse != nil {
		defer storageResponse.Body.Close()
	}
	var failure *APIError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, http.StatusForbidden, failure.Status)
	// The instance's own refusal keeps its wording.
	instanceResponse, err := client.DownloadSigned(context.Background(), server.URL+"/attachments/gone")
	require.EqualError(t, err, "permission denied by the Gitea instance")
	if instanceResponse != nil {
		defer instanceResponse.Body.Close()
	}
}

func TestConcurrentRequestsKeepTheirConnections(t *testing.T) {
	const inFlight = 8
	var opened atomic.Int32
	var arrived sync.WaitGroup
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		arrived.Done()
		arrived.Wait()
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client := New(server.URL, "", false)
	for range 2 {
		arrived.Add(inFlight)
		var requests sync.WaitGroup
		for range inFlight {
			requests.Go(func() {
				response, err := client.HTTP.Get(server.URL)
				if assert.NoError(t, err) {
					_ = response.Body.Close()
				}
			})
		}
		requests.Wait()
	}
	require.Equal(t, int32(inFlight), opened.Load())
}
