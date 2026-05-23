// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"fmt"
	"net/url"
)

// GetRemote tries to match a Remote of the repo via the given URL.
// Matching is based on the normalized URL, accepting different protocols.
func (r TeaRepo) GetRemote(remoteURL string) (*Remote, error) {
	repoURL, err := ParseURL(remoteURL)
	if err != nil {
		return nil, err
	}

	remotes, err := r.Remotes()
	if err != nil {
		return nil, err
	}
	for _, remote := range remotes {
		for _, u := range remote.Config().URLs {
			parsedRemoteURL, err := ParseURL(u)
			if err != nil {
				return nil, err
			}
			if parsedRemoteURL.Host == repoURL.Host && parsedRemoteURL.Path == repoURL.Path {
				return remote, nil
			}
		}
	}

	return nil, nil
}

// GetOrCreateRemote tries to match a Remote of the repo via the given URL.
// If no match is found, a new Remote with `newRemoteName` is created.
// Matching is based on the normalized URL, accepting different protocols.
func (r TeaRepo) GetOrCreateRemote(remoteURL, newRemoteName string) (*Remote, error) {
	localRemote, err := r.GetRemote(remoteURL)
	if err != nil {
		return nil, err
	}

	if localRemote == nil {
		if err := r.AddRemote(newRemoteName, remoteURL); err != nil {
			return nil, err
		}
		return r.Remote(newRemoteName)
	}

	return localRemote, nil
}

// TeaRemoteURL returns the first url entry for the given remote name.
func (r TeaRepo) TeaRemoteURL(name string) (auth *url.URL, err error) {
	remote, err := r.Remote(name)
	if err != nil {
		return nil, err
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return nil, fmt.Errorf("remote %s has no URL configured", name)
	}
	return ParseURL(urls[0])
}
