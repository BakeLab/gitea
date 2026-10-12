// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelFormStartsFromCurrentValues(t *testing.T) {
	p, _ := testPrompter(strings.NewReader("\r\r\r\r\r"))
	current := LabelFormValues{Name: "bug", Color: "#EE0701", Description: "Broken", Exclusive: true}
	values, err := p.EditLabel(context.Background(), `Editing label "bug" in alice/x`, current)
	require.NoError(t, err)
	require.Equal(t, LabelFormValues{Name: "bug", Color: "EE0701", Description: "Broken", Exclusive: true}, values)
}

func TestLabelFormMarksNameRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "bug\nee0701\n\nn\nn\n")
	values, err := p.EditLabel(context.Background(), "", LabelFormValues{})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Label name (required):")
	require.Equal(t, "bug", values.Name)
}
