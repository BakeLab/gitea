// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package terminal

import "golang.org/x/sys/unix"

// hideInput stops the terminal at fd from showing what is typed, keeping its line editing and
// Ctrl-C, until the returned function runs.
func hideInput(fd uintptr) (func(), error) {
	shown, err := unix.IoctlGetTermios(int(fd), ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	hidden := *shown
	hidden.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(int(fd), ioctlSetTermios, &hidden); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(int(fd), ioctlSetTermios, shown) }, nil
}
