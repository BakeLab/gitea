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
) (*url.URL, error) {
	// Normalize URL
	serverURL, err := NormalizeURL(giteaURL)
	if err != nil {
		return nil, fmt.Errorf("unable to parse URL: %s", err)
	}

	// .. if we have enough information to authenticate
	if len(token) == 0 && (len(user)+len(passwd)) == 0 {
		return nil, fmt.Errorf("no token set")
	} else if len(user) != 0 && len(passwd) == 0 {
		return nil, fmt.Errorf("no password set")
	} else if len(user) == 0 && len(passwd) != 0 {
		return nil, fmt.Errorf("no user set")
	}

	return serverURL, nil
}
