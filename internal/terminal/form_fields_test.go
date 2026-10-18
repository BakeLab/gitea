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

func TestIssueFormSanitizesServerTextInHeadingAndOptions(t *testing.T) {
	fields := issueFields{}
	groups := fields.groups("Editing R#1 (\x1b]8;;https://evil.example\x07login docs\x1b]8;;\x07)", noEdit, true, nil,
		[]IssueMilestoneChoice{{ID: 1, Title: "v1\x1b[8mhidden"}}, []IssueLabelChoice{{ID: 2, Name: "bug\x1b[8m"}})
	rendered := view(huh.NewForm(groups...), 100, 40)
	require.NotContains(t, rendered, "\x1b]8")
	require.NotContains(t, rendered, "\x1b[8m")
	require.Contains(t, rendered, "login docs")
}

func TestMilestoneSelectKeepsItsTitleOnShortTerminals(t *testing.T) {
	var milestones []IssueMilestoneChoice
	for i := range 40 {
		milestones = append(milestones, IssueMilestoneChoice{ID: int64(i + 1), Title: fmt.Sprintf("v%d", i)})
	}
	fields := issueFields{}
	groups := fields.groups("Editing R#1 (heading)", noEdit, true, nil, milestones, nil)
	rendered := view(huh.NewForm(groups...), 80, 24)
	require.LessOrEqual(t, strings.Count(rendered, "\n")+1, 24, "a taller frame scrolls its heading off the screen")
	require.Contains(t, rendered, "Editing R#1")
	rendered = view(huh.NewForm(groups[1]), 80, 24)
	require.LessOrEqual(t, strings.Count(rendered, "\n")+1, 24)
	require.Contains(t, rendered, "Milestone:")
}
