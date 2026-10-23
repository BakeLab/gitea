// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateRepoAsksForTheSettings(t *testing.T) {
	choices := RepoFormChoices{
		Owners: []string{"alice", "acme"}, Licenses: []string{"Apache-2.0", "MIT"}, Gitignores: []string{"Go", "Node"},
		ValidateName: func(name string) error {
			if strings.Contains(name, " ") {
				return errors.New("no spaces")
			}
			return nil
		},
	}
	p, rendered := linePrompter(t, "2\nbad name\ntool\nA tool\n2\ny\n3\n1\n2\n0\ny\n")
	values, err := p.CreateRepo(context.Background(), choices)
	require.NoError(t, err)
	require.Equal(t, RepoFormValues{Owner: "acme", Name: "tool", Description: "A tool", Private: true, AutoInit: true, License: "MIT", Gitignores: []string{"Go", "Node"}, Clone: true}, values)
	require.Contains(t, rendered.String(), "no spaces")

	// Without initial content, only cloning is asked after the first questions.
	p, _ = linePrompter(t, "1\ntool\n\n1\nn\nn\n")
	values, err = p.CreateRepo(context.Background(), choices)
	require.NoError(t, err)
	require.Equal(t, RepoFormValues{Owner: "alice", Name: "tool"}, values)
}
