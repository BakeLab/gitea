// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/spf13/cobra"
)

// ensureHeadPublished pushes the head branch, setting its upstream, when the selected remote tip is
// missing or differs. Cross-repo heads (owner:branch), non-work-trees, and repositories without a
// matching remote are skipped.
func (a *application) ensureHeadPublished(cmd *cobra.Command, t target, snapshot settings.Snapshot, head string) error {
	if head == "" || strings.Contains(head, ":") {
		return nil
	}
	ctx := cmd.Context()
	probe := a.gitProbe()
	inside, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return nil
	}
	local, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+head+"^{commit}")
	if err != nil {
		return nil
	}
	remote, err := a.checkoutRemote(ctx, t, snapshot)
	if err != nil {
		if a.options.remote == "" && snapshot.Preferences.Remote == "" && errors.Is(err, errNoMatchingRemote) {
			return nil
		}
		return clierr.Wrap(clierr.Usage, err)
	}
	remoteTip, tipErr := probe.Output(ctx, a.options.cwd, false, "for-each-ref", "--format=%(objectname)", "refs/remotes/"+remote+"/"+head)
	if tipErr == nil && remoteTip == local {
		return nil
	}
	confirmation := terminal.Confirmation{
		Prompt: fmt.Sprintf("Push branch %s to %s first?", head, remote), DefaultYes: true,
		NonTTY: fmt.Sprintf("push branch %q to remote %q before creating the pull request (or push it first)", head, remote),
	}
	if err := a.confirmAction(cmd, confirmation); err != nil {
		return err
	}
	credentials, err := a.remoteCredentials(ctx, t, remote)
	if err != nil {
		return err
	}
	args := append(append([]string{}, credentials...), "push", "--set-upstream", "--quiet", "--", remote, "refs/heads/"+head)
	if _, err = a.git(ctx, args...); err != nil {
		return err
	}
	a.done(cmd, "Pushed %s to %s", head, remote)
	return nil
}

func (a *application) addPullMutations(group *cobra.Command) {
	group.AddCommand(a.pullCreateCommand(), a.pullStateCommand(true), a.pullStateCommand(false), a.pullDiffCommand(), a.pullReviewCommand(), a.pullMergeCommand())
}

// pullStateCommand builds gt pr close, or gt pr reopen when closing is false.
func (a *application) pullStateCommand(closing bool) *cobra.Command {
	verb, done, state := "reopen", "Reopened", "open"
	if closing {
		verb, done, state = "close", "Closed", "closed"
	}
	var comment string
	var deleteBranch bool
	cmd := &cobra.Command{Use: verb + " [<number> | <url> | <branch>]", Short: titleCase(verb) + " a pull request", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		item, err := service.View(cmd.Context(), n)
		if err != nil {
			return err
		}
		ref := refLabel(t.Repo, n, item.Title)
		switch {
		case item.Merged:
			return fmt.Errorf("pull request %s is already merged; it cannot be %s", ref, map[bool]string{true: "closed", false: "reopened"}[closing])
		case item.State == state:
			a.warn(cmd, "Pull request %s is already %s", ref, state)
		default:
			if comment != "" {
				if _, err := (services.Comments{Client: t.client, Repository: t.Repo}).Create(cmd.Context(), n, comment); err != nil {
					return err
				}
			}
			if item, err = service.Edit(cmd.Context(), n, services.PullEdit{State: &state}); err != nil {
				return err
			}
			a.done(cmd, "%s pull request %s", done, ref)
		}
		if deleteBranch {
			a.deleteHead(cmd, service, item, false)
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		return nil
	}}
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "Leave a comment before changing the state")
	cmd.ValidArgsFunction = a.completePulls(map[bool]string{true: "open", false: "closed"}[closing])
	if closing {
		cmd.Flags().BoolVarP(&deleteBranch, "delete-branch", "d", false, "Delete the head branch on Gitea and, when it is checked out, locally")
		cmd.Example = `$ gt pr close 13
$ gt pr close 13 -c "Superseded by #15" --delete-branch`
	} else {
		cmd.Example = `$ gt pr reopen 13`
	}
	return cmd
}

