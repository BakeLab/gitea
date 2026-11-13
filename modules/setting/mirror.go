// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"regexp"
	"time"

	"gitea.dev/modules/log"
)

// Mirror settings
var Mirror = struct {
	Enabled         bool
	DisableNewPull  bool
	DisableNewPush  bool
	DefaultInterval time.Duration
	MinInterval     time.Duration
}{
	Enabled:         true,
	DisableNewPull:  false,
	DisableNewPush:  false,
	MinInterval:     10 * time.Minute,
	DefaultInterval: 8 * time.Hour,
}

// GitHubOrgSync contains settings for periodically discovering GitHub organization repositories
// and creating private pull mirrors for them.
var GitHubOrgSync = struct {
	Enabled         bool          `ini:"GITHUB_ORG_SYNC_ENABLED"`
	GitHubOwner     string        `ini:"GITHUB_ORG_SYNC_GITHUB_OWNER"`
	GiteaOwner      string        `ini:"GITHUB_ORG_SYNC_GITEA_OWNER"`
	Token           string        `ini:"GITHUB_ORG_SYNC_TOKEN"`
	MirrorInterval  time.Duration `ini:"GITHUB_ORG_SYNC_MIRROR_INTERVAL"`
	ActionsInterval time.Duration `ini:"GITHUB_ORG_SYNC_ACTIONS_INTERVAL"`
}{
	MirrorInterval:  time.Hour,
	ActionsInterval: 10 * time.Minute,
}

func loadMirrorFrom(rootCfg ConfigProvider) {
	section := rootCfg.Section("mirror")
	if err := section.MapTo(&Mirror); err != nil {
		log.Fatal("Failed to map Mirror settings: %v", err)
	}
	if err := section.MapTo(&GitHubOrgSync); err != nil {
		log.Fatal("Failed to map GitHub organization mirror settings: %v", err)
	}
	GitHubOrgSync.Token = loadSecret(section, "GITHUB_ORG_SYNC_TOKEN_URI", "GITHUB_ORG_SYNC_TOKEN")

	if !Mirror.Enabled {
		Mirror.DisableNewPull = true
		Mirror.DisableNewPush = true
	}

	if Mirror.MinInterval.Minutes() < 1 {
		log.Warn("Mirror.MinInterval is too low, set to 1 minute")
		Mirror.MinInterval = 1 * time.Minute
	}
	if Mirror.DefaultInterval < Mirror.MinInterval {
		Mirror.DefaultInterval = max(time.Hour*8, Mirror.MinInterval)
		log.Warn("Mirror.DefaultInterval is less than Mirror.MinInterval, set to %s", Mirror.DefaultInterval.String())
	}

	if GitHubOrgSync.Enabled {
		if !Mirror.Enabled || Mirror.DisableNewPull {
			log.Fatal("Mirror.GITHUB_ORG_SYNC_ENABLED requires mirror.ENABLED and DISABLE_NEW_PULL=false")
		}
		validGitHubOwner := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
		validGiteaOwner := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,39}$`)
		if !validGitHubOwner.MatchString(GitHubOrgSync.GitHubOwner) || !validGiteaOwner.MatchString(GitHubOrgSync.GiteaOwner) {
			log.Fatal("Mirror.GITHUB_ORG_SYNC_ENABLED requires valid GITHUB_ORG_SYNC_GITHUB_OWNER and GITHUB_ORG_SYNC_GITEA_OWNER")
		}
		if GitHubOrgSync.Token == "" {
			log.Fatal("Mirror.GITHUB_ORG_SYNC_ENABLED requires GITHUB_ORG_SYNC_TOKEN or GITHUB_ORG_SYNC_TOKEN_URI")
		}
		if GitHubOrgSync.MirrorInterval < Mirror.MinInterval || GitHubOrgSync.ActionsInterval < Mirror.MinInterval {
			log.Fatal("GitHub organization mirror intervals must be at least %s", Mirror.MinInterval)
		}
	}
}
