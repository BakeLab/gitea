// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	packages_model "gitea.dev/models/packages"
	"regexp"
)

// Versions arrive newest first. Unmatched versions retain the historical shared
// bucket; matched groups cannot consume one another's keep budget.
func cleanupCandidates(versions []*packages_model.PackageVersion, keep int, prefix string, pattern *regexp.Regexp) []*packages_model.PackageVersion {
	if keep <= 0 {
		return versions
	}
	counts := map[string]int{}
	candidates := make([]*packages_model.PackageVersion, 0, len(versions))
	for _, version := range versions {
		group := ""
		if pattern != nil {
			if match := pattern.FindStringSubmatch(prefix + version.LowerVersion); len(match) == 2 {
				group = "matched:" + match[1]
			}
		}
		counts[group]++
		if counts[group] > keep {
			candidates = append(candidates, version)
		}
	}
	return candidates
}
