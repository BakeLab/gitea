// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseFormAsksForEveryField(t *testing.T) {
	p, rendered := linePrompter(t, "v1.0\n\nShip it\n.\nmain\ny\nn\n")
	values, err := p.EditRelease(context.Background(), "", ReleaseFormValues{})
	require.NoError(t, err)
	require.Equal(t, ReleaseFormValues{Tag: "v1.0", Notes: "Ship it", Target: "main", Draft: true}, values)
	require.Contains(t, rendered.String(), "Tag (required):")
}

func TestReleaseFormKeepsCurrentValuesOnEnter(t *testing.T) {
	current := ReleaseFormValues{Tag: "v1", Title: "One", Notes: "a\r\nb\tc", Target: "main", Draft: true, Prerelease: true}
	p, _ := linePrompter(t, "\n\n\n\n\n\n")
	values, err := p.EditRelease(context.Background(), "Editing release v1 in alice/x", current)
	require.NoError(t, err)
	require.Equal(t, current, values)
}
