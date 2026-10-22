// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/transport"
)

// explainedError replaces the message of err and keeps err reachable for exit codes.
type explainedError struct {
	text string
	err  error
}

func (e *explainedError) Error() string { return e.text }
func (e *explainedError) Unwrap() error { return e.err }

// explain gives err the message text.
func explain(err error, format string, args ...any) error {
	return &explainedError{text: fmt.Sprintf(format, args...), err: err}
}

// Explain words a command failure for people. It adds the next step to refusals Gitea explains,
// and words rejected credentials, unreachable servers and expired timeouts. Exit codes are
// unchanged.
func Explain(ctx context.Context, deps Dependencies, err error) error {
	if err == nil || errors.As(err, new(*explainedError)) {
		return err
	}
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
		return explainAPIError(deps, err, apiErr)
	}
	if failure, ok := errors.AsType[*transport.RequestFailure](err); ok {
		// Go reports a proxy it cannot connect to as a proxyconnect error around the dial error.
		if proxy, ok := errors.AsType[*net.OpError](failure.Err); ok && proxy.Op == "proxyconnect" {
			if reason := unreachable(proxy.Err); reason != "" {
				name := "the configured proxy"
				if dial, ok := errors.AsType[*net.OpError](proxy.Err); ok && dial.Addr != nil {
					name = "the proxy " + dial.Addr.String()
				}
				return explain(err, "cannot reach %s (%s); check HTTPS_PROXY and HTTP_PROXY", name, reason)
			}
		}
		if reason := unreachable(failure.Err); reason != "" && failure.Server != "" {
			return explain(err, "cannot reach %s (%s); check --host or your network", failure.Server, reason)
		}
	}
	if errors.Is(err, transport.ErrTimeout) {
		return httpTimeoutError(deps, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return explain(err, "timed out before the operation finished")
	}
	return err
}

// httpTimeoutError words a request that ran out of http_timeout, which an expired --timeout is
// not: the server that did not respond, within how long, and how to wait longer.
func httpTimeoutError(deps Dependencies, err error) error {
	server := "the instance"
	if failure, ok := errors.AsType[*transport.RequestFailure](err); ok && failure.Server != "" {
		server = failure.Server
	}
	within, raise := "http_timeout", "raise it with: gt config set http_timeout DURATION"
	if deps.Settings != nil && deps.Getenv != nil {
		if timeout := (&application{deps: deps}).responseTimeout(); timeout > 0 {
			within += " (" + shortDuration(timeout) + ")"
		}
		if strings.TrimSpace(deps.Getenv("GITEA_CLI_HTTP_TIMEOUT")) != "" {
			raise = "raise GITEA_CLI_HTTP_TIMEOUT"
		}
	}
	return explain(err, "%s did not respond within %s; %s", server, within, raise)
}

func explainAPIError(deps Dependencies, err error, apiErr *transport.APIError) error {
	if !explainable(err, apiErr) {
		return err
	}
	if apiErr.Status == http.StatusUnauthorized {
		instance := cmp.Or(apiErr.Instance, "the instance")
		if deps.Getenv != nil && deps.Getenv("GITEA_TOKEN") != "" {
			return explain(err, "GITEA_TOKEN was rejected by %s; fix or unset GITEA_TOKEN", instance)
		}
		return explain(err, "authentication rejected by %s; run gt auth login", instance)
	}
	// Gitea names the scopes a token lacks, such as read:user, in the message of a 403.
	if apiErr.Status == http.StatusForbidden && strings.Contains(apiErr.Message, "required scope") {
		if deps.Getenv != nil && deps.Getenv("GITEA_TOKEN") != "" {
			return explain(err, "%s; set GITEA_TOKEN to a token with that scope", apiErr.Message)
		}
		token := "create a token with that scope and log in with it: gt auth login --with-token"
		switch oauth, other := instanceAuthMethods(deps, apiErr.Instance); {
		case oauth && other:
			return explain(err, "%s; %s, or for an OAuth account: %s", apiErr.Message, token, oauthScopeLogin(apiErr))
		case oauth:
			return explain(err, "%s; log in again with that scope: %s", apiErr.Message, oauthScopeLogin(apiErr))
		}
		return explain(err, "%s; %s", apiErr.Message, token)
	}
	if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Hint == "" {
		if hint := conflictHint(apiErr, conflict); hint != "" {
			return explain(err, "%s; %s", conflict.Error(), hint)
		}
	}
	return err
}

