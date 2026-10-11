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
	const head, tracking = "symbolic-ref --short HEAD", "config --get branch.main.remote"
	for _, test := range []struct {
		name      string
		runner    discoverRunner
		preferred string
		want      string
		wantErr   error
	}{
		{name: "preferred", preferred: "fork", want: "fork"},
		{name: "branch upstream", runner: discoverRunner{head: "main", tracking: "fork", "remote": "origin\nfork"}, want: "fork"},
		{name: "branch upstream with slashes", runner: discoverRunner{head: "main", tracking: "pulls/bob/project", "remote": "origin\npulls/bob/project"}, want: "pulls/bob/project"},
		{name: "local branch upstream", runner: discoverRunner{head: "main", tracking: ".", "remote": "origin\nfork"}, want: "origin"},
		{name: "upstream remote", runner: discoverRunner{"remote": "origin\nupstream"}, want: "upstream"},
		{name: "origin remote", runner: discoverRunner{"remote": "fork\norigin"}, want: "origin"},
		{name: "sole remote", runner: discoverRunner{"remote": "fork"}, want: "fork"},
		{name: "ambiguous", runner: discoverRunner{"remote": "one\ntwo"}, wantErr: ErrAmbiguousRemote},
		{name: "no remotes", runner: discoverRunner{"remote": ""}, wantErr: ErrAmbiguousRemote},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote, err := DiscoverRemote(context.Background(), test.runner, "/work", test.preferred)
			require.ErrorIs(t, err, test.wantErr)
			require.Equal(t, test.want, remote)
		})
	}
	_, err := DiscoverRemote(context.Background(), discoverRunner{}, "/work", "")
	require.Error(t, err)
}
