// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package transport provides scoped, cancellable Gitea API requests.
package transport

import (
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"gitea.dev/cli/internal/version"
)

// RequestSigner attaches HTTP signature headers for SSH/HTTPSign authentication. contents is the
// body to digest, or nil when there is none or r already carries its SHA-512 Digest header.
type RequestSigner interface {
	SignRequest(r *http.Request, contents []byte) error
}

// ReplayableBody is a request body that can be read again from the start, so that signing can
// hash a large body, such as an upload, as it reads it instead of buffering it.
type ReplayableBody struct {
	io.Reader
	open func() io.Reader
}

// Replayable returns the body that open reads from the start each time it is called.
func Replayable(open func() io.Reader) *ReplayableBody {
	return &ReplayableBody{Reader: open(), open: open}
}

// APIError reports request failure without including credentials or raw response bodies.
// Message holds only the sanitized "message" field of a JSON error body.
type APIError struct {
	Status                    int
	Method, Endpoint, Message string
	// Instance is the installation URL the failed request was sent to, when it was recorded
	// from a request.
	Instance string
	// Storage is the host a download was redirected to, such as signed object storage, when
	// that host rather than the instance answered.
	Storage string
	// Moved is the OWNER/REPO a repository was renamed or transferred to, when the instance
	// redirected a request that changes it and the redirect was not followed.
	Moved string
}

func (e *APIError) Error() string {
	var text string
	switch {
	case e.Moved != "":
		return "repository moved to " + e.Moved + "; address it by its new name to change it"
	case e.Storage != "":
		text = fmt.Sprintf("storage host %s refused the signed download with HTTP %d", e.Storage, e.Status)
		if e.Status == http.StatusForbidden {
			text += "; the signed URL may have expired, so try again"
		}
	case e.Status == 401:
		text = "authentication rejected; run gt auth login"
	case e.Status == 403:
		text = "permission denied by the Gitea instance"
	case e.Status == 404:
		// Gitea's 404 message is usually just "not found"; the request names what was missing.
		text = "not found (" + e.request() + ")"
		if strings.EqualFold(e.Message, "not found") {
			return text
		}
	case e.Status == 429:
		text = "Gitea rate limit exceeded; try again later"
	default:
		text = fmt.Sprintf("Gitea API returned HTTP %d for %s %s", e.Status, e.Method, e.Endpoint)
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// request describes the failed request, such as "HTTP 404 GET /api/v1/repos/alice/x".
func (e *APIError) request() string {
	return strings.TrimSpace(fmt.Sprintf("HTTP %d %s %s", e.Status, e.Method, e.Endpoint))
}

// bind records the instance of the request to u.
func (e *APIError) bind(u *url.URL) {
	if prefix, _, ok := strings.Cut(u.Path, "/api/v1/"); ok {
		e.Instance = (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: prefix}).String()
	}
}

const (
	maxErrorBody    = 4 << 10
	maxErrorMessage = 300
)

// newAPIError keeps only a bounded, printable JSON "message" field of a failed response body,
// with the secrets marked on ctx and the passwords of URLs redacted before it is cut short.
// Authentication failures and messages echoing the token keep no server text.
func newAPIError(ctx context.Context, status int, method, endpoint, token string, data []byte) *APIError {
	failure := &APIError{Status: status, Method: method, Endpoint: endpoint}
	if status == http.StatusUnauthorized {
		return failure
	}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) != nil || (token != "" && strings.Contains(body.Message, token)) {
		return failure
	}
	message := strings.Join(strings.FieldsFunc(RedactSecrets(ctx, body.Message), func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) }), " ")
	if runes := []rune(message); len(runes) > maxErrorMessage {
		message = string(runes[:maxErrorMessage]) + "..."
	}
	failure.Message = message
	return failure
}

// Client binds HTTP requests to one instance and credential.
type Client struct {
	BaseURL string
	Token   string
	// Sudo asks the instance to perform requests as this user (administrators only).
	Sudo    string
	Signer  RequestSigner
	HTTP    *http.Client
	Retries int
}

