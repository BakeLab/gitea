// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
// missing or differs, and returns the head to create the pull request from. A branch that tracks
// its namesake on a remote other than the base repository's is pushed there; any other branch, in
// a clone of a fork, goes to the fork, and so it does in a clone of the base repository that the
// account cannot push to, after forking it on a terminal. In those cases the head becomes
// OWNER:BRANCH. A cross-repo head (OWNER:BRANCH) is pushed only when the local branch tracks its
// namesake on a remote of OWNER. Non-work-trees and repositories without a matching remote are
// skipped. A base branch missing from the repository fails before anything is pushed.
func (a *application) ensureHeadPublished(cmd *cobra.Command, t target, snapshot settings.Snapshot, head string, base *pullBase) (string, error) {
	owner, branch, cross := strings.Cut(head, ":")
	if !cross {
		branch = head
	}
	if branch == "" {
		return head, nil
	}
	ctx := cmd.Context()
	if !a.insideClone(ctx) {
		return head, nil
	}
	probe := a.gitProbe()
	local, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return head, nil
	}
	published, remote, forked := head, "", false
	config := git.ReadBranchConfig(ctx, probe, a.options.cwd, branch)
	tracked := a.remoteRepository(ctx, t, config.Remote)
	trackedOwner, _, _ := strings.Cut(tracked, "/")
	switch {
	case cross:
		if config.Merge != branch || tracked == "" || !strings.EqualFold(trackedOwner, owner) {
			return head, nil // not a local branch of OWNER's repository
		}
		remote = config.Remote
	case config.Merge == branch && strings.EqualFold(tracked, t.Repo):
		remote = config.Remote // published on the base repository already
	case config.Merge == branch && tracked != "":
		remote, published = config.Remote, trackedOwner+":"+branch
	default:
		// A branch started from the base branch, or without an upstream.
		fork, forkOwner, forkNeeded := a.forkRemote(ctx, t, base)
		if forkNeeded {
			if err := base.require(ctx, t); err != nil {
				return head, err
			}
			if fork, forkOwner, err = a.forkForPush(cmd, t, branch); err != nil {
				return head, err
			}
			forked = true
		}
		if fork != "" {
			remote, published = fork, forkOwner+":"+branch
		}
	}
	if remote == "" {
		if remote, err = a.checkoutRemote(ctx, t, snapshot); err != nil {
			if a.options.remote == "" && errors.Is(err, errNoMatchingRemote) {
				return head, nil
			}
			return head, clierr.Wrap(clierr.Usage, err)
		}
	}
	remoteTip, tipErr := probe.Output(ctx, a.options.cwd, false, "for-each-ref", "--format=%(objectname)", "refs/remotes/"+remote+"/"+branch)
	if tipErr == nil && remoteTip == local {
		return published, nil
	}
	if !forked {
		if err := base.require(ctx, t); err != nil {
			return head, err
		}
		confirmation := terminal.Confirmation{
			Prompt: fmt.Sprintf("Push branch %s to %s first?", branch, remote), DefaultYes: true,
			NonTTY: fmt.Sprintf("push branch %q to remote %q before creating the pull request (or push it first)", branch, remote),
		}
		if err := a.confirmAction(cmd, confirmation); err != nil {
			return head, err
		}
	}
	credentials, err := a.remoteCredentials(ctx, t, remote)
	if err != nil {
		return head, err
	}
	args := append(append([]string{}, credentials...), "push", "--set-upstream", "--quiet", "--", remote, "refs/heads/"+branch)
	if _, err = a.git(ctx, args...); err != nil {
		return head, err
	}
	a.done(cmd, "Pushed %s to %s", branch, remote)
	if !strings.EqualFold(a.remoteRepository(ctx, t, remote), t.Repo) {
		// Discovery follows the branch's upstream, which is now the fork, unless the base remote is recorded.
		if base, err := a.checkoutRemote(ctx, t, snapshot); err == nil {
			if err := git.RecordBaseRemote(ctx, a.gitProbe(), a.options.cwd, branch, base); err != nil {
				a.warn(cmd, "Could not record Git remote %s as the base of branch %s: %v", base, branch, err)
			}
		}
	}
	return published, nil
}

