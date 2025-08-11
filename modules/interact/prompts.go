// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"
	"strings"
	"time"

	"code.gitea.io/tea/modules/theme"
	"code.gitea.io/tea/modules/utils"
	"github.com/charmbracelet/huh"
)

// PromptPassword asks for a password and blocks until input was made.
func PromptPassword(name string) (pass string, err error) {
	err = huh.NewInput().
		Title(name + " password:").
		Validate(huh.ValidateNotEmpty()).EchoMode(huh.EchoModePassword).
		Value(&pass).
		WithTheme(theme.GetTheme()).
		Run()
	return
}

// promptRepoSlug interactively prompts for a Gitea repository or returns the current one
func promptRepoSlug(defaultOwner, defaultRepo string) (owner, repo string, err error) {
	prompt := "Target repo:"
	defaultVal := ""
	required := true
	if len(defaultOwner) != 0 && len(defaultRepo) != 0 {
		defaultVal = fmt.Sprintf("%s/%s", defaultOwner, defaultRepo)
		required = false
	}
	var repoSlug string

	owner = defaultOwner
	repo = defaultRepo
	repoSlug = defaultVal

	err = huh.NewInput().
		Title(prompt).
		Value(&repoSlug).
		Validate(func(str string) error {
			if !required && len(str) == 0 {
				return nil
			}
			split := strings.Split(str, "/")
			if len(split) != 2 || len(split[0]) == 0 || len(split[1]) == 0 {
				return fmt.Errorf("must follow the <owner>/<repo> syntax")
			}
			return nil
		}).WithTheme(theme.GetTheme()).Run()

	if err == nil && len(repoSlug) != 0 {
		repoSlugSplit := strings.Split(repoSlug, "/")
		owner = repoSlugSplit[0]
		repo = repoSlugSplit[1]
	}
	return
}

// promptDatetime prompts for a date or datetime string.
// Supports all formats understood by araddon/dateparse.
func promptDatetime(prompt string) (val *time.Time, err error) {
	var date string
	if err := huh.NewInput().
		Title(prompt).
		Placeholder("YYYY-MM-DD").
		Validate(func(s string) error {
			if s == "" {
				return nil
			}
			_, err := time.Parse("2006-01-02", s)
			return err
		}).
		Value(&date).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return nil, err
	}

	if date == "" {
		return nil, nil // no date
	}
	t, _ := time.Parse("2006-01-02", date)
	return &t, nil
}

// promptSelect creates a generic multiselect prompt, with processing of custom values.
func promptMultiSelect(prompt string, options []string, customVal string) ([]string, error) {
	var selection []string
	if err := huh.NewMultiSelect[string]().
		Title(prompt).
		Options(huh.NewOptions(makeSelectOpts(options, customVal, "")...)...).
		Value(&selection).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return nil, err
	}
	return promptCustomVal(prompt, customVal, selection)
}

// promptSelectV2 creates a generic select prompt
func promptSelectV2(prompt string, options []string) (string, error) {
	if len(options) == 0 {
		return "", nil
	}
	selection := options[0]
	if err := huh.NewSelect[string]().
		Title(prompt).
		Options(huh.NewOptions(options...)...).
		Value(&selection).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return "", err
	}
	return selection, nil
}

// promptSelect creates a generic select prompt, with processing of custom values or none-option.
func promptSelect(prompt string, options []string, customVal, noneVal, defaultVal string) (string, error) {
	var selection string
	if defaultVal == "" && noneVal != "" {
		defaultVal = noneVal
	}

	selection = defaultVal
	if err := huh.NewSelect[string]().
		Title(prompt).
		Options(huh.NewOptions(makeSelectOpts(options, customVal, noneVal)...)...).
		Value(&selection).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return "", err
	}

	if noneVal != "" && selection == noneVal {
		return "", nil
	}
	if customVal != "" {
		sel, err := promptCustomVal(prompt, customVal, []string{selection})
		if err != nil {
			return "", err
		}
		selection = sel[0]
	}
	return selection, nil
}

// makeSelectOpts adds cusotmVal & noneVal to opts if set.
func makeSelectOpts(opts []string, customVal, noneVal string) []string {
	if customVal != "" {
		opts = append(opts, customVal)
	}
	if noneVal != "" {
		opts = append(opts, noneVal)
	}
	return opts
}

// promptCustomVal checks if customVal is present in selection, and prompts
// for custom input to add to the selection instead.
func promptCustomVal(prompt, customVal string, selection []string) ([]string, error) {
	// check for custom value & prompt again with text input
	if otherIndex := utils.IndexOf(selection, customVal); otherIndex != -1 {
		var customAssignees string
		if err := huh.NewInput().
			Title(prompt).
			Description("comma separated list").
			Value(&customAssignees).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return nil, err
		}
		selection = append(selection[:otherIndex], selection[otherIndex+1:]...)
		selection = append(selection, strings.Split(customAssignees, ",")...)
	}
	return selection, nil
}
