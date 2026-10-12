// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitea.dev/cli/internal/settings"
)

// GitRequest contains only the credential-routing parts of Git's input protocol.
type GitRequest struct{ Protocol, Host, Path, Username string }

// ReadGitRequest limits input and rejects ambiguous routing without echoing values.
func ReadGitRequest(input io.Reader) (GitRequest, error) {
	fields := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(input, 65537))
	scanner.Buffer(make([]byte, 4096), 65537)
	size := 0
	for scanner.Scan() {
		line := scanner.Text()
		size += len(line) + 1
		if size > 65536 {
			return GitRequest{}, errors.New("Git credential request exceeds 64 KiB")
		}
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.ContainsAny(value, "\x00\r") {
			return GitRequest{}, errors.New("invalid Git credential request")
		}
		switch key {
		case "protocol", "host", "path", "username", "url":
			if _, exists := fields[key]; exists {
				return GitRequest{}, errors.New("duplicate Git credential routing field")
			}
			fields[key] = value
		}
	}
	if scanner.Err() != nil {
		return GitRequest{}, errors.New("cannot read Git credential request")
	}
	if raw, exists := fields["url"]; exists {
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return GitRequest{}, errors.New("invalid Git credential URL")
		}
		if u.User != nil {
			if _, present := u.User.Password(); present {
				return GitRequest{}, errors.New("Git credential URL must not contain a password")
			}
			username := u.User.Username()
			if strings.ContainsAny(username, "\x00\r\n") {
				return GitRequest{}, errors.New("invalid Git credential username")
			}
			if previous, exists := fields["username"]; exists && previous != username {
				return GitRequest{}, errors.New("conflicting Git credential usernames")
			}
			fields["username"] = username
		}
		values := map[string]string{"protocol": u.Scheme, "host": u.Host, "path": strings.TrimPrefix(u.EscapedPath(), "/")}
		for key, value := range values {
			if previous, exists := fields[key]; exists && previous != value {
				return GitRequest{}, errors.New("conflicting Git credential URL fields")
			}
			fields[key] = value
		}
	}
	return GitRequest{fields["protocol"], fields["host"], fields["path"], fields["username"]}, nil
}

// MatchGitInstance selects the most specific configured installation. A pinned
// helper declines a request belonging to a different installation on that origin.
func MatchGitInstance(request GitRequest, hosts map[string]settings.Host, explicit, environment string) (string, error) {
	if request.Protocol != "http" && request.Protocol != "https" {
		return "", nil
	}
	origin, err := settings.NormalizeHost(request.Protocol + "://" + request.Host)
	if err != nil {
		return "", errors.New("invalid Git credential origin")
	}
	parsedOrigin, _ := url.Parse(origin)
	if parsedOrigin.Path != "" {
		return "", errors.New("invalid Git credential host")
	}
	if strings.Contains(strings.ToLower(request.Path), "%2f") || strings.Contains(strings.ToLower(request.Path), "%5c") {
		return "", errors.New("ambiguous Git credential path")
	}
	decoded, err := url.PathUnescape(request.Path)
	if err != nil || strings.ContainsAny(decoded, "\\\x00\r\n") {
		return "", errors.New("invalid Git credential path")
	}
	for segment := range strings.SplitSeq(decoded, "/") {
		if segment == "." || segment == ".." || strings.Contains(segment, "%") {
			return "", errors.New("ambiguous Git credential path")
		}
	}
	requestPath := "/" + strings.TrimPrefix(decoded, "/")
	candidates := make(map[string]bool, len(hosts)+2)
	for instance := range hosts {
		candidates[instance] = true
	}
	for _, extra := range []string{explicit, environment} {
		if extra != "" {
			normalized, err := settings.NormalizeHost(extra)
			if err != nil {
				return "", errors.New("invalid configured Git credential instance")
			}
			candidates[normalized] = true
		}
	}
	best, specificity := "", -1
	sameOrigin := 0
	for instance := range candidates {
		u, err := url.Parse(instance)
		if err != nil || u.Scheme+"://"+u.Host != origin {
			continue
		}
		sameOrigin++
		base := strings.TrimSuffix(u.Path, "/")
		if requestPath != base && !strings.HasPrefix(requestPath, base+"/") {
			continue
		}
		if len(base) > specificity {
			best, specificity = instance, len(base)
		}
	}
	// Without useHttpPath, Git omits the path. Multiple installations then cannot
	// be distinguished safely, even if one is installed at the origin root.
	if request.Path == "" && sameOrigin > 1 {
		return "", nil
	}
	if explicit != "" {
		pinned, _ := settings.NormalizeHost(explicit)
		if pinned != best {
			return "", nil
		}
	}
	return best, nil
}

// GitCredential loads or refreshes one explicitly selected stored account.
// It never accepts Git's store/erase protocol as an authentication mutation.
func (m Manager) GitCredential(ctx context.Context, instance, user string, client *http.Client, now time.Time) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	snapshot, err := m.settings.Load()
	if err != nil {
		return Credential{}, err
	}
	account, exists := snapshot.Hosts.Hosts[instance].Users[user]
	if !exists {
		return Credential{}, errors.New("selected Git account is not configured; run gt auth login")
	}
	if account.AuthMethod == settings.AuthOAuth {
		return m.Refresh(ctx, instance, user, client, false, now)
	}
	if account.Tokenless() {
		return Credential{}, errors.New("HTTP signature accounts cannot supply Git HTTPS credentials; use SSH remotes or a token account")
	}
	return m.credentials.Load(instance, user, account.CredentialStore)
}
