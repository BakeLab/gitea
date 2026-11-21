// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// SSHEndpoint normalizes a host or host:port, including bracketed IPv6 addresses.
func SSHEndpoint(authority string) (string, error) {
	parsed, err := url.Parse("ssh://" + authority)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(authority, "\\ \t\r\n") {
		return "", errors.New("ssh_host must be a hostname or host:port without user, path, or credentials")
	}
	if strings.HasSuffix(authority, ":") || (strings.Contains(parsed.Hostname(), ":") && net.ParseIP(parsed.Hostname()) == nil) {
		return "", errors.New("ssh_host has an invalid host or port")
	}
	port := parsed.Port()
	if port == "" {
		port = "22"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("SSH port must be between 1 and 65535")
	}
	return net.JoinHostPort(strings.ToLower(parsed.Hostname()), strconv.Itoa(number)), nil
}
