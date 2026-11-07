// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/huh/v2"
)

// PullFormChoices supplies defaults and selectable options for interactive PR create.
type PullFormChoices struct {
	// Repo is the base repository, named in the form's header.
	Repo                                   string
	BaseOwner, Base, HeadOwner, HeadBranch string
	// Title and Body seed the form; Title applies only while HeadBranch is kept.
	Title, Body          string
	AllowMaintainerEdits bool
	Assignees            []string
	Milestones           []IssueMilestoneChoice
	Labels               []IssueLabelChoice
	// CanPush offers metadata, which needs push access; without it, changing branches offers AGit.
	CanPush  bool
	EditBody BodyEditor
	// SeedBody seeds the body in place of Body when the branches asked first change.
	SeedBody func(ctx context.Context, base, headBranch string) (string, error)
}

// PullFormValues holds interactively collected pull request create fields.
type PullFormValues struct {
	IssueFormValues
	Agit                 bool
	Base, Head, Topic    string
	AllowMaintainerEdits bool
	// AsDraft submits the pull request as a draft.
	AsDraft bool
}

// PullForm collects interactive pull create input.
type PullForm interface {
	CreatePull(context.Context, PullFormChoices) (PullFormValues, error)
}

// pullNext is what the user does after writing a pull request's title and body.
type pullNext int

const (
	pullSubmit pullNext = iota
	pullSubmitDraft
	pullMetadata
	pullBranches
	pullCancel
)

// CreatePull runs the interactive pull create form on stderr, like gh pr create: the title and
// body of a pull request from the current branch, then "What's next?" to submit it, submit it as a
// draft, add metadata, change the branches or cancel. Branches are asked first when unknown.
func (p *FormPrompter) CreatePull(ctx context.Context, choices PullFormChoices) (PullFormValues, error) {
	branches := pullBranchFields{base: choices.Base, headOwner: choices.HeadOwner, headBranch: choices.HeadBranch, allowEdits: choices.AllowMaintainerEdits}
	branches.topic = branches.headBranch
	// A head equal to the base is the usual slip of committing on the default branch: ask instead.
	sameBranch := strings.TrimSpace(branches.base) == strings.TrimSpace(branches.headBranch) && (branches.headOwner == "" || branches.headOwner == choices.BaseOwner)
	if strings.TrimSpace(branches.base) == "" || strings.TrimSpace(branches.headBranch) == "" || sameBranch {
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(branches.fields(!choices.CanPush)...))); err != nil {
			return PullFormValues{}, err
		}
		base, head := strings.TrimSpace(branches.base), strings.TrimSpace(branches.headBranch)
		if (base != choices.Base || head != choices.HeadBranch) && choices.SeedBody != nil {
			var err error
			if choices.Body, err = choices.SeedBody(ctx, base, head); err != nil {
				return PullFormValues{}, err
			}
		}
	}
	fmt.Fprintln(p.Streams.ErrOut, SafeLine(branches.header(choices.Repo, choices.BaseOwner)))

	title := DefaultPullTitle(branches.headBranch)
	if choices.Title != "" && branches.headBranch == choices.HeadBranch {
		title = choices.Title
	}
	fields := issueFields{title: title, body: choices.Body}
	content := []huh.Field{titleField(&fields.title)}
	if choices.EditBody == nil {
		content = append(content, bodyField(BodyTitle, &fields.body))
	}
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(content...))); err != nil {
		return PullFormValues{}, err
	}
	fields.body = fields.bodyResult(choices.Body, choices.EditBody)
	values := PullFormValues{}
	var err error
	if values.Body, values.Draft, err = editBody(ctx, choices.EditBody, fields.body); err != nil {
		return values, err
	}
	for next := pullMetadata; next == pullMetadata || next == pullBranches; {
		metadata := choices.CanPush && !branches.agit && (len(choices.Assignees)+len(choices.Milestones)+len(choices.Labels) > 0)
		options := []huh.Option[pullNext]{huh.NewOption("Submit", pullSubmit)}
		if !branches.agit {
			options = append(options, huh.NewOption("Submit as draft", pullSubmitDraft))
		}
		if metadata {
			options = append(options, huh.NewOption("Add metadata (assignees, labels, milestone, due date)", pullMetadata))
		}
		options = append(options, huh.NewOption("Change branches", pullBranches), huh.NewOption("Cancel", pullCancel))
		next = pullSubmit
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewSelect[pullNext]().Title("What's next?").Options(options...).Value(&next)))); err != nil {
			return values, err
		}
		switch next {
		case pullMetadata:
			groups := fields.metadata(choices.Assignees, choices.Milestones, choices.Labels)
			if err := p.runForm(ctx, huh.NewForm(groups...)); err != nil {
				return values, err
			}
		case pullBranches:
			if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(branches.fields(!choices.CanPush)...))); err != nil {
				return values, err
			}
			fmt.Fprintln(p.Streams.ErrOut, SafeLine(branches.header(choices.Repo, choices.BaseOwner)))
		case pullCancel:
			return values, ErrCanceled
		}
		values.AsDraft = next == pullSubmitDraft
	}
	values.Title, values.Assignees, values.Milestone, values.Labels = strings.TrimSpace(fields.title), fields.assignees, fields.milestone, fields.labels
	deadline, ok, err := parseDueDate(fields.deadline)
	if err != nil {
		return values, err
	}
	if ok {
		values.Deadline = &deadline
	}
	values.Agit, values.Base, values.Topic, values.AllowMaintainerEdits = branches.agit, strings.TrimSpace(branches.base), strings.TrimSpace(branches.topic), branches.allowEdits
	values.Head = branches.head(choices.BaseOwner)
	return values, nil
}

