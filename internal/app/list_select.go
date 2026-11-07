// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

// selectAction is a follow-up to a selected item: a gt command to run.
type selectAction struct {
	label string
	args  []string
}

// selection describes how --select offers the items of a list command.
type selection[T any] struct {
	// noun names an item in the prompt, such as "an issue".
	noun  string
	label func(T) string
	// view is the gt command that shows an item's details.
	view    func(T) []string
	actions func(T) []selectAction
}

// selectFlag adds --select to the list command cmd.
func selectFlag(cmd *cobra.Command, selecting *bool) {
	cmd.Flags().BoolVar(selecting, "select", false, "Pick an item from a filterable list, view it and choose what to do with it")
}

// checkSelect refuses --select where gt cannot ask, or with output flags that it replaces.
func (a *application) checkSelect(cmd *cobra.Command, selecting bool) error {
	if !selecting {
		return nil
	}
	for _, flag := range []string{"json", "jq", "csv", "template", "web", "local"} {
		if cmd.Flags().Changed(flag) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--select cannot be combined with --%s", flag))
		}
	}
	if _, ok := a.chooser(); !ok {
		return clierr.Wrap(clierr.Usage, errors.New("--select requires a terminal where gt can prompt"))
	}
	return nil
}

// runSelection asks for one of items, shows it and runs the follow-up action the user picks. host
// keeps the commands it runs on the instance that listed the items.
func runSelection[T any](a *application, cmd *cobra.Command, items []T, spec selection[T], host string, page listPage) error {
	chooser, ok := a.chooser()
	if !ok {
		return clierr.Wrap(clierr.Usage, errors.New("--select requires a terminal where gt can prompt"))
	}
	if err := a.morePagesHint(cmd, page.More, page.Number); err != nil {
		return err
	}
	choices := make([]terminal.Choice, len(items))
	for i, item := range items {
		choices[i] = terminal.Choice{Label: spec.label(item), Value: strconv.Itoa(i)}
	}
	chosen, err := chooser.Choose(cmd.Context(), "Select "+spec.noun+" (/ to filter)", choices, "")
	if err != nil {
		return err
	}
	index, err := strconv.Atoi(chosen)
	if err != nil {
		return err
	}
	item := items[index]
	if err := a.runSelected(cmd, host, spec.view(item)); err != nil {
		return err
	}
	const done = "done"
	actions := spec.actions(item)
	choices = make([]terminal.Choice, 0, len(actions)+1)
	for i, action := range actions {
		choices = append(choices, terminal.Choice{Label: action.label, Value: strconv.Itoa(i)})
	}
	choices = append(choices, terminal.Choice{Label: "Done", Value: done})
	chosen, err = chooser.Choose(cmd.Context(), "What next?", choices, done)
	if err != nil || chosen == done {
		return err
	}
	if index, err = strconv.Atoi(chosen); err != nil {
		return err
	}
	return a.runSelected(cmd, host, actions[index].args)
}

// runSelected runs a gt command for a selected item on the instance host. The outer command already
// checks for a new release.
func (a *application) runSelected(cmd *cobra.Command, host string, args []string) error {
	deps := a.deps
	deps.Getenv = func(key string) string {
		if key == "GITEA_CLI_NO_UPDATE_NOTIFIER" {
			return "1"
		}
		return a.deps.Getenv(key)
	}
	if a.options.user != "" {
		args = append(args, "--user", a.options.user)
	}
	return Execute(cmd.Context(), deps, append(args, "--host", host))
}

// issueSelection offers the issues or pull requests of repo, as --kind lists them.
func issueSelection(repo, kind string) selection[services.Issue] {
	return selection[services.Issue]{
		noun: map[string]string{"issues": "an issue", "pulls": "a pull request", "all": "an issue or pull request"}[kind],
		label: func(item services.Issue) string {
			return fmt.Sprintf("#%d %s", item.Number, item.Title)
		},
		view: func(item services.Issue) []string {
			group := "issue"
			if item.Kind == services.KindPull {
				group = "pr"
			}
			return []string{group, "view", strconv.FormatInt(item.Number, 10), "-R", repo}
		},
		actions: func(item services.Issue) []selectAction {
			if item.Kind == services.KindPull {
				return pullActions(services.Pull{Number: item.Number, State: item.State}, repo)
			}
			return discussionActions("issue", item.Number, item.State, repo)
		},
	}
}

// pullSelection offers the pull requests of repo.
func pullSelection(repo string) selection[services.Pull] {
	return selection[services.Pull]{
		noun: "a pull request",
		label: func(item services.Pull) string {
			return fmt.Sprintf("#%d %s (%s)", item.Number, item.Title, pullHead(item, repo))
		},
		view: func(item services.Pull) []string {
			return []string{"pr", "view", strconv.FormatInt(item.Number, 10), "-R", repo}
		},
		actions: func(item services.Pull) []selectAction { return pullActions(item, repo) },
	}
}

// releaseSelection offers the releases of repo.
func releaseSelection(repo string) selection[services.Release] {
	return selection[services.Release]{
		noun: "a release",
		label: func(item services.Release) string {
			if item.Title == "" || item.Title == item.Tag {
				return item.Tag
			}
			return item.Tag + " " + item.Title
		},
		view: func(item services.Release) []string { return []string{"release", "view", item.Tag, "-R", repo} },
		actions: func(item services.Release) []selectAction {
			return []selectAction{
				{"Download its assets", []string{"release", "download", item.Tag, "-R", repo}},
				{"Open in the browser", []string{"release", "view", item.Tag, "--web", "-R", repo}},
			}
		},
	}
}

// repoSelection offers repositories.
func repoSelection() selection[services.Repository] {
	return selection[services.Repository]{
		noun:  "a repository",
		label: func(item services.Repository) string { return item.FullName },
		view:  func(item services.Repository) []string { return []string{"repo", "view", item.FullName} },
		actions: func(item services.Repository) []selectAction {
			return []selectAction{
				{"Clone", []string{"repo", "clone", item.FullName}},
				{"Open in the browser", []string{"repo", "view", item.FullName, "--web"}},
			}
		},
	}
}

// discussionActions are the follow-ups an issue and a pull request share.
func discussionActions(group string, number int64, state, repo string) []selectAction {
	n := strconv.FormatInt(number, 10)
	actions := []selectAction{{"Comment", []string{group, "comment", n, "-R", repo}}}
	switch state {
	case "open":
		actions = append(actions, selectAction{"Close", []string{group, "close", n, "-R", repo}})
	case "closed":
		actions = append(actions, selectAction{"Reopen", []string{group, "reopen", n, "-R", repo}})
	}
	return append(actions, selectAction{"Open in the browser", []string{group, "view", n, "--web", "-R", repo}})
}

// pullActions are the follow-ups to a pull request in its state.
func pullActions(item services.Pull, repo string) []selectAction {
	state := item.State
	if item.Merged {
		state = "merged"
	}
	actions := discussionActions("pr", item.Number, state, repo)
	n := strconv.FormatInt(item.Number, 10)
	own := []selectAction{
		{"Check out", []string{"pr", "checkout", n, "-R", repo}},
		{"Show the diff", []string{"pr", "diff", n, "-R", repo}},
	}
	if state == "open" {
		own = append(own, selectAction{"Review", []string{"pr", "review", n, "-R", repo}}, selectAction{"Merge", []string{"pr", "merge", n, "-R", repo}})
	}
	return append(own, actions...)
}
