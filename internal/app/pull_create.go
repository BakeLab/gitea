// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

type pullCreateFlags struct {
	options                                                     services.PullOptions
	bodyFile, deadline, milestone, topic                        string
	labelNames, reviewers, reviewerTeams, attachments, projects []string
	useEditor, draft, agit, allowMaintainerEdits, fill, web     bool
}

func (a *application) pullCreateCommand() *cobra.Command {
	var f pullCreateFlags
	create := &cobra.Command{Use: "create", Aliases: []string{"new"}, Short: "Create a pull request", Long: `Create a pull request from the current branch, or from --head.

On a terminal without flags, gt asks for the title and body, then what to do next: submit,
submit as a draft, add metadata or change the branches. Any flag of this command, such as
--fill, --head or --base (but not global flags such as --repo), skips the questions: the
title is then --title, else the commit message of a single-commit branch, else the branch
name.

A branch that is not on the remote yet is pushed first, with its upstream set, after a
confirmation; without a terminal, pass --yes to push it. A branch that
does not track a branch of the same name yet goes to the fork when origin is a fork of the
repository, or, when origin is not the repository itself or you cannot push to it, to the only
remote of your own fork; otherwise it goes to the repository's remote. Without push access and
a fork remote, gt offers on a terminal to fork the repository and adds the fork as the remote
"fork", or one named after your login when "fork" points elsewhere; elsewhere, run gt repo fork
--add-remote first. A missing base branch fails before the push. After a push to a fork, gt pr
commands on the branch keep selecting the repository rather than the fork.

With --agit, only the author of an AGit pull request can push to it, so on a branch of someone
else's, such as from gt pr checkout, pass --topic to open your own.`, Annotations: map[string]string{"gt.confirm": "true"}, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		return a.runPullCreate(cmd, &f)
	}}
	create.Flags().StringSliceVarP(&f.options.Assignees, "assignee", "a", nil, "Assign users by username; @me for yourself")
	create.Flags().StringSliceVarP(&f.labelNames, "label", "l", nil, "Apply labels by name; repo: or org: picks between same-named labels")
	create.Flags().StringVarP(&f.milestone, "milestone", "m", "", "Milestone title or ID")
	create.Flags().StringVarP(&f.options.Title, "title", "t", "", "Pull request title (default: the commit message of a single-commit branch, or the branch name)")
	create.Flags().StringVar(&f.deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339 or Unix timestamp; only the date counts, as the end of that day in UTC")
	create.Flags().StringVarP(&f.options.Body, "body", "b", "", "Pull request body")
	create.Flags().StringVarP(&f.bodyFile, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	create.Flags().BoolVarP(&f.useEditor, "editor", "e", false, "Write the body in your editor, starting from the repository's template")
	create.Flags().StringVarP(&f.options.Head, "head", "H", "", "Head branch, or OWNER:BRANCH for a fork (default: the current branch)")
	create.Flags().StringVarP(&f.options.Base, "base", "B", "", "Base branch (default: the repository's default branch)")
	create.Flags().BoolVarP(&f.draft, "draft", "d", false, "Create a draft pull request")
	create.Flags().BoolVarP(&f.fill, "fill", "f", false, "Skip the questions and take the title and body from the commits")
	create.Flags().BoolVarP(&f.web, "web", "w", false, "Open the comparison of --head and --base in the browser to create the pull request there")
	create.Flags().BoolVar(&f.allowMaintainerEdits, "allow-maintainer-edits", true, "Allow maintainers to push to the head branch")
	create.Flags().BoolVar(&f.agit, "agit", false, "Create the pull request through AGit push (refs/for)")
	create.Flags().StringSliceVarP(&f.reviewers, "reviewer", "r", nil, "Request reviews from users")
	create.Flags().StringSliceVar(&f.reviewerTeams, "reviewer-team", nil, "Request reviews from teams within the repository's organization")
	create.Flags().StringArrayVar(&f.attachments, "attachment", nil, "Attach a file to the pull request (repeatable)")
	create.Flags().StringArrayVarP(&f.projects, "project", "p", nil, "Add the pull request to the repository's or its owner's project with this ID or `title`, a number being an ID first (repeatable)")
	create.Flags().StringVar(&f.topic, "topic", "", "AGit topic name (default: head branch); requires --agit")
	_ = create.RegisterFlagCompletionFunc("assignee", a.completeUsers())
	_ = create.RegisterFlagCompletionFunc("reviewer", a.completeUsers())
	_ = create.RegisterFlagCompletionFunc("label", a.completeLabels())
	_ = create.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	_ = create.RegisterFlagCompletionFunc("base", a.completeBranches())
	_ = create.RegisterFlagCompletionFunc("head", a.completeBranches())
	create.Example = `# Create a pull request for the current branch interactively
$ gt pr create

# Take the title and body from the commits, without questions
$ gt pr create --fill

# In a script, also push the branch without asking
$ gt pr create --fill --yes

$ gt pr create -t "Add login" -b "Closes #1" -B develop -r carol -l enhancement`
	return create
}

func (a *application) runPullCreate(cmd *cobra.Command, f *pullCreateFlags) (runErr error) {
	var editorDraft terminal.Draft
	defer finishDraft(&editorDraft, &runErr)
	if err := validateBodySources(cmd); err != nil {
		return err
	}
	if f.agit {
		if a.options.json != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--json, --jq or --csv cannot be combined with --agit; AGit creates the pull request through git push"))
		}
		if f.web {
			return clierr.Wrap(clierr.Usage, errors.New("--web cannot be combined with --agit; AGit creates the pull request through git push, without a branch to compare"))
		}
		if err := onlyFlagsWith(cmd, "agit", "AGit pushes carry only a title and body", "title", "body", "body-file", "editor", "head", "base", "draft", "fill", "topic"); err != nil {
			return err
		}
		if f.topic != "" && f.topic == f.options.Base {
			return agitTopicError(f.topic)
		}
	} else if f.topic != "" {
		return clierr.Wrap(clierr.Usage, errors.New("--topic requires --agit"))
	}
	if f.web {
		if err := requireWebFlags(cmd, "head", "base"); err != nil {
			return err
		}
	}
	if id, err := strconv.ParseInt(f.milestone, 10, 64); err == nil && id < 0 {
		return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a milestone title or a positive ID"))
	}
	if err := requireNames("label", "", f.labelNames); err != nil {
		return err
	}
	if err := requireNames("assignee", "", f.options.Assignees); err != nil {
		return err
	}
	trimNames(f.reviewers, f.reviewerTeams)
	if slices.Contains(f.reviewers, "@me") {
		return clierr.Wrap(clierr.Usage, errors.New("--reviewer cannot include @me; Gitea does not let the author review their pull request"))
	}
	reviews := services.ReviewRequestChanges{Add: services.ReviewRequestInput{Users: f.reviewers, Teams: f.reviewerTeams}}
	requestReviews := len(f.reviewers)+len(f.reviewerTeams) > 0
	if requestReviews {
		if err := reviews.Validate(); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
	}
	if err := requireNames("project", "", f.projects); err != nil {
		return err
	}
	files, err := openRegularFiles(f.attachments)
	defer closeFiles(files)
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("deadline") {
		if f.options.Deadline, err = parseDeadline(f.deadline); err != nil {
			return err
		}
	}
	s, err := a.settings()
	if err != nil {
		return err
	}
	if !localFlagsChanged(cmd) && a.policy(s).CanPrompt(a.deps.Streams) && a.deps.PullForm != nil {
		return a.createPullInteractively(cmd, s)
	}
	t, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	f.options.Labels, err = (services.Labels{Client: t.client, Repository: t.Repo}).ResolveIssueNames(cmd.Context(), f.labelNames)
	if err != nil {
		return err
	}
	if f.milestone != "" && f.milestone != "0" {
		if f.options.Milestone, err = milestoneID(cmd, t, f.milestone); err != nil {
			return err
		}
	}
	t.expandMeAll(f.options.Assignees)
	if f.options.Head == "" {
		owner, branch := a.defaultPullHead(cmd.Context())
		if branch == "" {
			return clierr.Wrap(clierr.Usage, errors.New("pr create requires --head outside a branch checkout"))
		}
		f.options.Head = branch
		if !f.agit && owner != "" && !strings.EqualFold(owner, t.owner()) {
			f.options.Head = owner + ":" + branch
		}
	}
	base := &pullBase{}
	if f.options.Base == "" {
		repo, err := base.repository(cmd.Context(), t)
		if err != nil {
			return err
		}
		if err := repo.CheckEnabled(); err != nil {
			return err
		}
		if f.options.Base = repo.DefaultBranch; f.options.Base == "" {
			return clierr.Wrap(clierr.Usage, errors.New("pr create requires --base: the repository has no default branch"))
		}
	}
	base.branch = f.options.Base
	if !f.agit {
		if err := refuseSameBranch(f.options.Head, f.options.Base, cmd.Flags().Changed("head")); err != nil {
			return err
		}
		if err := a.refuseEmptyPull(cmd.Context(), t, f.options.Base, f.options.Head); err != nil {
			return err
		}
	}
	if f.web {
		// The comparison page needs the head on Gitea, as OWNER:BRANCH when it went to a fork.
		if f.options.Head, err = a.ensureHeadPublished(cmd, t, s, f.options.Head, base); err != nil {
			return err
		}
		return a.openBrowser(cmd, repositoryURL(t)+"/compare/"+compareRef(f.options.Base)+"..."+compareRef(f.options.Head))
	}
	var commitBody string
	if f.options.Title == "" {
		if subject, body, ok := a.singleCommitMessage(cmd.Context(), f.options.Base, a.localHead(cmd.Context(), t, f.options.Head)); ok {
			f.options.Title, commitBody = subject, body
		} else if f.options.Title = terminal.DefaultPullTitle(f.options.Head); f.options.Title == "" {
			return clierr.Wrap(clierr.Usage, errors.New("pr create requires --title"))
		}
	}
	service := services.Pulls{Client: t.client, Repository: t.Repo}
	if f.useEditor {
		var seed string
		if seed, err = service.Template(cmd.Context(), f.options.Base); err != nil {
			return err
		}
		if seed == "" {
			seed = commitBody
		}
		editorDraft, err = a.editDraft(cmd, seed)
		f.options.Body = editorDraft.Body
	} else if !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
		f.options.Body = commitBody
	} else {
		f.options.Body, err = a.readBody(cmd, f.options.Body, f.bodyFile)
	}
	if err != nil {
		return err
	}
	if f.draft {
		f.options.Title = services.AddDraftPrefix(f.options.Title)
	}
	if f.agit {
		return a.pushAgit(cmd, t, s, base, git.AgitOptions{Remote: a.options.remote, Head: f.options.Head, Base: f.options.Base, Topic: f.topic, Title: f.options.Title, Body: f.options.Body})
	}
	f.options.AllowMaintainerEdit = &f.allowMaintainerEdits
	reviewService := services.Reviews{Client: t.client, Repository: t.Repo}
	// Unknown assignees and reviewers fail before the head is pushed and the pull request exists.
	if err := service.CheckUsers(cmd.Context(), f.options.Assignees, f.reviewers); err != nil {
		return err
	}
	if len(f.reviewerTeams) > 0 {
		if err := reviewService.CheckRequestTeams(cmd.Context(), f.reviewerTeams); err != nil {
			return err
		}
	}
	// Projects are resolved first, so that a missing one fails before the pull request exists.
	boards, err := loadProjectChanges(cmd.Context(), t, f.projects, nil, false)
	if err != nil {
		return err
	}
	if f.options.Head, err = a.ensureHeadPublished(cmd, t, s, f.options.Head, base); err != nil {
		return err
	}
	item, err := service.Create(cmd.Context(), f.options)
	if err != nil {
		return err
	}
	var failures []error
	if requestReviews {
		if updated, err := reviewService.ChangeRequests(cmd.Context(), item.Number, reviews); err != nil {
			failures = append(failures, fmt.Errorf("request reviews: %w", err))
		} else {
			item = updated
		}
	}
	failures = append(failures, uploadAttachments(cmd.Context(), files, item.Number, services.Issues{Client: t.client, Repository: t.Repo}.UploadAttachment, nil)...)
	if _, err := boards.applyToNumber(cmd.Context(), item.Number); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(followUpError(fmt.Sprintf("pull request #%d", item.Number), "created", failures), a.reportCreatedPull(cmd, t, item))
}