// pullBranchFields are the branches of a new pull request.
type pullBranchFields struct {
	base, headOwner, headBranch, topic string
	allowEdits, agit                   bool
}

// fields asks for the branches; offerAgit adds the choice of an AGit push for users who cannot
// push to the base repository.
func (b *pullBranchFields) fields(offerAgit bool) []huh.Field {
	fields := []huh.Field{requiredLineField("Base branch", &b.base)}
	if offerAgit {
		fields = append(fields, huh.NewConfirm().Title("Push through AGit (refs/for) instead of a branch of your own?").Value(&b.agit))
	}
	return append(fields,
		lineField("Head repository owner:", &b.headOwner, true),
		requiredLineField("Head branch", &b.headBranch),
		lineField("AGit topic (when pushing through AGit):", &b.topic, true),
		huh.NewConfirm().Title("Allow maintainers to push to the head branch?").Value(&b.allowEdits),
	)
}

// head is the head of the pull request: BRANCH, OWNER:BRANCH for a head in another repository, or
// HEAD for an AGit push without a branch.
func (b *pullBranchFields) head(baseOwner string) string {
	branch, owner := strings.TrimSpace(b.headBranch), strings.TrimSpace(b.headOwner)
	switch {
	case b.agit && branch == "":
		return "HEAD"
	case !b.agit && owner != "" && owner != baseOwner:
		return owner + ":" + branch
	}
	return branch
}

// header introduces the pull request: "Creating pull request for feat/one into main in alice/x".
func (b *pullBranchFields) header(repo, baseOwner string) string {
	text := fmt.Sprintf("Creating pull request for %s into %s", b.head(baseOwner), strings.TrimSpace(b.base))
	if repo != "" {
		text += " in " + repo
	}
	return text
}

// DefaultPullTitle turns a branch-like head into a readable pull title seed.
func DefaultPullTitle(head string) string {
	if i := strings.LastIndex(head, ":"); i != -1 {
		head = head[i+1:]
	}
	parts := strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(head)))
	if len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		r, size := utf8.DecodeRuneInString(part)
		parts[i] = string(unicode.ToUpper(r)) + part[size:]
	}
	return strings.Join(parts, " ")
}
