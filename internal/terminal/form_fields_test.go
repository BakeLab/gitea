// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	gt "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

// view renders form after the terminal reports width×height.
func view(form *huh.Form, width, height int) string {
	form.Init()
	form.Update(gt.WindowSizeMsg{Width: width, Height: height})
	return form.View()
}

func TestMultiSelectShowsEveryOption(t *testing.T) {
	for _, count := range []int{1, 2, 5, maxVisibleOptions, 12} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var options []string
			for i := range count {
				options = append(options, fmt.Sprintf("user%02d", i))
			}
			var selected []string
			rendered := view(huh.NewForm(huh.NewGroup(multiSelect("Assignees:", huh.NewOptions(options...), &selected))), 80, 24)
			for i, option := range options {
				if i < maxVisibleOptions {
					require.Contains(t, rendered, option)
				} else {
					require.NotContains(t, rendered, option, "options past the first page scroll")
				}
			}
		})
	}
}

func TestFormsRefuseTerminalsWithoutSize(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {80, 0}, {0, 24}} {
		p, rendered := testPrompter(strings.NewReader("Title\r"))
		p.size = func() (int, int, error) { return size[0], size[1], nil }
		_, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
		require.ErrorIs(t, err, errNoTerminalSize, "%v", size)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.Empty(t, rendered.String())
	}
	p, _ := testPrompter(strings.NewReader("Title\r"))
	p.size = func() (int, int, error) { return 80, 24, nil }
	values, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "Title", values.Title)
}
