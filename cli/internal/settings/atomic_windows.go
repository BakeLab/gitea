// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"errors"
	"os"
	"slices"
	"time"

	"golang.org/x/sys/windows"
)

// retry runs op until it stops failing with one of the transient errors, which Windows reports for
// as long as another process, such as a concurrent gt, holds or replaces the file.
func retry(op func() error, transient ...error) error {
	deadline := time.Now().Add(2 * time.Second)
	for delay := time.Millisecond; ; delay = min(2*delay, 100*time.Millisecond) {
		err := op()
		isTransient := slices.ContainsFunc(transient, func(target error) bool { return errors.Is(err, target) })
		if err == nil || !isTransient || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
	}
}

func rename(oldpath, newpath string) error {
	return retry(func() error { return os.Rename(oldpath, newpath) }, windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION)
}

// ReadFile reads a file that AtomicWrite may be replacing, which Windows refuses until it is done.
func ReadFile(name string) ([]byte, error) {
	var data []byte
	err := retry(func() (err error) {
		data, err = os.ReadFile(name)
		return err
	}, windows.ERROR_SHARING_VIOLATION)
	return data, err
}
