// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package auth

import (
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

// getAgent connects to the ssh-agent at SSH_AUTH_SOCK; release closes the connection.
func getAgent() (client agent.Agent, release func(), err error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, nil, errors.New("no ssh-agent available")
	}
	if _, err := os.Stat(socket); err != nil {
		return nil, nil, errors.New("no ssh-agent available")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, nil, errors.New("cannot connect to ssh-agent")
	}
	return agent.NewClient(conn), func() { _ = conn.Close() }, nil
}
