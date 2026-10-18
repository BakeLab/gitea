// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type discoverRunner map[string]string

func (r discoverRunner) Output(_ context.Context, dir string, prompt bool, args ...string) (string, error) {
	value, ok := r[strings.Join(args, " ")]
	if dir != "/work" || prompt || !ok {
		return "", errors.New("missing fake Git result")
	}
	return value, nil
}

func TestDiscoverRemote(t *testing.T) {
	const head, tracking, base = "symbolic-ref --quiet HEAD", "config --get branch.main.remote", "config --get branch.main.gitea-cli-base-remote"
	for _, test := range []struct {
		name      string
		runner    discoverRunner
		preferred string
		want      string
		wantErr   error
	}{
		{name: "preferred", preferred: "fork", want: "fork"},
		{name: "branch upstream", runner: discoverRunner{head: "main", tracking: "fork", "remote": "origin\nfork"}, want: "fork"},
		{name: "branch upstream with slashes", runner: discoverRunner{head: "main", tracking: "team/fork", "remote": "origin\nteam/fork"}, want: "team/fork"},
		{name: "pull head remote", runner: discoverRunner{head: "main", tracking: "pulls/bob/project", "remote": "origin\npulls/bob/project"}, want: "origin"},
		{name: "recorded pull base", runner: discoverRunner{head: "main", tracking: "fork", base: "upstream", "remote": "origin\nupstream\nfork"}, want: "upstream"},
		{name: "removed pull base", runner: discoverRunner{head: "main", tracking: "fork", base: "gone", "remote": "origin\nfork"}, want: "fork"},
		{name: "local branch upstream", runner: discoverRunner{head: "main", tracking: ".", "remote": "origin\nfork"}, want: "origin"},
		{name: "fork clone branch tracking origin", runner: discoverRunner{head: "main", tracking: "origin", "remote": "origin\nupstream"}, want: "upstream"},
		{name: "branch tracking origin without upstream", runner: discoverRunner{head: "main", tracking: "origin", "remote": "origin\nfork"}, want: "origin"},
		{name: "upstream remote", runner: discoverRunner{"remote": "origin\nupstream"}, want: "upstream"},
		{name: "origin remote", runner: discoverRunner{"remote": "fork\norigin"}, want: "origin"},
		{name: "sole remote", runner: discoverRunner{"remote": "fork"}, want: "fork"},
		{name: "ambiguous", runner: discoverRunner{"remote": "one\ntwo"}, wantErr: ErrAmbiguousRemote},
		{name: "no remotes", runner: discoverRunner{"remote": ""}, wantErr: ErrAmbiguousRemote},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote, err := DiscoverRemote(context.Background(), test.runner, "/work", test.preferred, nil)
			require.ErrorIs(t, err, test.wantErr)
			require.Equal(t, test.want, remote)
		})
	}
	_, err := DiscoverRemote(context.Background(), discoverRunner{}, "/work", "", nil)
	require.Error(t, err)
}

func TestDiscoverRemoteSkipsUnusableRemotes(t *testing.T) {
	const head, tracking, base = "symbolic-ref --quiet HEAD", "config --get branch.main.remote", "config --get branch.main.gitea-cli-base-remote"
	// gitea makes each named remote usable; a name=repository pair names its repository.
	gitea := func(names ...string) func(string) string {
		return func(remote string) string {
			for _, name := range names {
				if key, repo, ok := strings.Cut(name, "="); ok && key == remote {
					return repo
				} else if name == remote {
					return "localhost/" + remote
				}
			}
			return ""
		}
	}
	for _, test := range []struct {
		name    string
		runner  discoverRunner
		usable  func(string) string
		want    string
		wantErr error
	}{
		{name: "origin on another forge", runner: discoverRunner{"remote": "gitea\norigin"}, usable: gitea("gitea"), want: "gitea"},
		{name: "no upstream or origin", runner: discoverRunner{"remote": "gitea\ngithub"}, usable: gitea("gitea"), want: "gitea"},
		{name: "usable upstream keeps priority", runner: discoverRunner{"remote": "fork\norigin\nupstream"}, usable: gitea("fork", "upstream"), want: "upstream"},
		{name: "usable origin after unusable upstream", runner: discoverRunner{"remote": "fork\norigin\nupstream"}, usable: gitea("fork", "origin"), want: "origin"},
		{name: "unusable branch upstream", runner: discoverRunner{head: "main", tracking: "github", "remote": "github\norigin"}, usable: gitea("origin"), want: "origin"},
		{name: "usable recorded pull base", runner: discoverRunner{head: "main", tracking: "fork", base: "gitea", "remote": "fork\ngitea\norigin"}, usable: gitea("fork", "gitea"), want: "gitea"},
		{name: "several usable others", runner: discoverRunner{"remote": "a-gitea\norigin\nz-fork"}, usable: gitea("a-gitea", "z-fork"), wantErr: ErrAmbiguousRemote},
		{name: "usable others naming one repository", runner: discoverRunner{"remote": "gitea\ngitea-ssh\norigin"}, usable: gitea("gitea=localhost/alice/project", "gitea-ssh=localhost/alice/project"), want: "gitea"},
		{name: "unusable branch upstream named origin", runner: discoverRunner{head: "main", tracking: "origin", "remote": "gitea\norigin"}, usable: gitea("gitea"), want: "gitea"},
		{name: "pull head remotes never win", runner: discoverRunner{"remote": "gitea\norigin\npulls/bob/project"}, usable: gitea("gitea", "pulls/bob/project"), want: "gitea"},
		{name: "nothing usable keeps the name choice", runner: discoverRunner{"remote": "fork\norigin"}, usable: gitea(), want: "origin"},
		{name: "nothing usable stays ambiguous", runner: discoverRunner{"remote": "one\ntwo"}, usable: gitea(), wantErr: ErrAmbiguousRemote},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote, err := DiscoverRemote(context.Background(), test.runner, "/work", "", test.usable)
			require.ErrorIs(t, err, test.wantErr)
			require.Equal(t, test.want, remote)
		})
	}
}
