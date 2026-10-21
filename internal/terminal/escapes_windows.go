// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build windows

package terminal

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableEscapes turns on virtual terminal processing for the console behind file, so that it
// interprets color escape sequences instead of showing them. It reports whether the console does,
// and returns how to restore the console's mode when it changed it, as the console keeps the mode
// after gt exits.
func enableEscapes(file *os.File) (bool, func()) {
	handle := windows.Handle(file.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false, nil
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true, nil
	}
	if windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return false, nil
	}
	return true, func() { _ = windows.SetConsoleMode(handle, mode) }
}