// deleteHead deletes the head branch of a merged or closed pull request on Gitea, unless it is
// gone, and then locally, reporting each step. merging tells that Gitea deleted it as part of
// the merge.
func (a *application) deleteHead(cmd *cobra.Command, service services.Pulls, item services.Pull, merging bool) {
	switch {
	case item.HeadRemoteDeleted && merging:
		a.done(cmd, "Deleted remote branch %s", item.Head)
	case item.HeadRemoteDeleted:
		a.warn(cmd, "Remote branch %s was already deleted", item.Head)
	case !item.HeadCanPush:
		a.warn(cmd, "Kept remote branch %s: you cannot push to %s", item.Head, item.HeadRepo)
	default:
		if err := service.DeleteHead(cmd.Context(), item); err != nil {
			a.warn(cmd, "Kept remote branch %s: %v", item.Head, err)
		} else {
			a.done(cmd, "Deleted remote branch %s", item.Head)
		}
	}
	a.deleteLocalHead(cmd, item)
}

// deleteLocalHead deletes the checked-out branch when it is at the head of the pull request,
// switching to the base branch first, like gh pr merge --delete-branch.
func (a *application) deleteLocalHead(cmd *cobra.Command, item services.Pull) {
	ctx := cmd.Context()
	branch, err := a.currentBranch(ctx)
	if err != nil || branch == item.Base || item.HeadSHA == "" {
		return
	}
	if tip, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}"); err != nil || tip != item.HeadSHA {
		return
	}
	result, err := git.CleanPull(ctx, a.deps.Git, git.CleanOptions{Directory: a.options.cwd, DefaultBranch: item.Base, LocalBranch: branch, Prompt: a.canPrompt()})
	if err != nil {
		a.warn(cmd, "Kept local branch %s: %v", branch, err)
		return
	}
	a.done(cmd, "Switched to %s and deleted local branch %s", result.DefaultBranch, result.LocalBranch)
}

func (a *application) pullDiffCommand() *cobra.Command {
	var nameOnly, web bool
	var color string
	cmd := &cobra.Command{Use: "diff [<number> | <url> | <branch>]", Short: "Show the changes of a pull request", Long: `Show the changes of a pull request as a unified diff.

On a terminal the diff is colored and paged. Piped, it is written unchanged, so that
gt pr diff 13 | git apply works.`, Annotations: pullArgumentsAnnotation(textOutput("diff returns patch text")), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		if web {
			item, err := service.View(cmd.Context(), n)
			if err != nil {
				return err
			}
			return a.openBrowser(cmd, item.URL+"/files")
		}
		if nameOnly {
			files, err := service.Files(cmd.Context(), n)
			if err != nil {
				return err
			}
			for i, file := range files {
				files[i] = terminal.SafeLine(file)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), strings.Join(append(files, ""), "\n"))
			return err
		}
		body, err := service.Diff(cmd.Context(), n)
		if err != nil {
			return err
		}
		defer body.Close()
		d := a.display()
		colored := color == "always" || (color == "auto" && d.Color)
		if !d.TTY && !colored {
			_, err = io.Copy(cmd.OutOrStdout(), body)
			return err
		}
		text, err := diffText(body, colored)
		if err != nil {
			return err
		}
		if !d.TTY {
			_, err = io.WriteString(cmd.OutOrStdout(), text)
			return err
		}
		return a.writeHuman(cmd, text)
	}}
	cmd.Flags().BoolVar(&nameOnly, "name-only", false, "List only the names of the changed files")
	enumFlag(cmd, &color, "color", "", "auto", "Color the diff", "auto", "always", "never")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the changed files in the browser")
	cmd.ValidArgsFunction = a.completePulls("all")
	cmd.Example = `$ gt pr diff
$ gt pr diff 13 --name-only
$ gt pr diff 13 | git apply`
	return cmd
}

// diffText sanitizes a diff for a terminal and, when colored is set, colors it like git: added
// lines green, removed lines red, hunk headers cyan and file headers bold.
func diffText(diff io.Reader, colored bool) (string, error) {
	var b strings.Builder
	scanner := bufio.NewScanner(diff)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := terminal.SafeText(scanner.Text())
		switch {
		case !colored:
		case strings.HasPrefix(line, "diff --git"), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "new file mode"), strings.HasPrefix(line, "deleted file mode"):
			line = terminal.Emphasis(line, true)
		case strings.HasPrefix(line, "@@"):
			line = terminal.Colorize("cyan", line, true)
		case strings.HasPrefix(line, "+"):
			line = terminal.Colorize("green", line, true)
		case strings.HasPrefix(line, "-"):
			line = terminal.Colorize("red", line, true)
		}
		b.WriteString(line + "\n")
	}
	return b.String(), scanner.Err()
}