// UserAgent identifies gt to instances, proxies, and server logs.
var UserAgent = "gt/" + version.Current() + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"

type userAgentTransport struct{ base http.RoundTripper }

func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent)
	}
	return t.base.RoundTrip(req)
}

// New constructs an instance-scoped client. Its HTTP client follows no redirects itself; API
// requests follow only those within the instance API. Only connection setup is bounded here;
// WithResponseTimeout bounds the wait for an answer, which a synchronous migration may need
// minutes to start. insecure skips certificate verification for the instance's host only; other
// hosts, such as the storage a download redirects to, are still verified.
func New(instance, token string, insecure bool) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} // user-controlled, instance-scoped
	var transport http.RoundTripper = tr
	if base, err := url.Parse(instance); insecure && err == nil {
		verified := tr.Clone()
		verified.TLSClientConfig = tr.TLSClientConfig.Clone()
		verified.TLSClientConfig.InsecureSkipVerify = false
		transport = hostTransport{host: base.Host, instance: tr, other: verified}
	}
	return &Client{BaseURL: instance, Token: token, Retries: 2, HTTP: &http.Client{Transport: userAgentTransport{transport}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

// headerTransport adds headers, such as those an access proxy in front of the instance requires,
// to the requests that go to the instance's host, and to no other host. A header the request
// already sets, such as one of gt api -H, is kept.
type headerTransport struct {
	base    http.RoundTripper
	host    string
	headers http.Header
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Host, t.host) {
		added := []string{}
		for name := range t.headers {
			if _, set := req.Header[name]; !set {
				added = append(added, name)
			}
		}
		ctx := context.WithValue(req.Context(), configuredHeaders{}, added)
		// A server or proxy that echoes a configured value, such as an access proxy's secret, gets
		// it redacted from --debug and from errors.
		for _, name := range added {
			ctx = WithSecrets(ctx, slices.DeleteFunc(slices.Clone(t.headers[name]), func(value string) bool { return len(value) < minHeaderSecret })...)
		}
		req = req.Clone(ctx)
		for _, name := range added {
			req.Header[name] = t.headers[name]
		}
	}
	return t.base.RoundTrip(req)
}

// minHeaderSecret is the length below which a configured header value, such as "1" or "acme", is
// not redacted: it is too short to be a credential, and redacting it would garble messages.
const minHeaderSecret = 8

type configuredHeaders struct{}

// ConfiguredHeaders returns the names of the headers the instance's configuration added to req,
// such as the request of a response, whose values may be secrets read from the environment.
func ConfiguredHeaders(req *http.Request) []string {
	names, _ := req.Context().Value(configuredHeaders{}).([]string)
	return names
}

// WithHeaders returns a client that adds headers to every request to its instance.
func (c *Client) WithHeaders(headers http.Header) *Client {
	base, err := url.Parse(c.BaseURL)
	if len(headers) == 0 || err != nil {
		return c
	}
	clone := *c
	httpClient := *c.HTTP
	httpClient.Transport = headerTransport{base: cmp.Or[http.RoundTripper](c.HTTP.Transport, http.DefaultTransport), host: base.Host, headers: headers}
	clone.HTTP = &httpClient
	return &clone
}

// WithClientCertificate returns a client that presents the PEM certificate and key in certFile and
// keyFile, or in certFile alone, when the instance asks for one in the TLS handshake; other hosts
// never see it. The files are read at the first handshake, so an unreadable pair fails the request
// that needs it. A client whose transport cannot present one fails every request instead.
func (c *Client) WithClientCertificate(certFile, keyFile string) *Client {
	if certFile == "" {
		return c
	}
	load := sync.OnceValues(func() (*tls.Certificate, error) { return LoadClientCertificate(certFile, keyFile) })
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return c
	}
	transport, ok := replaceHTTPTransport(c.HTTP.Transport, func(tr *http.Transport) http.RoundTripper {
		instance := tr.Clone()
		instance.TLSClientConfig = instance.TLSClientConfig.Clone()
		if instance.TLSClientConfig == nil {
			instance.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		instance.TLSClientConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return load() }
		return hostTransport{host: base.Host, instance: instance, other: tr}
	})
	if !ok {
		transport = failingTransport{fmt.Errorf("cannot present the client certificate %s: the HTTP transport cannot be configured", certFile)}
	}
	clone := *c
	httpClient := *c.HTTP
	httpClient.Transport = transport
	clone.HTTP = &httpClient
	return &clone
}

