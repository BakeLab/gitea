// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"os"
	"strconv"
	"time"

	"code.gitea.io/tea/modules/config"
)

// GetLoginByEnvVar returns a login based on environment variables, or nil if no login can be created
func GetLoginByEnvVar() *config.Login {
	var token string

	giteaToken := os.Getenv("GITEA_TOKEN")
	githubToken := os.Getenv("GH_TOKEN")
	giteaInstanceURL := os.Getenv("GITEA_INSTANCE_URL")
	giteaInstanceSSHHost := os.Getenv("GITEA_INSTANCE_SSH_HOST")
	instanceInsecure := os.Getenv("GITEA_INSTANCE_INSECURE")
	insecure := false
	if len(instanceInsecure) > 0 {
		insecure, _ = strconv.ParseBool(instanceInsecure)
	}

	// if no tokens are set, or no instance url for gitea fail fast
	if len(giteaInstanceURL) == 0 || (len(giteaToken) == 0 && len(githubToken) == 0) {
		return nil
	}

	token = giteaToken
	if len(giteaToken) == 0 {
		token = githubToken
	}

	return &config.Login{
		Name:              "GITEA_LOGIN_VIA_ENV",
		URL:               giteaInstanceURL,
		Token:             token,
		SSHHost:           giteaInstanceSSHHost,
		Insecure:          insecure,
		SSHKey:            "",
		SSHCertPrincipal:  "",
		SSHKeyFingerprint: "",
		SSHAgent:          false,
		Created:           time.Now().Unix(),
		VersionCheck:      false,
	}
}
