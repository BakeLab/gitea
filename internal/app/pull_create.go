// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/git"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/settings"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullCreateCommand() *cobra.Command {
	var options services.PullOptions
	var bodyFile, deadline, milestoneName, topic string
	var labelNames, reviewers, reviewerTeams, attachments []string
	var useEditor, draft, agit, allowMaintainerEdits bool
	create := &cobra.Command{Use: "create", Short: "Create a pull request", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		if agit {
			if a.options.json != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--json/--jq cannot be combined with --agit; AGit creates the pull request through git push"))
			}
			for _, name := range []string{"assignee", "label", "milestone", "milestone-name", "deadline", "reviewer", "reviewer-team", "attachment"} {
				if cmd.Flags().Changed(name) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be combined with --agit; AGit pushes carry only a title and body", name))
				}
			}
		} else if topic != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--topic requires --agit"))
		}
		if err := validateMilestoneFlags(cmd, milestoneName, options.Milestone); err != nil {
			return err
		}
		if err := requireNames("label", "", labelNames); err != nil {
			return err
		}
		if err := requireNames("assignee", "", options.Assignees); err != nil {
			return err
		}
		trimNames(reviewers, reviewerTeams)
		reviews := services.ReviewRequestChanges{Add: services.ReviewRequestInput{Users: reviewers, Teams: reviewerTeams}}
		requestReviews := len(reviewers)+len(reviewerTeams) > 0
		if requestReviews {
			if err := reviews.Validate(); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
		}
		files, err := openRegularFiles(attachments)
		defer closeFiles(files)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("deadline") {
			if options.Deadline, _, err = parseDeadline(deadline); err != nil {
				return err
			}
		}
		s, err := a.deps.Settings.Load()
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
		options.Labels, err = (services.Labels{Client: t.client, Repository: t.Repo}).ResolveIssueNames(cmd.Context(), labelNames)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("milestone-name") {
			if options.Milestone, err = (services.Milestones{Client: t.client, Repository: t.Repo}).ResolveName(cmd.Context(), milestoneName); err != nil {
				return err
			}
		}
		if options.Head == "" {
			owner, branch := a.defaultPullHead(cmd.Context())
			if branch == "" {
				return clierr.Wrap(clierr.Usage, errors.New("supply --head outside a branch checkout"))
			}
			options.Head = branch
			if baseOwner, _, _ := strings.Cut(t.Repo, "/"); !agit && owner != "" && !strings.EqualFold(owner, baseOwner) {
				options.Head = owner + ":" + branch
			}
		}
		if options.Base == "" {
			repo, err := (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo)
			if err != nil {
				return err
			}
			if options.Base = repo.DefaultBranch; options.Base == "" {
				return clierr.Wrap(clierr.Usage, errors.New("supply --base; repository has no default branch"))
			}
		}
		var commitBody string
		if options.Title == "" {
			if subject, body, ok := a.singleCommitMessage(cmd.Context(), options.Base, options.Head); ok {
				options.Title, commitBody = subject, body
			} else if options.Title = terminal.DefaultPullTitle(options.Head); options.Title == "" {
				return clierr.Wrap(clierr.Usage, errors.New("supply --title"))
			}
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		if useEditor {
			var seed string
			if seed, err = service.Template(cmd.Context(), options.Base); err != nil {
				return err
			}
			if seed == "" {
				seed = commitBody
			}
			editorDraft, err = a.editDraft(cmd, seed)
			options.Body = editorDraft.Body
		} else if !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
			options.Body = commitBody
		} else {
			options.Body, err = a.readBody(cmd, options.Body, bodyFile)
		}
		if err != nil {
			return err
		}
		if draft {
			options.Title = services.AddDraftPrefix(options.Title)
		}
		if agit {
			return a.pushAgit(cmd, t, s, git.AgitOptions{Remote: a.options.remote, Head: options.Head, Base: options.Base, Topic: topic, Title: options.Title, Body: options.Body})
		}
		options.AllowMaintainerEdit = &allowMaintainerEdits
		reviewService := services.Reviews{Client: t.client, Repository: t.Repo}
		if len(reviewerTeams) > 0 {
			if err := reviewService.CheckRequestTeams(cmd.Context(), reviewerTeams); err != nil {
				return err
			}
		}
		if err := a.ensureHeadPublished(cmd, t, s, options.Head); err != nil {
			return err
		}
		item, err := service.Create(cmd.Context(), options)
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
		if err := writeRecord(a, cmd, item, pullRecord); err != nil {
			return err
		}
		if len(failures) > 0 {
			return fmt.Errorf("pull request #%d was created, but follow-up steps failed: %w", item.Number, errors.Join(failures...))
		}
		return nil
	}}
	create.Flags().StringSliceVar(&options.Assignees, "assignee", nil, "Assign users by username")
	create.Flags().StringSliceVar(&labelNames, "label", nil, "Apply labels by exact name; repo:/org: disambiguate")
	create.Flags().Int64Var(&options.Milestone, "milestone", 0, "Milestone ID")
	create.Flags().StringVar(&milestoneName, "milestone-name", "", "Milestone by exact title; conflicts with --milestone")
	create.Flags().StringVar(&options.Title, "title", "", "Pull request title (default: derived from the head branch)")
	create.Flags().StringVar(&deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp")
	create.Flags().StringVar(&options.Body, "body", "", "Pull request body")
	create.Flags().StringVar(&bodyFile, "body-file", "", "Read body from a file, or - for stdin")
	create.Flags().BoolVar(&useEditor, "editor", false, "Compose the pull request body in the configured editor")
	create.Flags().StringVar(&options.Head, "head", "", "Head branch, optionally user:branch (default: current branch)")
	create.Flags().StringVar(&options.Base, "base", "", "Base branch (default: repository default branch)")
	create.Flags().BoolVar(&draft, "draft", false, "Prefix title with WIP for Gitea draft behavior")
	create.Flags().BoolVar(&allowMaintainerEdits, "allow-maintainer-edits", true, "Allow maintainers to push to the head branch")
	create.Flags().BoolVar(&allowMaintainerEdits, "edits", true, "Alias of --allow-maintainer-edits")
	_ = create.Flags().MarkHidden("edits")
	create.Flags().BoolVar(&agit, "agit", false, "Create the pull request through AGit push (refs/for)")
	create.Flags().StringSliceVar(&reviewers, "reviewer", nil, "Request reviews from users by username")
	create.Flags().StringSliceVar(&reviewerTeams, "reviewer-team", nil, "Request reviews from teams within the repository's organization")
	create.Flags().StringArrayVar(&attachments, "attachment", nil, "Attach a file to the pull request (repeatable)")
	create.Flags().StringVar(&topic, "topic", "", "AGit topic name (default: head branch); requires --agit")
	return create
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
	snapshot, err := a.deps.Settings.Load()
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

func (a *application) createPullInteractively(cmd *cobra.Command, snapshot settings.Snapshot) (runErr error) {
	target, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	repo, err := (services.Repositories{Client: target.client}).View(cmd.Context(), target.Repo)
	if err != nil {
		return err
	}
	selectables, err := (services.Pulls{Client: target.client, Repository: target.Repo}).CreateSelectables(cmd.Context())
	if err != nil {
		return err
	}
	baseOwner, _, _ := strings.Cut(target.Repo, "/")
	headOwner, headBranch := a.defaultPullHead(cmd.Context())
	template, err := (services.Pulls{Client: target.client, Repository: target.Repo}).Template(cmd.Context(), repo.DefaultBranch)
	if err != nil {
		return err
	}
	title, body, _ := a.singleCommitMessage(cmd.Context(), repo.DefaultBranch, headBranch)
	if template != "" {
		body = template
	}
	choices := terminal.PullFormChoices{
		BaseOwner: baseOwner, Base: repo.DefaultBranch, HeadOwner: headOwner, HeadBranch: headBranch, Title: title, Body: body,
		AllowMaintainerEdits: true, CanPush: selectables.CanPush, Assignees: selectables.Assignees,
		EditBody: a.bodyEditor(cmd),
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
	if err := a.confirm(cmd, "Create pull request on "+target.Repo+": "+values.Title); err != nil {
		return err
	}
	if values.Agit {
		return a.pushAgit(cmd, target, snapshot, git.AgitOptions{Remote: a.options.remote, Head: values.Head, Base: values.Base, Topic: values.Topic, Title: values.Title, Body: values.Body})
	}
	options := services.PullOptions{
		Title: values.Title, Body: values.Body, Assignees: values.Assignees, Milestone: values.Milestone,
		Labels: values.Labels, Deadline: values.Deadline, Base: values.Base, Head: values.Head,
		AllowMaintainerEdit: &values.AllowMaintainerEdits,
	}
	if err := a.ensureHeadPublished(cmd, target, snapshot, options.Head); err != nil {
		return err
	}
	item, err := (services.Pulls{Client: target.client, Repository: target.Repo}).Create(cmd.Context(), options)
	if err != nil {
		return err
	}
	return writeRecord(a, cmd, item, pullRecord)
}