// mergeVerbs report a merge by its style: "Squashed and merged pull request alice/x#3 (title)".
var mergeVerbs = map[string]string{
	"merge": "Merged", "rebase": "Rebased and merged", "rebase-merge": "Rebased and merged", "squash": "Squashed and merged", "fast-forward-only": "Merged",
}

// mergeMethods are the merge styles in Gitea's order.
var mergeMethods = []string{"merge", "rebase", "rebase-merge", "squash", "fast-forward-only"}

func (a *application) pullMergeCommand() *cobra.Command {
	var method, title, message, messageFile string
	var deleteBranch, useEditor, auto bool
	var style struct{ merge, squash, rebase bool }
	cmd := &cobra.Command{Use: "merge [<number> | <url> | <branch>]", Short: "Merge a pull request", Long: `Merge a pull request on Gitea.

Without a method, gt uses the repository's default merge style. On a terminal, leaving out
the method or --delete-branch asks for them, preselecting the repository's defaults. With
--delete-branch, gt also deletes the local head branch when it is checked out, switching to
the base branch.`, Annotations: pullArgumentsAnnotation(map[string]string{"gt.confirm": "true"}), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		for name, set := range map[string]bool{"merge": style.merge, "squash": style.squash, "rebase": style.rebase} {
			if set && method != "" {
				return clierr.Wrap(clierr.Usage, errors.New("choose one merge method: --merge, --squash, --rebase or --method"))
			}
			if set {
				method = name
			}
		}
		if err := validateTextSources(cmd, "message", "message-file"); err != nil {
			return err
		}
		if !useEditor {
			var err error
			if message, err = a.readTextSource(cmd, message, messageFile, "message", "message-file"); err != nil {
				return err
			}
		}
		t, n, err := a.mergeTarget(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		item, err := service.View(cmd.Context(), n)
		if err != nil {
			return err
		}
		ref := refLabel(t.Repo, n, item.Title)
		switch {
		case item.Merged:
			a.warn(cmd, "Pull request %s is already merged", ref)
			if a.options.json != "" {
				return a.writeJSON(cmd, item)
			}
			return nil
		case item.State == "closed":
			return fmt.Errorf("pull request %s is closed; reopen it with: gt pr reopen %d", ref, n)
		case item.Draft:
			return fmt.Errorf("pull request %s is a draft; mark it ready with: gt pr ready %d", ref, n)
		}
		repoSettings, err := service.MergeSettings(cmd.Context())
		if err != nil {
			return err
		}
		askMethod, askDelete := method == "", !cmd.Flags().Changed("delete-branch") && !item.HeadRemoteDeleted
		if method == "" {
			method = repoSettings.Default
		} else if !slices.Contains(repoSettings.Styles, method) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%s merges are disabled in %s; use one of: %s", method, t.Repo, strings.Join(repoSettings.Styles, ", ")))
		}
		if !cmd.Flags().Changed("delete-branch") {
			deleteBranch = repoSettings.DeleteBranch
		}
		header := fmt.Sprintf("Merge #%d %q (%s → %s) in %s", n, item.Title, pullHead(item, t.Repo), item.Base, t.Repo)
		if (askMethod || askDelete) && !a.options.yes && a.canPrompt() && a.deps.MergeForm != nil {
			choices := terminal.MergeFormChoices{Header: header, Method: method, DeleteBranch: deleteBranch}
			if askMethod {
				choices.Methods = repoSettings.Styles
			}
			if askDelete {
				choices.Branch = item.Head
			}
			values, err := a.deps.MergeForm.MergeOptions(cmd.Context(), choices)
			if err != nil {
				return err
			}
			method, deleteBranch = values.Method, values.DeleteBranch
		} else if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: header + "?", DefaultYes: true, NonTTY: "merge " + t.Repo + "#" + fmt.Sprint(n)}); err != nil {
			return err
		}
		if useEditor {
			if editorDraft, err = a.editDraft(cmd, ""); err != nil {
				return err
			}
			message = editorDraft.Body
		}
		if err := service.Merge(cmd.Context(), n, services.MergeOptions{Method: method, Title: title, Message: message, DeleteBranch: deleteBranch, Auto: auto}); err != nil {
			return err
		}
		merged, err := service.View(cmd.Context(), n)
		if err != nil {
			return fmt.Errorf("pull request %s was merged, but reading it failed: %w", ref, err)
		}
		if auto && !merged.Merged {
			a.done(cmd, "Pull request %s will be merged when its checks succeed", ref)
		} else {
			a.done(cmd, "%s pull request %s", mergeVerbs[method], ref)
			if deleteBranch {
				a.deleteHead(cmd, service, merged, true)
			}
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, merged)
		}
		return nil
	}}
	enumFlag(cmd, &method, "method", "s", "", "Merge method (default: the repository's default merge style)", mergeMethods...)
	cmd.Flags().StringVar(&method, "style", "", "Alias of --method")
	enumValues(cmd, "style", mergeMethods...)
	_ = cmd.Flags().MarkHidden("style")
	cmd.Flags().BoolVar(&style.merge, "merge", false, "Create a merge commit")
	cmd.Flags().BoolVar(&style.squash, "squash", false, "Squash the commits into one commit and merge it")
	cmd.Flags().BoolVar(&style.rebase, "rebase", false, "Rebase the commits onto the base branch")
	cmd.Flags().BoolVarP(&deleteBranch, "delete-branch", "d", false, "Delete the head branch after merging (default: the repository setting)")
	cmd.Flags().BoolVar(&auto, "auto", false, "Merge once the required checks succeed")
	cmd.Flags().StringVarP(&title, "title", "t", "", "Merge commit title")
	cmd.Flags().StringVarP(&message, "message", "b", "", "Merge commit message")
	cmd.Flags().StringVarP(&messageFile, "message-file", "F", "", "Read the merge commit message from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Write the merge commit message in your editor")
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if strings.Contains(err.Error(), "flag needs an argument: 's' in -s") {
			return clierr.Wrap(clierr.Usage, errors.New("-s takes a method (merge, rebase, rebase-merge, squash or fast-forward-only); for gh-style use --squash"))
		}
		return c.Root().FlagErrorFunc()(c, err)
	})
	cmd.ValidArgsFunction = a.completePulls("open")
	cmd.Example = `# Merge the pull request of the current branch with the repository's default style
$ gt pr merge

# Squash #13 and delete its branch
$ gt pr merge 13 --squash --delete-branch

# Merge once the checks pass, without prompts
$ gt pr merge 13 --auto --yes`
	return cmd
}

