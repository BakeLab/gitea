// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"fmt"
	"strings"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/debug"
	"code.gitea.io/tea/modules/git"
)

// MatchLogins matches the given remoteURL against the provided logins and returns
// the first matching login
// remoteURL could be like:
//
//	https://gitea.com/owner/repo.git
//	http://gitea.com/owner/repo.git
//	ssh://gitea.com/owner/repo.git
//	git@gitea.com:owner/repo.git
func MatchLogins(remoteURL string, logins []config.Login) (*config.Login, string, error) {
	for _, l := range logins {
		debug.Printf("Matching remote URL '%s' against %v login", remoteURL, l)
		sshHost := l.GetSSHHost()
		atIdx := strings.Index(remoteURL, "@")
		colonIdx := strings.Index(remoteURL, ":")
		if atIdx > 0 && colonIdx > atIdx {
			domain := remoteURL[atIdx+1 : colonIdx]
			if domain == sshHost {
				return &l, strings.TrimSuffix(remoteURL[colonIdx+1:], ".git"), nil
			}
		} else {
			p, err := git.ParseURL(remoteURL)
			if err != nil {
				return nil, "", fmt.Errorf("git remote URL parse failed: %s", err.Error())
			}

			switch {
			case strings.EqualFold(p.Scheme, "http") || strings.EqualFold(p.Scheme, "https"):
				if strings.HasPrefix(remoteURL, l.URL) {
					ps := strings.Split(p.Path, "/")
					path := strings.Join(ps[len(ps)-2:], "/")
					return &l, strings.TrimSuffix(path, ".git"), nil
				}
			case strings.EqualFold(p.Scheme, "ssh"):
				if sshHost == p.Host || sshHost == p.Hostname() {
					return &l, strings.TrimLeft(p.Path, "/"), nil
				}
			default:
				// unknown scheme
				return nil, "", fmt.Errorf("git remote URL parse failed: %s", "unknown scheme "+p.Scheme)
			}
		}
	}
	return nil, "", errNotAGiteaRepo
}