// pushAgit creates a pull request by pushing the head branch to refs/for/<base>/<topic>.
// Without --remote it pushes to the one remote that matches the resolved repository.
func (a *application) pushAgit(cmd *cobra.Command, t target, s settings.Snapshot, base *pullBase, opts git.AgitOptions) error {
	if _, branch, ok := strings.Cut(opts.Head, ":"); ok {
		opts.Head = branch
	}
	topicGiven := opts.Topic != ""
	if !topicGiven {
		opts.Topic = opts.Head
	}
	if opts.Topic == opts.Base {
		return agitTopicError(opts.Topic)
	}
	var err error
	if opts.Remote == "" {
		if opts.Remote, err = a.checkoutRemote(cmd.Context(), t, s); err != nil {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("AGit push: %w", err))
		}
	}
	if opts.CredentialArgs, err = a.remoteCredentials(cmd.Context(), t, opts.Remote); err != nil {
		return err
	}
	opts.Directory, opts.Prompt = a.options.cwd, a.policy(s).CanPrompt(a.deps.Streams)
	if err := base.checkEnabled(cmd.Context(), t); err != nil {
		return err
	}
	if !topicGiven {
		if err := a.refuseForeignAgitBranch(cmd.Context(), t, opts.Head); err != nil {
			return err
		}
	}
	// Gitea refuses a push for several reasons, such as one the topic already has or one that drops
	// commits, and does not say which topic a pull request has, so no refusal is taken as up to date.
	if err := git.PushAgitPR(cmd.Context(), a.deps.Git, opts); err != nil {
		if _, isGit := errors.AsType[*git.CommandError](err); isGit {
			return fmt.Errorf("Git did not push %s to refs/for/%s/%s; see its output above", opts.Head, opts.Base, opts.Topic)
		}
		return err
	}
	pull, ok := a.recordAgitPull(cmd.Context(), t, opts)
	if !ok {
		a.warn(cmd, "Pushed %s, but could not find its pull request among your open AGit pull requests into %s", opts.Head, opts.Base)
		return nil
	}
	return a.reportCreatedPull(cmd, t, pull)
}