// pullBase is the base branch of a new pull request and its repository, which is read once.
type pullBase struct {
	branch string
	repo   *services.PullRepository
}

// repository returns the repository of t that the pull request goes into.
func (b *pullBase) repository(ctx context.Context, t target) (services.PullRepository, error) {
	if b.repo == nil {
		repo, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Base(ctx)
		if err != nil {
			return repo, err
		}
		b.repo = &repo
	}
	return *b.repo, nil
}

// checkEnabled fails when the repository is missing or has its pull requests turned off. Other
// failures to read it are left to the requests that follow.
func (b *pullBase) checkEnabled(ctx context.Context, t target) error {
	repo, err := b.repository(ctx, t)
	if err != nil {
		if services.IsNotFound(err) {
			return err
		}
		return nil
	}
	return repo.CheckEnabled()
}

// canPush reports whether the account may push to the repository, or cannot tell.
func (b *pullBase) canPush(ctx context.Context, t target) bool {
	repo, err := b.repository(ctx, t)
	return err != nil || repo.CanPush
}

// require fails when the repository has its pull requests turned off, or the base branch, unless
// empty, is missing from it, so that the head branch is not published for nothing.
func (b *pullBase) require(ctx context.Context, t target) error {
	if err := b.checkEnabled(ctx, t); err != nil {
		return err
	}
	if b.branch == "" {
		return nil
	}
	_, err := (services.Branches{Client: t.client}).View(ctx, t.Repo, b.branch)
	return err
}

// remoteRepository returns the OWNER/NAME of the repository the named Git remote points to on t's
// instance, or "" when there is no such remote or it points elsewhere.
func (a *application) remoteRepository(ctx context.Context, t target, remote string) string {
	if remote == "" {
		return ""
	}
	raw, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", remote)
	if err != nil {
		return ""
	}
	host, repo, err := settings.RemoteRepository(raw, t.hosts())
	if err != nil || host != t.Host {
		return ""
	}
	return repo
}

// forkRemote returns the Git remote of a fork of t's repository to push a new branch to, and the
// owner of that fork, which may be an organization: origin when it is such a fork, else the only
// remote of a fork of the account gt acts as, unless origin is t's repository itself and the
// account can push to it. Both are empty when there is no such remote, or the account cannot push
// to that fork, so that the branch goes to the base repository. forkNeeded reports that a remote
// of t's repository exists but the account can push neither to it nor to a fork remote.
func (a *application) forkRemote(ctx context.Context, t target, base *pullBase) (remote, owner string, forkNeeded bool) {
	names, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote")
	if err != nil {
		return "", "", false
	}
	login := t.login()
	forks := map[string]string{}
	baseOrigin, baseRemote := false, false
	for name := range strings.FieldsSeq(names) {
		repo := a.remoteRepository(ctx, t, name)
		repoOwner, _, _ := strings.Cut(repo, "/")
		if strings.EqualFold(repo, t.Repo) {
			baseOrigin, baseRemote = baseOrigin || name == "origin", true
			continue
		}
		if repo == "" || strings.EqualFold(repoOwner, t.owner()) {
			continue // Gitea forks a repository only into another owner.
		}
		if name == "origin" || (login != "" && strings.EqualFold(repoOwner, login)) {
			forks[name] = repo
		}
	}
	service := services.Pulls{Client: t.client, Repository: t.Repo}
	pushable := func() bool { return base.canPush(ctx, t) }
	if baseOrigin && pushable() {
		return "", "", false // a clone of the base repository
	}
	remote = "origin"
	if _, ok := forks[remote]; !ok {
		if len(forks) != 1 {
			return "", "", len(forks) == 0 && baseRemote && (baseOrigin || !pushable())
		}
		for name := range forks {
			remote = name
		}
	}
	// A repository that is not a fork of t's cannot be the head of its pull request, and a fork the
	// account cannot push to, such as a colleague's, cannot take the branch.
	if ok, err := service.PushableFork(ctx, forks[remote]); err != nil || !ok {
		return "", "", false
	}
	owner, _, _ = strings.Cut(forks[remote], "/")
	return remote, owner, false
}

