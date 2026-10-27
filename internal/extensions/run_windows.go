// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package extensions

import (
	"os"
	"os/signal"
)

// relaySignals keeps gt alive while the extension handles Ctrl-C, which Windows delivers to every
// process of the console.
func relaySignals(*os.Process) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	return func() { signal.Stop(signals) }
}

func exitCode(state *os.ProcessState) int { return state.ExitCode() }
