// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package settings

// PolicyPath is where the system policy is read from.
func PolicyPath() string { return "/etc/gitea-cli/policy.yml" }