// replaceHTTPTransport returns rt with the *http.Transport it wraps, through the wrappers of this
// package, replaced by what replace returns for it, and false when rt wraps none. Of a
// hostTransport, only the instance's side is replaced.
func replaceHTTPTransport(rt http.RoundTripper, replace func(*http.Transport) http.RoundTripper) (http.RoundTripper, bool) {
	var ok bool
	switch t := rt.(type) {
	case *http.Transport:
		return replace(t), true
	case userAgentTransport:
		t.base, ok = replaceHTTPTransport(t.base, replace)
		return t, ok
	case headerTransport:
		t.base, ok = replaceHTTPTransport(t.base, replace)
		return t, ok
	case responseTimeout:
		t.base, ok = replaceHTTPTransport(t.base, replace)
		return t, ok
	case debugTransport:
		t.base, ok = replaceHTTPTransport(t.base, replace)
		return t, ok
	case hostTransport:
		t.instance, ok = replaceHTTPTransport(t.instance, replace)
		return t, ok
	}
	return rt, false
}

// failingTransport fails every request with err, such as a client certificate it cannot present.
type failingTransport struct{ err error }

func (t failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, t.err
}

// LoadClientCertificate loads the PEM certificate in certFile with the key in keyFile, or in
// certFile when keyFile is empty.
func LoadClientCertificate(certFile, keyFile string) (*tls.Certificate, error) {
	keyFile = cmp.Or(keyFile, certFile)
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("cannot load the client certificate %s with key %s: %w", certFile, keyFile, err)
	}
	return &certificate, nil
}

// hostTransport sends the requests to the instance's host through instance and all others, such as
// to the storage a download redirects to, through other, so that only the instance sees what
// instance presents, such as a client certificate, and only the instance is exempt from
// certificate verification.
type hostTransport struct {
	host            string
	instance, other http.RoundTripper
}

func (t hostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.EqualFold(req.URL.Host, t.host) {
		return t.instance.RoundTrip(req)
	}
	return t.other.RoundTrip(req)
}

// WithResponseTimeout returns a client whose requests fail when the instance takes longer than
// timeout to start answering once the request has been sent, or, while a request body is sent,
// to read more of it, or, where the system tells, to receive more of what the connection still
// holds of it, or, once it answers, to send more of the response body; neither a slow but
// progressing upload nor a slow but progressing body, such as a download, is cut off. Zero waits
// as long as it takes.
func (c *Client) WithResponseTimeout(timeout time.Duration) *Client {
	if timeout <= 0 {
		return c
	}
	clone := *c
	httpClient := *c.HTTP
	httpClient.Transport = responseTimeout{base: cmp.Or[http.RoundTripper](c.HTTP.Transport, http.DefaultTransport), timeout: timeout}
	clone.HTTP = &httpClient
	return &clone
}

type unboundedResponse struct{}

// UnboundedResponse marks ctx for requests whose answer starts only when a long operation is done,
// such as a synchronous migration, so that no response timeout applies to them.
func UnboundedResponse(ctx context.Context) context.Context {
	return context.WithValue(ctx, unboundedResponse{}, true)
}

// ErrTimeout is the cause of a request that ran out of the response timeout: whose answer did
// not start, whose body stopped going out, or whose response stalled. It matches
// context.DeadlineExceeded, so that it exits like a timeout, but tells the request apart from an
// operation whose own deadline, such as a --timeout, expired.
var ErrTimeout error = responseTimeoutError{}