// instanceAuthMethods reports whether the accounts of instance include OAuth accounts and others.
// Explain does not know which account a command used, so a hint must suit all of them.
func instanceAuthMethods(deps Dependencies, instance string) (oauth, other bool) {
	if deps.Settings == nil {
		return false, false
	}
	s, err := deps.Settings.Load()
	if err != nil {
		return false, false
	}
	for _, account := range s.Hosts.Hosts[instance].Users {
		if account.AuthMethod == settings.AuthOAuth {
			oauth = true
		} else {
			other = true
		}
	}
	return oauth, other
}

// oauthScopeLogin is the login that grants an OAuth account the scopes a 403 names, such as
// "token does not have at least one of required scope(s), required=[write:user], token scope=...".
func oauthScopeLogin(apiErr *transport.APIError) string {
	scopes := "SCOPES"
	if _, rest, ok := strings.Cut(apiErr.Message, "required=["); ok {
		if required, _, ok := strings.Cut(rest, "]"); ok && required != "" {
			scopes = strings.Join(strings.Fields(required), ",")
		}
	}
	return "gt auth login --host " + apiErr.Instance + " --oauth --scopes " + scopes
}

// explainable reports whether err says no more than the failed request: its text is the API
// error's or a classification of it. Context a caller added ("release deleted, but tag
// deletion failed: ...") is kept, and so are raw responses, such as those of gt api, which
// were not recorded from one of gt's requests.
func explainable(err error, apiErr *transport.APIError) bool {
	if apiErr.Instance == "" {
		return false
	}
	text := err.Error()
	if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Error() == text {
		return true
	}
	if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Error() == text {
		return true
	}
	return apiErr.Error() == text
}

// conflictHint returns the next step for a refused request, or "".
func conflictHint(apiErr *transport.APIError, conflict *services.ConflictError) string {
	number := strings.TrimPrefix(conflict.Key, "#")
	switch conflict.Reason {
	case services.ReasonArchived:
		if conflict.Scope != "" {
			return "unarchive it with: gt repo unarchive " + conflict.Scope
		}
	case services.ReasonMerged:
		if apiErr.Method == http.MethodPatch {
			return "its state can no longer change"
		}
	case services.ReasonDraft:
		if number != conflict.Key {
			return "mark it ready with: gt pr ready " + number
		}
	case services.ReasonOwnReview:
		return "leave a comment instead"
	case services.ReasonNoChanges:
		return "check out your feature branch or pass --head"
	case services.ReasonDisabled:
		if conflict.Key != "" {
			return "enable it with: gt workflow enable " + conflict.Key
		}
	case services.ReasonNotDone:
		if conflict.Kind == "run" && conflict.Key != "" {
			return "cancel it first: gt run cancel " + conflict.Key
		}
	case services.ReasonOwnsRepos:
		return "transfer them with gt repo transfer or delete them first"
	}
	return ""
}

// shellQuote quotes s as one argument of a suggested command line, in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// unreachable words a failure to connect, or returns "" for other request failures.
func unreachable(err error) string {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return "no such host"
	case errors.As(err, &opErr) && opErr.Op == "dial" && opErr.Timeout():
		return "connection timed out"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return opErr.Err.Error()
	}
	return ""
}

// expired reports whether err is the expiry of the command's own deadline, such as --timeout,
// rather than a request that ran out of http_timeout.
func expired(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, transport.ErrTimeout)
}

// timeoutError words an expired --timeout as "timed out after 4s waiting for run 34" instead of
// "context deadline exceeded", keeping the exit code. Other errors are returned unchanged.
func timeoutError(err error, d time.Duration, format string, args ...any) error {
	if !expired(err) {
		return err
	}
	text := "timed out after " + shortDuration(d)
	if format != "" {
		text += " " + fmt.Sprintf(format, args...)
	}
	return explain(err, "%s", text)
}

// shortDuration formats d without zero units, such as 1h, 1h30m or 45s.
func shortDuration(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}
