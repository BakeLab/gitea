// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gitea.dev/tea/modules/utils"
	"golang.org/x/crypto/ssh"
)

type pwCallback = func(string) (string, error)

// GetAuthForURL returns backend-agnostic auth settings for git network operations.
func GetAuthForURL(remoteURL *url.URL, authToken, keyFile string, passwordCallback pwCallback) (*AuthMethod, error) {
	switch remoteURL.Scheme {
	case "http", "https":
		return &AuthMethod{Scheme: remoteURL.Scheme, Username: authToken}, nil
	case "ssh":
		if keyFile == "" {
			return &AuthMethod{Scheme: remoteURL.Scheme, Username: remoteURL.User.Username()}, nil
		}
		expandedKeyFile, err := utils.AbsPathWithExpansion(keyFile)
		if err != nil {
			return nil, err
		}
		sshKey, err := os.ReadFile(expandedKeyFile)
		if err != nil {
			return nil, fmt.Errorf("can not read ssh key '%s'", expandedKeyFile)
		}

		auth := &AuthMethod{
			Scheme:   remoteURL.Scheme,
			Username: remoteURL.User.Username(),
			KeyFile:  expandedKeyFile,
		}
		if _, err := ssh.ParsePrivateKey(sshKey); err == nil {
			return auth, nil
		}

		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			if passwordCallback == nil {
				return nil, err
			}
			for i := 0; i < 3; i++ {
				pass, err := passwordCallback(expandedKeyFile)
				if err != nil {
					return nil, err
				}
				if _, err := ssh.ParsePrivateKeyWithPassphrase(sshKey, []byte(pass)); err == nil {
					auth.KeyPassphrase = pass
					return auth, nil
				}
			}
			return nil, err
		}

		return nil, err
	default:
		return nil, fmt.Errorf("don't know how to handle url scheme %v", remoteURL.Scheme)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
