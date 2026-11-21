// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build windows

package terminal

import "golang.org/x/sys/windows"

// hideInput stops the console at fd from showing what is typed, keeping its line editing and
// Ctrl-C, until the returned function runs.
func hideInput(fd uintptr) (func(), error) {
	handle := windows.Handle(fd)
	var shown uint32
	if err := windows.GetConsoleMode(handle, &shown); err != nil {
		return nil, err
	}
	if err := windows.SetConsoleMode(handle, shown&^windows.ENABLE_ECHO_INPUT); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, shown) }, nil
}
