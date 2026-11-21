// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"errors"
	"strings"

	"github.com/kballard/go-shellquote"
)

// SSHArguments selects a private key for one Git operation. Agent selection
// retains the user's SSH configuration, as does an account without a key.
func SSHArguments(key string, agent bool) ([]string, error) {
	if strings.ContainsAny(key, "\x00\r\n") {
		return nil, errors.New("SSH key path cannot contain NUL or line breaks")
	}
	if agent || key == "" {
		return nil, nil
	}
	command := shellquote.Join("ssh", "-i", key, "-oIdentitiesOnly=yes", "-oIdentityAgent=none")
	return []string{"-c", "core.sshCommand=" + command}, nil
}
