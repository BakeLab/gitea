// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"

	gt "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
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
		t.Run(strconv.Itoa(count), func(t *testing.T) {
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
	p, _ := testPrompter(strings.NewReader("Title\r\r"))
	p.size = func() (int, int, error) { return 80, 24, nil }
	values, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "Title", values.Title)

	// Accessible forms read line by line, so they need no size.
	p, _ = accessiblePrompter(t, "Title\n\n")
	p.size = func() (int, int, error) { return 0, 0, nil }
	values, err = p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "Title", values.Title)
	p, _ = accessiblePrompter(t, "y\n")
	p.size = func() (int, int, error) { return 0, 0, nil }
	confirmed, err := p.ConfirmDefault(context.Background(), "Delete?", false)
	require.NoError(t, err)
	require.True(t, confirmed)
}

func TestFieldResultRestoresUnchangedLines(t *testing.T) {
	seed := "intro\r\n```make\r\nall:\r\n\techo hi\r\n```\r\n"
	shown := fieldText(seed)
	require.Equal(t, "intro\n```make\nall:\n    echo hi\n```\n", shown)
	require.Equal(t, seed, fieldResult(seed, shown))
	require.Equal(t, seed+"more", fieldResult(seed, shown+"more"), "an appended line keeps the tabs and CRLF of the others")
	require.Equal(t, "intro\r\n```make\r\nall:\r\n    echo bye\r\n```\r\n", fieldResult(seed, strings.Replace(shown, "hi", "bye", 1)), "an edited line keeps what was typed")
	require.Equal(t, "a\n\tb\n    b\nc", fieldResult("a\n\tb\n    b", "a\n    b\n    b\nc"), "lines that show alike keep their own original")
	require.Equal(t, "\tb\na", fieldResult("a\n\tb", "    b\na"), "a moved line keeps its tabs")
}

func TestBodyFieldStartsAtTheBeginning(t *testing.T) {
	value := "first line\n" + strings.Repeat("more\n", 30)
	field := bodyField("Body", &value)
	field.Lines(5)
	// The cursor's reverse video splits the line it sits on, so compare the plain text.
	rendered := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(view(huh.NewForm(huh.NewGroup(field)), 80, 24), "")
	require.Contains(t, rendered, "first line", "an existing body shows its beginning")
}

func TestDueDateInputFitsNarrowTerminals(t *testing.T) {
	for width := range 13 {
		var deadline string
		require.NotPanics(t, func() { view(huh.NewForm(huh.NewGroup(dueDateInput("Due date:", &deadline))), width, 24) }, "width %d", width)
	}
	require.Contains(t, view(huh.NewForm(huh.NewGroup(dueDateInput("Due date:", new(string)))), 80, 24), "YYY-MM-DD", "the whole placeholder still shows after the cursor")
}

func TestIssueFormSanitizesServerTextInHeadingAndOptions(t *testing.T) {
	fields := issueFields{}
	groups := fields.groups("Editing R#1 (\x1b]8;;https://evil.example\x07login docs\x1b]8;;\x07)", noEdit, nil,
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
	groups := fields.groups("Editing R#1 (heading)", noEdit, nil, milestones, nil)
	rendered := view(huh.NewForm(groups...), 80, 24)
	require.LessOrEqual(t, strings.Count(rendered, "\n")+1, 24, "a taller frame scrolls its heading off the screen")
	require.Contains(t, rendered, "Editing R#1")
	rendered = view(huh.NewForm(groups[1]), 80, 24)
	require.LessOrEqual(t, strings.Count(rendered, "\n")+1, 24)
	require.Contains(t, rendered, "Milestone:")
}

func TestAccessibleRequiredFieldsRejectControlCharacters(t *testing.T) {
	p, rendered := accessiblePrompter(t, "\x1b \nTitle\n\n")
	values, err := p.collectIssueProperties(context.Background(), "", "", IssueFormChoices{EditBody: noEdit})
	require.NoError(t, err)
	require.Equal(t, "Title", values.Title)
	require.Contains(t, rendered.String(), "input cannot be empty")
}

func TestBodyFieldInsertsLineBreaksPastNinetyNineLines(t *testing.T) {
	for _, newLine := range []gt.KeyPressMsg{{Code: 'j', Mod: gt.ModCtrl}, {Code: gt.KeyEnter, Mod: gt.ModAlt}} {
		lines := make([]string, 99)
		for i := range lines {
			lines[i] = fmt.Sprintf("l%d", i+1)
		}
		value := strings.Join(lines, "\n")
		form := huh.NewForm(huh.NewGroup(bodyField(BodyTitle, &value)))
		view(form, 80, 24)
		form.Update(gt.KeyPressMsg{Code: 'N', Text: "N"})
		form.Update(newLine)
		form.Update(gt.KeyPressMsg{Code: 'Y', Text: "Y"})
		require.Equal(t, "N\nYl1\n"+strings.Join(lines[1:], "\n"), value, "%v", newLine)
	}
}