type responseTimeoutError struct{}

func (responseTimeoutError) Error() string        { return context.DeadlineExceeded.Error() }
func (responseTimeoutError) Is(target error) bool { return target == context.DeadlineExceeded }
func (responseTimeoutError) Timeout() bool        { return true }

type responseTimeout struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t responseTimeout) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Context().Value(unboundedResponse{}) != nil {
		return t.base.RoundTrip(req)
	}
	// A request that runs out of time is canceled with the reason as its cause, for --debug.
	ctx, cancelCause := context.WithCancelCause(req.Context())
	cancel := func() { cancelCause(nil) }
	wait := &answerWait{timeout: t.timeout, cancel: cancelCause, done: make(chan struct{})}
	bounded := req.WithContext(ctx)
	// The instance answers only once it has read the body, so while a body, such as a large
	// upload, is being sent, the wait starts over whenever more of it goes out; a peer that stops
	// reading it fails the request as one that does not answer.
	if req.Body != nil && req.Body != http.NoBody {
		wait.sending = true
		// Once the body has been read, what the connection still holds of it goes out at the
		// pace of the link; where the system tells, the wait follows its send queue.
		bounded = bounded.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { wait.use(info.Conn) },
		}))
		bounded.Body = progressBody{req.Body, wait}
		if req.GetBody != nil {
			bounded.GetBody = func() (io.ReadCloser, error) {
				body, err := req.GetBody()
				if err != nil || body == http.NoBody {
					wait.progress(false)
					return body, err
				}
				wait.progress(true)
				return progressBody{body, wait}, nil
			}
		}
	}
	wait.start()
	resp, err := t.base.RoundTrip(bounded)
	if timedOut := wait.stop(); timedOut != nil {
		if err == nil {
			_ = resp.Body.Close()
		}
		cancelCause(timedOut)
		return nil, timedOut
	}
	if err != nil {
		cancel()
		return nil, err
	}
	// The body is read under the request's context, which lives until the body is closed.
	resp.Body = cancelOnClose{idleBody{resp.Body, ctx, t.timeout, cancelCause}, cancel}
	return resp, nil
}

// idleBody fails a read of a response body that waits longer than timeout for data, such as from
// a stalled connection, and cancels its request with why. Only the wait within a read counts, so
// a body that is read slowly, or that arrives slowly but steadily, is not cut off.
type idleBody struct {
	io.ReadCloser
	ctx     context.Context
	timeout time.Duration
	cancel  context.CancelCauseFunc
}

func (b idleBody) Read(p []byte) (int, error) {
	timer := time.AfterFunc(b.timeout, func() {
		b.cancel(fmt.Errorf("no data received within %s: %w", b.timeout, ErrTimeout))
	})
	n, err := b.ReadCloser.Read(p)
	if !timer.Stop() && err != nil {
		if cause := context.Cause(b.ctx); cause != nil {
			err = cause
		}
	}
	return n, err
}

// answerWait cancels a request that makes no progress for timeout: whose body, while sending,
// stops going out, or whose answer has not started once the body has been sent.
type answerWait struct {
	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
	// sending reports whether the body is still being sent.
	sending bool
	// err is why the wait ran out, once it has.
	err     error
	timeout time.Duration
	cancel  context.CancelCauseFunc
	// conn is the connection the request is sent on, and drains counts the times its body was
	// read in full, so that only the latest drain follows the connection.
	conn   net.Conn
	drains int
	// done is closed once the wait has ended.
	done chan struct{}
}

func (w *answerWait) start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer == nil && !w.stopped {
		w.timer = time.AfterFunc(w.timeout, w.expire)
	}
}

// expire cancels the request once the wait has run out.
func (w *answerWait) expire() {
	w.mu.Lock()
	err := w.timedOut()
	w.mu.Unlock()
	w.cancel(err)
}

