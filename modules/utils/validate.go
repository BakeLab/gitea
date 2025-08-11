// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package utils

import (
	"fmt"
	"net/url"
)

// ValidateAuthenticationMethod checks the provided authentication method parameters
func ValidateAuthenticationMethod(
	giteaURL string,
	token string,
	user string,
	passwd string,
	sshAgent bool,
	sshKey string,
	sshCertPrincipal string,
) (*url.URL, error) {
	// Normalize URL
	serverURL, err := NormalizeURL(giteaURL)
	if err != nil {
		return nil, fmt.Errorf("Unable to parse URL: %s", err)
	}

	if !sshAgent && sshCertPrincipal == "" && sshKey == "" {
		// .. if we have enough information to authenticate
		if len(token) == 0 && (len(user)+len(passwd)) == 0 {
			return nil, fmt.Errorf("No token set")
		} else if len(user) != 0 && len(passwd) == 0 {
			return nil, fmt.Errorf("No password set")
		} else if len(user) == 0 && len(passwd) != 0 {
			return nil, fmt.Errorf("No user set")
		}
	}
	return serverURL, nil
}