// forkForPush forks t's repository, which the account cannot push to, once confirmed on a
// terminal, and adds the fork as the Git remote "fork", or one named after the account when
// "fork" points elsewhere, to push the head branch to. A remote of either name that points to a
// fork the account can push to is used instead.
func (a *application) forkForPush(cmd *cobra.Command, t target, branch string) (remote, owner string, err error) {
	if !a.canPrompt() {
		return "", "", fmt.Errorf("you cannot push to %s; fork it with gt repo fork --remote and retry", t.Repo)
	}
	ctx := cmd.Context()
	names, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote")
	if err != nil {
		return "", "", err
	}
	existing := ""
	for _, name := range []string{"fork", t.login()} {
		if name == "" {
			continue
		}
		if !slices.Contains(strings.Fields(names), name) {
			remote = name
			break
		}
		if repo := a.remoteRepository(ctx, t, name); repo != "" {
			if ok, err := (services.Pulls{Client: t.client, Repository: t.Repo}).PushableFork(ctx, repo); err == nil && ok {
				remote, existing = name, repo
				break
			}
		}
	}
	if remote == "" {
		return "", "", fmt.Errorf("you cannot push to %s, and Git remotes fork and %s point elsewhere; add a remote of your fork with gt repo fork --remote --remote-name NAME and retry", t.Repo, t.login())
	}
	question := fmt.Sprintf("You cannot push to %s. Fork it and push branch %s to the fork?", t.Repo, branch)
	if existing != "" {
		question = fmt.Sprintf("You cannot push to %s. Push branch %s to your fork %s?", t.Repo, branch, existing)
	}
	if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: question, DefaultYes: true}); err != nil {
		return "", "", err
	}
	if existing != "" {
		owner, _, _ = strings.Cut(existing, "/")
		return remote, owner, nil
	}
	fork, existed, err := (services.Repositories{Client: t.client}).Fork(ctx, t.Repo, services.ForkOptions{})
	if err != nil {
		return "", "", err
	}
	if existed {
		a.warn(cmd, "%s already exists", fork.FullName)
	} else {
		a.done(cmd, "Forked %s to %s", t.Repo, fork.FullName)
	}
	if err := a.addForkRemote(cmd, t, fork, remote, false, ""); err != nil {
		return "", "", err
	}
	owner, _, _ = strings.Cut(fork.FullName, "/")
	return remote, owner, nil
}

func (a *application) addPullMutations(group *cobra.Command) {
	group.AddCommand(a.pullCreateCommand(), a.pullStateCommand(true), a.pullStateCommand(false), a.pullDiffCommand(), a.pullReviewCommand(), a.pullMergeCommand())
}

func (a *application) pullStateCommand(closing bool) *cobra.Command {
	verb, done, state := "reopen", "Reopened", "open"
	if closing {
		verb, done, state = "close", "Closed", "closed"
	}
	var comment string
	var deleteBranch bool
	cmd := &cobra.Command{Use: verb + " [<number> | <url> | <branch>]", Short: titleCase(verb) + " a pull request", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelectorAny(cmd, args)
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
			comments := services.Comments{Client: t.client, Repository: t.Repo}
			if item, err = withComment(cmd.Context(), comments, n, comment, func() (services.Pull, error) {
				return service.Edit(cmd.Context(), n, services.PullEdit{State: &state})
			}); err != nil {
				return err
			}
			a.done(cmd, "%s pull request %s", done, ref)
		}
		if deleteBranch {
			item = a.deleteHead(cmd, service, item, false, true)
		}
		return a.pullResult(cmd, t, item)
	}}
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "Leave a comment before changing the state")
	cmd.ValidArgsFunction = firstArg(a.completePulls(map[bool]string{true: "open", false: "closed"}[closing]))
	if closing {
		cmd.Flags().BoolVarP(&deleteBranch, "delete-branch", "d", false, "Delete the head branch on Gitea, also in a fork you can push to, and locally when it is checked out")
		cmd.Example = `$ gt pr close 13
$ gt pr close 13 -c "Superseded by #15" --delete-branch`
	} else {
		cmd.Example = `$ gt pr reopen 13`
	}
	return cmd
}