// timedOut records and returns why the wait ran out. w.mu must be held.
func (w *answerWait) timedOut() error {
	if w.err == nil && w.sending {
		w.err = fmt.Errorf("no progress sending the request within %s: %w", w.timeout, ErrTimeout)
	} else if w.err == nil {
		w.err = fmt.Errorf("no answer within %s: %w", w.timeout, ErrTimeout)
	}
	return w.err
}

// progress starts the wait over, unless it has already run out or ended, and records whether the
// body is still being sent.
func (w *answerWait) progress(sending bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.restart(sending)
}

// restart is progress with w.mu held.
func (w *answerWait) restart(sending bool) {
	w.sending = sending
	if w.timer != nil && !w.stopped && w.timer.Stop() {
		w.timer.Reset(w.timeout)
	}
}

// use records the connection the request is sent on.
func (w *answerWait) use(conn net.Conn) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conn = conn
}

// sent records that the body has been read in full. The connection may still hold much of it,
// up to several megabytes, which a slow link takes long to send, so where the system tells how
// much the connection holds, the body counts as being sent until that is nothing, and the wait
// starts over whenever it goes down. Elsewhere the wait for the answer starts at once. What the
// instance's system has received counts as sent, though the instance may still be reading it
// from its connection, which the wait for the answer then includes.
func (w *answerWait) sent() {
	w.mu.Lock()
	w.drains++
	conn, drain := w.conn, w.drains
	if _, ok := unsentBytes(conn); !ok {
		w.restart(false)
		w.mu.Unlock()
		return
	}
	w.restart(true)
	w.mu.Unlock()
	go w.drain(conn, drain)
}

// drain follows the send queue of conn until it is empty.
func (w *answerWait) drain(conn net.Conn, drain int) {
	tick := time.NewTicker(min(max(w.timeout/4, 10*time.Millisecond), 250*time.Millisecond))
	defer tick.Stop()
	last := -1
	for {
		unsent, ok := unsentBytes(conn)
		w.mu.Lock()
		if w.drains != drain {
			w.mu.Unlock()
			return
		}
		if !ok || unsent == 0 {
			w.restart(false)
		} else if last >= 0 && unsent < last {
			w.restart(true)
		}
		w.mu.Unlock()
		if !ok || unsent == 0 {
			return
		}
		last = unsent
		select {
		case <-tick.C:
		case <-w.done:
			return
		}
	}
}

// stop ends the wait and returns why it had already run out, if it had.
func (w *answerWait) stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped && w.done != nil {
		close(w.done)
	}
	w.stopped = true
	if w.timer != nil && !w.timer.Stop() {
		return w.timedOut()
	}
	return nil
}

// progressBody starts the wait over whenever part of the request body is read, and starts the
// wait for the answer once it has been read in full.
type progressBody struct {
	io.ReadCloser
	wait *answerWait
}

func (b progressBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	switch {
	case err == io.EOF:
		b.wait.sent()
	case n > 0:
		b.wait.progress(true)
	}
	return n, err
}

// unsentBytes returns the bytes of what was written to conn that its peer has not acknowledged,
// and false when the system cannot tell. Tests replace it.
var unsentBytes = func(conn net.Conn) (int, bool) {
	if secure, ok := conn.(*tls.Conn); ok {
		conn = secure.NetConn()
	}
	socket, ok := conn.(syscall.Conn)
	if !ok {
		return 0, false
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, false
	}
	var unsent int
	var queueErr error
	if err := raw.Control(func(fd uintptr) { unsent, queueErr = queuedBytes(fd) }); err != nil || queueErr != nil {
		return 0, false
	}
	return unsent, true
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// scoped parses raw for this instance, rejecting userinfo, fragments, dot
// segments, and backslashes. kind names the reference in error messages.
func (c *Client) scoped(raw, kind string) (*url.URL, *url.URL, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, nil, errors.New("invalid instance URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" {
		return nil, nil, errors.New("invalid " + kind)
	}
	for segment := range strings.SplitSeq(u.Path, "/") {
		if segment == ".." || segment == "." {
			return nil, nil, errors.New(kind + " cannot contain path traversal")
		}
	}
	if strings.Contains(u.Path, "\\") {
		return nil, nil, errors.New(kind + " cannot contain backslashes")
	}
	return base, u, nil
}

// within reports whether u uses the instance scheme and host and lies below its base path plus under.
func within(base, u *url.URL, under string) bool {
	return u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) && strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+under)
}