// refuseForeignAgitBranch refuses to push head to its own name as topic when it is the branch of
// another user's open AGit pull request, such as from pr checkout: only the author can push to
// one, so the push would quietly open another pull request.
func (a *application) refuseForeignAgitBranch(ctx context.Context, t target, head string) error {
	if strings.HasPrefix(head, "refs/") {
		return nil
	}
	number := git.ReadBranchConfig(ctx, a.gitProbe(), a.options.cwd, head).PullNumber
	if number == 0 {
		return nil
	}
	pull, viewErr := (services.Pulls{Client: t.client, Repository: t.Repo}).View(ctx, number)
	if viewErr != nil || pull.State != "open" || pull.Head != "" || pull.HeadRemoteDeleted {
		return nil
	}
	if account, err := services.CurrentAccount(ctx, t.client); err != nil || strings.EqualFold(pull.Author, account) {
		return err
	}
	return fmt.Errorf("branch %s belongs to AGit pull request %s by %s, which only its author can push to; pass --topic to open your own pull request",
		head, refLabel(t.Repo, number, pull.Title), pull.Author)
}

func agitTopicError(topic string) error {
	return clierr.Wrap(clierr.Usage, fmt.Errorf("the AGit topic %s must differ from the base branch; pass another --topic", topic))
}

