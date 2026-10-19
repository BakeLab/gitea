// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
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
	options                                                 services.PullOptions
	bodyFile, deadline, milestone, topic                    string
	labelNames, reviewers, reviewerTeams, attachments       []string
	useEditor, draft, agit, allowMaintainerEdits, fill, web bool
}

func (a *application) pullCreateCommand() *cobra.Command {
	var f pullCreateFlags
	create := &cobra.Command{Use: "create", Aliases: []string{"new"}, Short: "Create a pull request", Long: `Create a pull request from the current branch, or from --head.

On a terminal without flags, gt asks for the title and body, then what to do next: submit,
submit as a draft, add metadata or change the branches. --fill skips the questions and uses
the commit message of a single-commit branch, or the branch name, as the title. A branch that
is not on the remote yet is pushed first, with its upstream set.`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		return a.runPullCreate(cmd, &f)
	}}
	create.Flags().StringSliceVarP(&f.options.Assignees, "assignee", "a", nil, "Assign users by username; @me for yourself")
	create.Flags().StringSliceVarP(&f.labelNames, "label", "l", nil, "Apply labels by name; repo: or org: picks between same-named labels")
	create.Flags().StringVarP(&f.milestone, "milestone", "m", "", "Milestone title or ID")
	create.Flags().StringVarP(&f.options.Title, "title", "t", "", "Pull request title (default: the commit message of a single-commit branch, or the branch name)")
	create.Flags().StringVar(&f.deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp")
	create.Flags().StringVarP(&f.options.Body, "body", "b", "", "Pull request body")
	create.Flags().StringVarP(&f.bodyFile, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	create.Flags().BoolVarP(&f.useEditor, "editor", "e", false, "Write the body in your editor, starting from the repository's template")
	create.Flags().StringVarP(&f.options.Head, "head", "H", "", "Head branch, or OWNER:BRANCH for a fork (default: the current branch)")
	create.Flags().StringVarP(&f.options.Base, "base", "B", "", "Base branch (default: the repository's default branch)")
	create.Flags().BoolVarP(&f.draft, "draft", "d", false, "Create a draft pull request")
	create.Flags().BoolVarP(&f.fill, "fill", "f", false, "Skip the questions and take the title and body from the commits")
	create.Flags().BoolVarP(&f.web, "web", "w", false, "Open the comparison page in the browser to create the pull request there")
	create.Flags().BoolVar(&f.allowMaintainerEdits, "allow-maintainer-edits", true, "Allow maintainers to push to the head branch")
	create.Flags().BoolVar(&f.agit, "agit", false, "Create the pull request through AGit push (refs/for)")
	create.Flags().StringSliceVarP(&f.reviewers, "reviewer", "r", nil, "Request reviews from users; @me for yourself")
	create.Flags().StringSliceVar(&f.reviewerTeams, "reviewer-team", nil, "Request reviews from teams within the repository's organization")
	create.Flags().StringArrayVar(&f.attachments, "attachment", nil, "Attach a file to the pull request (repeatable)")
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
			return clierr.Wrap(clierr.Usage, errors.New("--json/--jq cannot be combined with --agit; AGit creates the pull request through git push"))
		}
		for _, name := range []string{"assignee", "label", "milestone", "deadline", "reviewer", "reviewer-team", "attachment"} {
			if cmd.Flags().Changed(name) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be combined with --agit; AGit pushes carry only a title and body", name))
			}
		}
	} else if f.topic != "" {
		return clierr.Wrap(clierr.Usage, errors.New("--topic requires --agit"))
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
	reviews := services.ReviewRequestChanges{Add: services.ReviewRequestInput{Users: f.reviewers, Teams: f.reviewerTeams}}
	requestReviews := len(f.reviewers)+len(f.reviewerTeams) > 0
	if requestReviews {
		if err := reviews.Validate(); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
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
	t.expandMeAll(f.reviewers)
	if f.options.Head == "" {
		owner, branch := a.defaultPullHead(cmd.Context())
		if branch == "" {
			return clierr.Wrap(clierr.Usage, errors.New("supply --head outside a branch checkout"))
		}
		f.options.Head = branch
		if !f.agit && owner != "" && !strings.EqualFold(owner, t.owner()) {
			f.options.Head = owner + ":" + branch
		}
	}
	if f.options.Base == "" {
		repo, err := (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo)
		if err != nil {
			return err
		}
		if f.options.Base = repo.DefaultBranch; f.options.Base == "" {
			return clierr.Wrap(clierr.Usage, errors.New("supply --base; repository has no default branch"))
		}
	}
	if f.web {
		return a.openBrowser(cmd, repositoryURL(t)+"/compare/"+compareRef(f.options.Base)+"..."+compareRef(f.options.Head))
	}
	if !f.agit {
		if err := refuseSameBranch(f.options.Head, f.options.Base); err != nil {
			return err
		}
	}
	var commitBody string
	if f.options.Title == "" {
		if subject, body, ok := a.singleCommitMessage(cmd.Context(), f.options.Base, f.options.Head); ok {
			f.options.Title, commitBody = subject, body
		} else if f.options.Title = terminal.DefaultPullTitle(f.options.Head); f.options.Title == "" {
			return clierr.Wrap(clierr.Usage, errors.New("supply --title"))
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
		return a.pushAgit(cmd, t, s, git.AgitOptions{Remote: a.options.remote, Head: f.options.Head, Base: f.options.Base, Topic: f.topic, Title: f.options.Title, Body: f.options.Body})
	}
	f.options.AllowMaintainerEdit = &f.allowMaintainerEdits
	reviewService := services.Reviews{Client: t.client, Repository: t.Repo}
	if len(f.reviewerTeams) > 0 {
		if err := reviewService.CheckRequestTeams(cmd.Context(), f.reviewerTeams); err != nil {
			return err
		}
	}
	if err := a.ensureHeadPublished(cmd, t, s, f.options.Head); err != nil {
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
	for _, file := range files {
		if _, err := (services.Issues{Client: t.client, Repository: t.Repo}).UploadAttachment(cmd.Context(), item.Number, filepath.Base(file.Name()), file); err != nil {
			failures = append(failures, fmt.Errorf("upload %s: %w", file.Name(), err))
		}
	}
	if err := a.reportCreatedPull(cmd, t, item); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("pull request #%d was created, but follow-up steps failed: %w", item.Number, errors.Join(failures...))
	}
	return nil
}

// pushAgit creates a pull request by pushing the head branch to refs/for/<base>/<topic>.
// Without --remote it pushes to the one remote that matches the resolved repository.
func (a *application) pushAgit(cmd *cobra.Command, t target, s settings.Snapshot, opts git.AgitOptions) error {
	if _, branch, ok := strings.Cut(opts.Head, ":"); ok {
		opts.Head = branch
	}
	if opts.Topic == "" {
		opts.Topic = opts.Head
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
	return git.PushAgitPR(cmd.Context(), a.deps.Git, opts)
}

func (a *application) defaultPullHead(ctx context.Context) (owner, branch string) {
	probe := a.gitProbe()
	branch, remote := git.CurrentUpstreamRemote(ctx, probe, a.options.cwd)
	if branch == "" {
		return "", ""
	}
	if remote == "" {
		return "", branch
	}
	raw, err := probe.Output(ctx, a.options.cwd, false, "remote", "get-url", remote)
	if err != nil || raw == "" {
		return "", branch
	}
	snapshot, err := a.settings()
	if err != nil {
		return "", branch
	}
	_, repo, err := settings.RepositoryURL(raw, snapshot.Hosts.Hosts)
	if err != nil {
		return "", branch
	}
	owner, _, _ = strings.Cut(repo, "/")
	return owner, branch
}

// singleCommitMessage returns the subject and body of the only commit head has ahead of base.
// Any Git failure, or a head with zero or several commits, reports ok=false.
func (a *application) singleCommitMessage(ctx context.Context, base, head string) (subject, body string, ok bool) {
	if _, branch, found := strings.Cut(head, ":"); found {
		head = branch
	}
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
func refuseSameBranch(head, base string) error {
	if head != "" && head == base {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("must be on a branch named differently than %q; create a branch or pass --head", base))
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
	}
	choices.Milestones, choices.Labels = milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	values, err := a.deps.PullForm.CreatePull(cmd.Context(), choices)
	defer finishDraft(&values.Draft, &runErr)
	if err != nil {
		return err
	}
	if values.Base == "" {
		return clierr.Wrap(clierr.Usage, errors.New("supply a target branch; repository has no default branch"))
	}
	if values.Agit && a.options.json != "" {
		return clierr.Wrap(clierr.Usage, errors.New("--json/--jq cannot be combined with AGit create; AGit creates the pull request through git push"))
	}
	if values.AsDraft {
		values.Title = services.AddDraftPrefix(values.Title)
	}
	if values.Agit {
		return a.pushAgit(cmd, target, snapshot, git.AgitOptions{Remote: a.options.remote, Head: values.Head, Base: values.Base, Topic: values.Topic, Title: values.Title, Body: values.Body})
	}
	options := services.PullOptions{
		Title: values.Title, Body: values.Body, Assignees: values.Assignees, Milestone: values.Milestone,
		Labels: values.Labels, Deadline: values.Deadline, Base: values.Base, Head: values.Head,
		AllowMaintainerEdit: &values.AllowMaintainerEdits,
	}
	if err := refuseSameBranch(options.Head, options.Base); err != nil {
		return err
	}
	if err := a.ensureHeadPublished(cmd, target, snapshot, options.Head); err != nil {
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
	return a.created(cmd, item, item.URL)
}

// compareRef escapes a branch name, or OWNER:BRANCH, for a compare URL, where a '#' or '%' in the
// name would otherwise start a fragment or an invalid escape.
func compareRef(ref string) string {
	return strings.ReplaceAll(url.PathEscape(ref), "%2F", "/")
}
