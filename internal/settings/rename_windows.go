// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// rename retries while another reader, such as a concurrent gt, still has newpath open: Windows
// refuses to replace an open file until it is closed.
func rename(oldpath, newpath string) error {
	deadline := time.Now().Add(2 * time.Second)
	for delay := time.Millisecond; ; delay = min(2*delay, 100*time.Millisecond) {
		err := os.Rename(oldpath, newpath)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
	}
}