// recordAgitPull records the AGit pull request just pushed from the head branch in that branch's
// configuration, like pr checkout does: an AGit pull request has no head branch, so this is how
// pr commands on the branch, and pr clean --ignore-sha, find it. It returns that pull request, so
// that its URL is printed like for other created pull requests. It is best effort: it looks for the
// user's own open AGit pull request at the branch's commit among the most recent ones, and finds
// none when there are several, as Gitea does not say which topic a pull request has.
func (a *application) recordAgitPull(ctx context.Context, t target, opts git.AgitOptions) (services.Pull, bool) {
	if strings.HasPrefix(opts.Head, "refs/") {
		return services.Pull{}, false
	}
	probe := a.gitProbe()
	sha, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+opts.Head+"^{commit}")
	if err != nil {
		return services.Pull{}, false
	}
	pulls, _, err := (services.Pulls{Client: t.client, Repository: t.Repo}).List(ctx, services.PullListOptions{State: "open", Limit: 50})
	if err != nil {
		return services.Pull{}, false
	}
	account, err := services.CurrentAccount(ctx, t.client)
	if err != nil {
		return services.Pull{}, false
	}
	mine := slices.DeleteFunc(pulls, func(pull services.Pull) bool {
		return pull.Head != "" || pull.HeadRemoteDeleted || pull.HeadSHA != sha || pull.Base != opts.Base || !strings.EqualFold(pull.Author, account)
	})
	if len(mine) != 1 {
		return services.Pull{}, false
	}
	_ = git.RecordPull(ctx, probe, a.options.cwd, opts.Head, mine[0].Number, opts.Remote)
	return mine[0], true
}

