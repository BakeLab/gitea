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

func TestLabelFormStartsFromCurrentValues(t *testing.T) {
	validColor := func(s string) error {
		if len(strings.TrimPrefix(s, "#")) != 6 {
			return errors.New("use six hexadecimal digits")
		}
		return nil
	}
	current := LabelFormValues{Name: "bug", Color: "d73a4a", Description: "Something is broken"}
	p, _ := testPrompter(strings.NewReader("\r\r\r"))
	values, err := p.EditLabel(context.Background(), `Editing label "bug" in alice/x`, current, validColor)
	require.NoError(t, err)
	require.Equal(t, current, values)

	p, rendered := linePrompter(t, "\n\n\n")
	values, err = p.EditLabel(context.Background(), "", current, validColor)
	require.NoError(t, err)
	require.Equal(t, current, values, "empty answers keep every value, the description too")
	require.Contains(t, rendered.String(), `Description (optional) [Something is broken; "-" clears]:`)

	p, rendered = linePrompter(t, "\nzz\n#00ff00\n-\n")
	values, err = p.EditLabel(context.Background(), "", current, validColor)
	require.NoError(t, err)
	require.Equal(t, LabelFormValues{Name: "bug", Color: "#00ff00"}, values)
	require.Contains(t, rendered.String(), "use six hexadecimal digits")
}