// deleteHead deletes the head branch of a merged or closed pull request on Gitea, unless it is
// gone, and then locally when local is set, reporting each step. merging tells that Gitea was
// asked to delete it as part of the merge; a branch Gitea kept then stays, since Gitea keeps the
// head branch of another open pull request, which deleting it would close. It returns item with
// HeadRemoteDeleted set when the remote branch is gone.
func (a *application) deleteHead(cmd *cobra.Command, service services.Pulls, item services.Pull, merging, local bool) services.Pull {
	switch {
	case item.Head == "":
		// An AGit pull request has no head branch to delete.
	case item.HeadRemoteDeleted && merging:
		a.done(cmd, "Deleted remote branch %s", item.Head)
	case item.HeadRemoteDeleted:
		a.warn(cmd, "Remote branch %s was already deleted", item.Head)
	case !item.HeadCanPush:
		a.warn(cmd, "Kept remote branch %s: you cannot push to %s", item.Head, item.HeadRepo)
	case merging:
		a.warn(cmd, "Kept remote branch %s: Gitea did not delete it (it is protected, the default branch, or used by another open pull request)", item.Head)
	default:
		reason := a.headKeptFor(cmd.Context(), service, item)
		if moved, err := service.HeadMoved(cmd.Context(), item); reason == "" && err == nil && moved {
			reason = fmt.Sprintf("it has commits that are not in pull request #%d", item.Number)
		}
		if reason != "" {
			a.warn(cmd, "Kept remote and local branch %s: %s", item.Head, reason)
			return item
		}
		if err := service.DeleteHead(cmd.Context(), item); err != nil {
			a.warn(cmd, "Kept remote branch %s: %v", item.Head, err)
		} else {
			a.done(cmd, "Deleted remote branch %s", item.Head)
			item.HeadRemoteDeleted = true
		}
	}
	if local {
		a.deleteLocalHead(cmd, item)
	}
	return item
}