// URL accepts only endpoints within the selected installation API.
func (c *Client) URL(endpoint string) (string, error) {
	base, u, err := c.scoped(endpoint, "API endpoint")
	if err != nil {
		return "", err
	}
	if u.IsAbs() || u.Host != "" {
		if !within(base, u, "/api/") {
			return "", errors.New("API URL must belong to the selected instance and its API base path")
		}
		return u.String(), nil
	}
	endpointPath := u.Path
	escapedEndpointPath := u.EscapedPath()
	if !strings.HasPrefix(endpointPath, "/api/") {
		endpointPath = "/api/v1/" + strings.TrimLeft(endpointPath, "/")
		escapedEndpointPath = "/api/v1/" + strings.TrimLeft(escapedEndpointPath, "/")
	}
	base.RawPath = strings.TrimRight(base.EscapedPath(), "/") + escapedEndpointPath
	base.Path = strings.TrimRight(base.Path, "/") + endpointPath
	base.RawQuery = u.RawQuery
	return base.String(), nil
}

// RequestFailure reports a request that received no HTTP response, such as a refused
// connection or an untrusted certificate, or whose response broke off.
type RequestFailure struct {
	// Action names what failed, such as "API request failed".
	Action string
	// Server is the scheme and host the request was sent to, without path or query.
	Server string
	// Err is the network or TLS cause.
	Err error
}

func (e *RequestFailure) Error() string { return e.Action + ": " + e.Err.Error() }
func (e *RequestFailure) Unwrap() error { return e.Err }

// RequestError keeps the network or TLS cause of a failed request. The cause of a *url.Error
// omits the request URL, whose query may carry secrets.
func RequestError(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		failure := &RequestFailure{Action: action, Err: urlErr.Err}
		if u, parseErr := url.Parse(urlErr.URL); parseErr == nil {
			failure.Server = (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
		}
		return failure
	}
	return errors.New(action + "; check connectivity and TLS settings")
}

// ResponseError reports a failure to read the body of resp, such as a connection that stalled or
// dropped, as a *RequestFailure, like a request that received no answer.
func ResponseError(ctx context.Context, resp *http.Response, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := &RequestFailure{Action: "reading the API response failed", Err: err}
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		failure.Server = (&url.URL{Scheme: resp.Request.URL.Scheme, Host: resp.Request.URL.Host}).String()
	}
	return failure
}

// Do executes a cancellable request, retrying only safe body-free reads.
// A response outside 2xx is closed and returned as an *APIError.
func (c *Client) Do(ctx context.Context, method, endpoint string, body io.Reader, headers http.Header) (*http.Response, error) {
	ctx, failure := CaptureErrors(ctx)
	resp, err := c.Send(ctx, method, endpoint, body, headers)
	if err != nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return resp, err
	}
	_ = resp.Body.Close()
	return nil, failure()
}

// Send is Do without converting responses outside 2xx into errors.
func (c *Client) Send(ctx context.Context, method, endpoint string, body io.Reader, headers http.Header) (*http.Response, error) {
	requestURL, err := c.URL(endpoint)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, errors.New("cannot construct API request")
	}
	if replay, ok := body.(*ReplayableBody); ok {
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(replay.open()), nil }
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := c.API().Do(req)
	if err != nil {
		return nil, RequestError(ctx, "API request failed", err)
	}
	return resp, nil
}
