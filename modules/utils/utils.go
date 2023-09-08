// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import (
	"os"

	"golang.org/x/crypto/ssh"
)

// Contains checks containment
func Contains(haystack []string, needle string) bool {
	return IndexOf(haystack, needle) != -1
}

// IndexOf returns the index of first occurrence of needle in haystack
func IndexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}

// IsKeyEncrypted checks if the key is encrypted
func IsKeyEncrypted(sshKey string) (bool, error) {
	priv, err := os.ReadFile(sshKey)
	if err != nil {
		return false, err
	}

	_, err = ssh.ParsePrivateKey(priv)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return true, nil
		}
	}

	return false, err
}