// headKeptFor tells why the head branch of item must stay: deleting it would close another open
// pull request from it.
func (a *application) headKeptFor(ctx context.Context, service services.Pulls, item services.Pull) string {
	other, found, err := service.OtherOpenWithHead(ctx, item)
	switch {
	case err != nil:
		return fmt.Sprintf("could not check for other open pull requests from it: %v", err)
	case found:
		return "it is the head of open pull request " + refLabel(service.Repository, other.Number, other.Title)
	}
	return ""
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
gt pr diff 13 | git apply works, and --name-only writes the exact file names.`, Annotations: pullArgumentsAnnotation(textOutput("diff returns patch text")), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelectorAny(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		if web {
			return a.openBrowser(cmd, fmt.Sprintf("%s/pulls/%d/files", repositoryURL(t), n))
		}
		if nameOnly {
			files, err := service.Files(cmd.Context(), n)
			if err != nil {
				return err
			}
			// Piped names stay exact, so that they can be passed on to other commands.
			if a.display().TTY {
				for i, file := range files {
					files[i] = terminal.SafeLine(file)
				}
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
	cmd.ValidArgsFunction = firstArg(a.completePulls("all"))
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

// manualMerge marks a pull request merged outside Gitea as merged by a commit of its base branch.
const manualMerge = "manually-merged"

var mergeMethods = []string{"merge", "rebase", "rebase-merge", "squash", "fast-forward-only", manualMerge}

func (a *application) pullMergeCommand() *cobra.Command {
	var method, title, message, messageFile, commit string
	var deleteBranch, useEditor, auto bool
	var style struct{ merge, squash, rebase bool }
	cmd := &cobra.Command{Use: "merge [<number> | <url> | <branch>]", Short: "Merge a pull request", Long: `Merge a pull request on Gitea.

Without a method, gt uses the repository's default merge style. On a terminal, leaving out
the method or --delete-branch asks for them, preselecting the repository's defaults. With
--delete-branch, gt also deletes the local head branch when it is checked out, switching to
the base branch.

--delete-branch deletes the head branch of a pull request from a fork too, when you can push
to that fork: your own, or any as a site administrator. A fork you cannot push to keeps it.

For a pull request merged outside Gitea, such as with git merge and git push, --method
manually-merged --commit SHA marks it as merged by that commit of the base branch. The
repository must allow it (gt repo edit --allow-manual-merge). Gitea does not check that the
commit contains the pull request's changes.`, Annotations: pullArgumentsAnnotation(map[string]string{"gt.confirm": "true"}), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
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
		manual := method == manualMerge
		switch {
		case manual && commit == "":
			return clierr.Wrap(clierr.Usage, errors.New("--method manually-merged requires --commit"))
		case !manual && cmd.Flags().Changed("commit"):
			return clierr.Wrap(clierr.Usage, errors.New("--commit requires --method manually-merged"))
		case manual && !commitSHA.MatchString(commit):
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--commit requires a commit SHA of 7 to 64 hexadecimal digits, got %q", commit))
		}
		if manual {
			for _, name := range []string{"auto", "title", "message", "message-file", "editor"} {
				if cmd.Flags().Changed(name) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("--method manually-merged cannot be combined with --%s", name))
				}
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
			if deleteBranch {
				item = a.deleteHead(cmd, service, item, false, true)
			}
			return a.pullResult(cmd, t, item)
		case item.State == "closed":
			return fmt.Errorf("pull request %s is closed; reopen it with: gt pr reopen %d", ref, n)
		case item.Draft:
			return fmt.Errorf("pull request %s is a draft; mark it ready with: gt pr ready %d", ref, n)
		case item.HeadRemoteDeleted && item.Head != "" && !manual:
			return fmt.Errorf("pull request %s cannot be merged: its head branch %s was deleted; push it again to merge", ref, item.Head)
		}
		repoSettings, err := service.MergeSettings(cmd.Context())
		if err != nil {
			return err
		}
		askMethod, askDelete := method == "", !cmd.Flags().Changed("delete-branch") && !item.HeadRemoteDeleted && item.Head != ""
		if method == "" {
			method = repoSettings.Default
		} else if !manual && !slices.Contains(repoSettings.Styles, method) { // the SDK does not read allow_manual_merge, so Gitea checks manual merges
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%s merges are disabled in %s; use one of: %s", method, t.Repo, strings.Join(repoSettings.Styles, ", ")))
		}
		// The repository setting deletes the branch on Gitea only; the local branch goes when
		// --delete-branch or the form asks for it.
		deleteLocal := deleteBranch
		if !cmd.Flags().Changed("delete-branch") {
			deleteBranch = repoSettings.DeleteBranch
		}
		pull := fmt.Sprintf("#%d %q (%s → %s) in %s", n, item.Title, pullHead(item, t.Repo), item.Base, t.Repo)
		header, action := "Merge "+pull, fmt.Sprintf("merge %s#%d", t.Repo, n)
		if manual {
			header, action = "Mark "+pull+" as merged by "+commit, fmt.Sprintf("mark %s#%d as merged", t.Repo, n)
		}
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
			if askDelete {
				deleteLocal = deleteBranch
			}
		} else if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: header + "?", DefaultYes: true, NonTTY: action}); err != nil {
			return err
		}
		if useEditor {
			if editorDraft, err = a.editDraft(cmd, ""); err != nil {
				return err
			}
			message = editorDraft.Body
		}
		// Gitea ignores delete_branch_after_merge for manual merges; gt deletes the branch itself.
		if err := service.Merge(cmd.Context(), n, services.MergeOptions{Method: method, Title: title, Message: message, DeleteBranch: deleteBranch && !manual, Auto: auto, HeadSHA: item.HeadSHA, Commit: commit}); err != nil {
			if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
				switch {
				case auto && apiErr.Status == http.StatusConflict && strings.Contains(apiErr.Message, "already scheduled to auto merge"):
					a.warn(cmd, "Pull request %s is already scheduled to merge when its checks succeed", ref)
					return a.pullResult(cmd, t, item)
				case manual && strings.Contains(apiErr.Message, "merge strategy is not allowed"):
					return explain(err, "manual merges are disabled in %s; allow them with: gt repo edit --allow-manual-merge -R %s", t.Repo, t.Repo)
				case manual && strings.Contains(apiErr.Message, "commit not found in base"):
					return explain(err, "commit %s is not on the base branch %s of %s", commit, item.Base, t.Repo)
				// Gitea answers a diverged fast-forward-only merge with a server error carrying Git's hints.
				case strings.Contains(apiErr.Message, "DivergingFastForwardOnly"):
					return explain(err, "cannot fast-forward pull request %s: the base branch has commits the head lacks; rebase the head branch or choose another merge method", ref)
				}
			}
			return err
		}
		merged, err := service.View(cmd.Context(), n)
		if err != nil {
			return fmt.Errorf("pull request %s was merged, but reading it failed: %w", ref, err)
		}
		if auto && !merged.Merged {
			a.done(cmd, "Pull request %s will be merged when its checks succeed", ref)
		} else {
			if manual {
				a.done(cmd, "Marked pull request %s as merged by %s", ref, shortSHA(commit))
			} else {
				a.done(cmd, "%s pull request %s", mergeVerbs[method], ref)
			}
			if deleteBranch {
				merged = a.deleteHead(cmd, service, merged, !manual && !item.HeadRemoteDeleted, deleteLocal)
			}
		}
		return a.pullResult(cmd, t, merged)
	}}
	enumFlag(cmd, &method, "method", "s", "", "Merge method (default: the repository's default merge style)", mergeMethods...)
	cmd.Flags().BoolVar(&style.merge, "merge", false, "Create a merge commit")
	cmd.Flags().BoolVar(&style.squash, "squash", false, "Squash the commits into one commit and merge it")
	cmd.Flags().BoolVar(&style.rebase, "rebase", false, "Rebase the commits onto the base branch")
	cmd.Flags().BoolVarP(&deleteBranch, "delete-branch", "d", false, "Delete the head branch on Gitea and locally after merging (default: the repository setting, on Gitea only)")
	cmd.Flags().BoolVar(&auto, "auto", false, "Merge once the required checks succeed")
	cmd.Flags().StringVarP(&title, "title", "t", "", "Merge commit title")
	cmd.Flags().StringVarP(&message, "message", "b", "", "Merge commit message")
	cmd.Flags().StringVarP(&messageFile, "message-file", "F", "", "Read the merge commit message from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Write the merge commit message in your editor")
	cmd.Flags().StringVar(&commit, "commit", "", "Commit `sha` on the base branch that merged the pull request outside Gitea (with --method manually-merged)")
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if strings.Contains(err.Error(), "flag needs an argument: 's' in -s") {
			return clierr.Wrap(clierr.Usage, errors.New("-s takes a method (merge, rebase, rebase-merge, squash, fast-forward-only or manually-merged); for gh-style use --squash"))
		}
		return c.Root().FlagErrorFunc()(c, err)
	})
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	cmd.Example = `# Merge the pull request of the current branch with the repository's default style
$ gt pr merge

# Squash #13 and delete its branch
$ gt pr merge 13 --squash --delete-branch

# Merge once the checks pass, without prompts
$ gt pr merge 13 --auto --yes

# Mark #13 as merged by a commit pushed to the base branch outside Gitea
$ gt pr merge 13 --method manually-merged --commit 4b825dc --delete-branch`
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
