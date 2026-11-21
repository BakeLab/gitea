// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build windows

package auth

import (
	"errors"

	"github.com/davidmz/go-pageant"
	"golang.org/x/crypto/ssh/agent"
)

// getAgent returns the Pageant client, which holds no connection for release to close.
func getAgent() (client agent.Agent, release func(), err error) {
	if !pageant.Available() {
		return nil, nil, errors.New("no pageant available")
	}
	return pageant.New(), func() {}, nil
}