func (a *application) defaultPullHead(ctx context.Context) (owner, branch string) {
	probe := a.gitProbe()
	config, err := git.CurrentBranchConfig(ctx, probe, a.options.cwd)
	if err != nil {
		return "", ""
	}
	branch = config.Name
	if config.Remote == "" || config.Merge != branch { // a branch started from another, such as origin/main, is pushed on its own
		return "", branch
	}
	raw, err := probe.Output(ctx, a.options.cwd, false, "remote", "get-url", config.Remote)
	if err != nil || raw == "" {
		return "", branch
	}
	snapshot, err := a.settings()
	if err != nil {
		return "", branch
	}
	_, repo, err := settings.RemoteRepository(raw, snapshot.Hosts.Hosts)
	if err != nil {
		return "", branch
	}
	owner, _, _ = strings.Cut(repo, "/")
	return owner, branch
}

// singleCommitMessage returns the subject and body of the only commit the local branch head has
// ahead of base.
// Any Git failure, or a head with zero or several commits, reports ok=false.
func (a *application) singleCommitMessage(ctx context.Context, base, head string) (subject, body string, ok bool) {
	if base == "" || head == "" || strings.HasPrefix(base, "-") || strings.HasPrefix(head, "-") {
		return "", "", false
	}
	probe := a.gitProbe()
	remotes := []string{"upstream", "origin"}
	if a.options.remote != "" {
		remotes = []string{a.options.remote}
	}
	count, err := "", errors.New("no base reference")
	for _, ref := range append(remotes, "") {
		if ref != "" {
			ref += "/"
		}
		if count, err = probe.Output(ctx, a.options.cwd, false, "rev-list", "--count", ref+base+".."+head); err == nil {
			break
		}
	}
	if err != nil || count != "1" {
		return "", "", false
	}
	message, err := probe.Output(ctx, a.options.cwd, false, "log", "-1", "--format=%s%n%n%b", head)
	if err != nil {
		return "", "", false
	}
	subject, body, _ = strings.Cut(message, "\n")
	subject, body = strings.TrimSpace(subject), strings.TrimSpace(body)
	return subject, body, subject != ""
}

// refuseSameBranch rejects a head that is the base branch of the same repository, before anything
// is pushed: such a pull request has no commits, and pushing would publish the base branch itself.
func refuseSameBranch(head, base string, explicit bool) error {
	if head == "" || head != base {
		return nil
	}
	if explicit {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--head cannot be the base branch %q", base))
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("must be on a branch named differently than %q; create a branch or pass --head", base))
}

// localHead returns the local branch that holds head, or "" when head is OWNER:BRANCH and the local
// branch BRANCH does not track its namesake on a remote of OWNER, being someone else's branch.
func (a *application) localHead(ctx context.Context, t target, head string) string {
	owner, branch, cross := strings.Cut(head, ":")
	if !cross {
		return head
	}
	config := git.ReadBranchConfig(ctx, a.gitProbe(), a.options.cwd, branch)
	tracked, _, _ := strings.Cut(a.remoteRepository(ctx, t, config.Remote), "/")
	if config.Merge != branch || tracked == "" || !strings.EqualFold(tracked, owner) {
		return ""
	}
	return branch
}

// refuseEmptyPull rejects a local head branch without commits ahead of the base branch, before
// anything is pushed, since Gitea would open a pull request without changes. It compares with the
// remote-tracking base branch of the base repository and lets the head pass when Git cannot.
func (a *application) refuseEmptyPull(ctx context.Context, t target, base, head string) error {
	head = a.localHead(ctx, t, head)
	if base == "" || head == "" || strings.HasPrefix(base, "-") || strings.HasPrefix(head, "-") {
		return nil
	}
	probe := a.gitProbe()
	names, err := probe.Output(ctx, a.options.cwd, false, "remote")
	if err != nil {
		return nil
	}
	for remote := range strings.FieldsSeq(names) {
		if !a.remoteOwns(ctx, t, remote) {
			continue
		}
		count, err := probe.Output(ctx, a.options.cwd, false, "rev-list", "--count", "refs/remotes/"+remote+"/"+base+"..refs/heads/"+head)
		if err != nil {
			continue
		}
		if count == "0" {
			return fmt.Errorf("no commits between %s and %s; commit your changes to %s first", base, head, head)
		}
		return nil
	}
	return nil
}