// mergeTarget selects the pull request to merge: the argument, else the current branch's pull
// request, else, on a terminal, one that the user picks among those for the current branch.
func (a *application) mergeTarget(cmd *cobra.Command, args []string) (target, int64, error) {
	t, n, err := a.pullSelector(cmd, args)
	if err == nil || len(args) > 0 || a.options.repo != "" || a.options.yes || !a.canPrompt() || a.deps.MergeForm == nil ||
		clierr.ExitCode(err) != clierr.Failure || errors.As(err, new(*transport.APIError)) {
		return t, n, err
	}
	if t, err = a.resolve(cmd.Context(), true); err != nil {
		return t, 0, err
	}
	n, err = a.selectPullToMerge(cmd, t)
	return t, n, err
}

func (a *application) selectPullToMerge(cmd *cobra.Command, t target) (int64, error) {
	branch, err := a.currentBranch(cmd.Context())
	if err != nil {
		return 0, clierr.Wrap(clierr.Usage, errors.New("not on a branch; pass a pull request number, URL or branch"))
	}
	items, err := (services.Pulls{Client: t.client, Repository: t.Repo}).ListOpen(cmd.Context())
	if err != nil {
		return 0, err
	}
	var headMatches, baseMatches []terminal.MergePullChoice
	for _, item := range items {
		choice := terminal.MergePullChoice{Number: item.Number, Title: item.Title, Head: pullHead(item, t.Repo)}
		switch branch {
		case item.Head:
			headMatches = append(headMatches, choice)
		case item.Base:
			baseMatches = append(baseMatches, choice)
		}
	}
	return a.deps.MergeForm.SelectPull(cmd.Context(), append(headMatches, baseMatches...))
}
