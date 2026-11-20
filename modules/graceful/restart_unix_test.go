// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !windows

package graceful

import "testing"

func TestRestartChildIdentity(t *testing.T) {
	for _, fds := range []string{"", "0", "1"} {
		t.Setenv(listenFDsEnv, fds)
		if isRestartChild(42, "") || isRestartChild(42, "41") || !isRestartChild(42, "42") {
			t.Fatalf("incorrect restart identity with LISTEN_FDS=%q", fds)
		}
	}
}