func (a *application) createPullInteractively(cmd *cobra.Command, snapshot settings.Snapshot) (runErr error) {
	target, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	service := services.Pulls{Client: target.client, Repository: target.Repo}
	loaded, err := service.CreateChoices(cmd.Context())
	if err != nil {
		return err
	}
	repo, selectables := loaded.Repository, loaded.CreateSelectables
	baseOwner := target.owner()
	headOwner, headBranch := a.defaultPullHead(cmd.Context())
	title, body, _ := a.singleCommitMessage(cmd.Context(), repo.DefaultBranch, headBranch)
	if loaded.Template != "" {
		body = loaded.Template
	}
	choices := terminal.PullFormChoices{
		Repo: target.Repo, BaseOwner: baseOwner, Base: repo.DefaultBranch, HeadOwner: headOwner, HeadBranch: headBranch, Title: title, Body: body,
		AllowMaintainerEdits: true, CanPush: selectables.CanPush, Assignees: selectables.Assignees,
		EditBody: a.bodyEditor(cmd, terminal.BodyTitle),
		SeedBody: func(ctx context.Context, base, headBranch string) (string, error) {
			template, err := loaded.Template, error(nil)
			if base != repo.DefaultBranch {
				template, err = service.Template(ctx, base)
			}
			if template != "" || err != nil {
				return template, err
			}
			_, body, _ := a.singleCommitMessage(ctx, base, headBranch)
			return body, nil
		},
	}
	choices.Milestones, choices.Labels = milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	values, err := a.deps.PullForm.CreatePull(cmd.Context(), choices)
	defer finishDraft(&values.Draft, &runErr)
	if err != nil {
		return err
	}
	if values.Base == "" {
		return clierr.Wrap(clierr.Usage, errors.New("pr create requires a target branch: the repository has no default branch"))
	}
	if values.Agit && a.options.json != "" {
		return clierr.Wrap(clierr.Usage, errors.New("--json, --jq or --csv cannot be combined with AGit create; AGit creates the pull request through git push"))
	}
	base := &pullBase{branch: values.Base, repo: &loaded.Base}
	if values.AsDraft {
		values.Title = services.AddDraftPrefix(values.Title)
	}
	if values.Agit {
		return a.pushAgit(cmd, target, snapshot, base, git.AgitOptions{Remote: a.options.remote, Head: values.Head, Base: values.Base, Topic: values.Topic, Title: values.Title, Body: values.Body})
	}
	if values.Deadline != nil {
		due := endOfDay(*values.Deadline)
		values.Deadline = &due
	}
	options := services.PullOptions{
		Title: values.Title, Body: values.Body, Assignees: values.Assignees, Milestone: values.Milestone,
		Labels: values.Labels, Deadline: values.Deadline, Base: values.Base, Head: values.Head,
		AllowMaintainerEdit: &values.AllowMaintainerEdits,
	}
	if err := refuseSameBranch(options.Head, options.Base, false); err != nil {
		return err
	}
	if err := a.refuseEmptyPull(cmd.Context(), target, options.Base, options.Head); err != nil {
		return err
	}
	if options.Head, err = a.ensureHeadPublished(cmd, target, snapshot, options.Head, base); err != nil {
		return err
	}
	item, err := service.Create(cmd.Context(), options)
	if err != nil {
		return err
	}
	return a.reportCreatedPull(cmd, target, item)
}

// reportCreatedPull reports a new pull request and prints its URL, or the pull request as JSON.
func (a *application) reportCreatedPull(cmd *cobra.Command, t target, item services.Pull) error {
	a.done(cmd, "Created pull request %s", refLabel(t.Repo, item.Number, item.Title))
	if a.options.json != "" {
		var err error
		if item, err = a.pullWithReviews(cmd, t, item); err != nil {
			return err
		}
	}
	return a.created(cmd, item, item.URL)
}

// compareRef escapes a branch name, or OWNER:BRANCH, for a compare URL, where a '#' or '%' in the
// name would otherwise start a fragment or an invalid escape.
func compareRef(ref string) string {
	return strings.ReplaceAll(url.PathEscape(ref), "%2F", "/")
}
