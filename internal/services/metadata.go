// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Label is a repository or organization label.
type Label struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Exclusive   bool   `json:"exclusive"`
	Archived    bool   `json:"is_archived"`
}

// LabelOptions distinguishes omitted label fields during edits.
type LabelOptions struct {
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
	Exclusive   *bool   `json:"exclusive,omitempty"`
	Archived    *bool   `json:"is_archived,omitempty"`
}

// KeepArchived returns opts with the archived state of current unless opts sets one: Gitea
// unarchives a label whose edit leaves is_archived out.
func (opts LabelOptions) KeepArchived(current Label) LabelOptions {
	if opts.Archived == nil {
		opts.Archived = &current.Archived
	}
	return opts
}

// Labels implements explicitly scoped label operations.
type Labels struct {
	Client                   *transport.Client
	Repository, Organization string
}

// List returns scoped labels without merging unrelated organization labels.
func (s Labels) List(ctx context.Context, limit, page int) ([]Label, error) {
	labels, _, err := s.list(ctx, limit, page)
	if err != nil {
		return nil, err
	}
	return convertAll(labels, labelFrom), nil
}

// ListPage is List with the number of labels in the scope, as the server counts them.
func (s Labels) ListPage(ctx context.Context, limit, page int) (Page[Label], error) {
	labels, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Label, http.Header, error) {
		labels, resp, err := s.list(ctx, limit, page)
		return labels, headerOf(resp), err
	})
	return convertPage(labels, labelFrom), err
}

func (s Labels) list(ctx context.Context, limit, page int) ([]*gitea.Label, *gitea.Response, error) {
	labels, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Label, *gitea.Response, error) {
		if s.Organization != "" {
			return c.Organizations.ListOrgLabels(ctx, s.Organization, gitea.ListOrgLabelsOptions{ListOptions: listOptions(limit, page)})
		}
		owner, repo, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Repositories.ListRepoLabels(ctx, owner, repo, gitea.ListLabelsOptions{ListOptions: listOptions(limit, page)})
	})
	if s.Organization != "" {
		return labels, resp, Classify(err, "organization", strconv.Quote(s.Organization), "")
	}
	return labels, resp, Classify(err, "repository", s.Repository, "")
}

// All returns every label in the scope.
func (s Labels) All(ctx context.Context) ([]Label, error) {
	return listEvery(100, func(limit, page int) (Page[Label], error) { return s.ListPage(ctx, limit, page) })
}

// Create creates one label.
func (s Labels) Create(ctx context.Context, opts LabelOptions) (Label, error) {
	name, color, description := deref(opts.Name), deref(opts.Color), deref(opts.Description)
	exclusive, archived := deref(opts.Exclusive), deref(opts.Archived)
	label, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Label, *gitea.Response, error) {
		if s.Organization != "" {
			return c.Organizations.CreateOrgLabel(ctx, s.Organization, gitea.CreateOrgLabelOption{
				Name: name, Color: color, Description: description, Exclusive: exclusive,
			})
		}
		owner, repo, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Repositories.CreateLabel(ctx, owner, repo, gitea.CreateLabelOption{
			Name: name, Color: color, Description: description, Exclusive: exclusive, IsArchived: archived,
		})
	})
	if err != nil {
		return Label{}, err
	}
	if s.Organization != "" && archived && !label.IsArchived { // Gitea ignores is_archived when it creates an organization label
		return s.Edit(ctx, label.ID, LabelOptions{Archived: opts.Archived})
	}
	return labelFrom(label), nil
}

// Edit updates only explicitly supplied fields.
func (s Labels) Edit(ctx context.Context, id int64, opts LabelOptions) (Label, error) {
	label, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Label, *gitea.Response, error) {
		if s.Organization != "" {
			return c.Organizations.EditOrgLabel(ctx, s.Organization, id, gitea.EditOrgLabelOption{
				Name: opts.Name, Color: opts.Color, Description: opts.Description, Exclusive: opts.Exclusive, IsArchived: opts.Archived,
			})
		}
		owner, repo, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Repositories.EditLabel(ctx, owner, repo, id, gitea.EditLabelOption{
			Name: opts.Name, Color: opts.Color, Description: opts.Description, Exclusive: opts.Exclusive, IsArchived: opts.Archived,
		})
	})
	if err != nil {
		return Label{}, err
	}
	return labelFrom(label), nil
}

// Delete deletes one scoped label.
func (s Labels) Delete(ctx context.Context, id int64) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		if s.Organization != "" {
			return c.Organizations.DeleteOrgLabel(ctx, s.Organization, id)
		}
		owner, repo, err := splitRepository(s.Repository)
		if err != nil {
			return nil, err
		}
		return c.Repositories.DeleteLabel(ctx, owner, repo, id)
	})
}

// Milestone is a stable milestone result.
type Milestone struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	State        string  `json:"state"`
	Deadline     *string `json:"due_on"`
	OpenIssues   int     `json:"open_issues"`
	ClosedIssues int     `json:"closed_issues"`
}

func milestoneFrom(milestone *gitea.Milestone) Milestone {
	return Milestone{
		ID: milestone.ID, Title: milestone.Title, Description: milestone.Description, State: string(milestone.State),
		Deadline: optionalTimestamp(milestone.Deadline), OpenIssues: milestone.OpenIssues, ClosedIssues: milestone.ClosedIssues,
	}
}

// MilestoneOptions allows explicit state, description and deadline updates.
type MilestoneOptions struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	State       *string `json:"state,omitempty"`
	Deadline    *string `json:"due_on,omitempty"`
}

// deadline parses the RFC 3339 deadline; the zero time clears it.
func (o MilestoneOptions) deadline() (*time.Time, error) {
	if o.Deadline == nil {
		return nil, nil
	}
	deadline, err := time.Parse(time.RFC3339, *o.Deadline)
	if err != nil {
		return nil, fmt.Errorf("invalid milestone deadline %q: %w", *o.Deadline, err)
	}
	return &deadline, nil
}

// Milestones implements repository milestone operations.
type Milestones struct {
	Client     *transport.Client
	Repository string
}

// List returns a non-nil page of milestones.
func (s Milestones) List(ctx context.Context, state string, limit, page int) ([]Milestone, error) {
	milestones, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Milestone, *gitea.Response, error) {
		return c.Repositories.ListMilestones(ctx, owner, repo, gitea.ListMilestoneOption{ListOptions: listOptions(limit, page), State: gitea.StateType(state)})
	})
	if err != nil {
		// A 404 here can only mean the repository is missing or invisible.
		return nil, Classify(err, "repository", s.Repository, "")
	}
	return convertAll(milestones, milestoneFrom), nil
}

// Create creates one milestone.
func (s Milestones) Create(ctx context.Context, opts MilestoneOptions) (Milestone, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Milestone{}, err
	}
	deadline, err := opts.deadline()
	if err != nil {
		return Milestone{}, err
	}
	milestone, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Milestone, *gitea.Response, error) {
		return c.Repositories.CreateMilestone(ctx, owner, repo, gitea.CreateMilestoneOption{
			Title: deref(opts.Title), Description: deref(opts.Description), State: gitea.StateType(deref(opts.State)), Deadline: deadline,
		})
	})
	if err != nil {
		return Milestone{}, err
	}
	return milestoneFrom(milestone), nil
}

// Edit updates a milestone.
func (s Milestones) Edit(ctx context.Context, id int64, opts MilestoneOptions) (Milestone, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Milestone{}, err
	}
	deadline, err := opts.deadline()
	if err != nil {
		return Milestone{}, err
	}
	// Gitea clears omitted due_on values, so unrelated edits must resend the date.
	if opts.Deadline == nil {
		current, err := s.get(ctx, owner, repo, id)
		if err != nil {
			return Milestone{}, err
		}
		deadline = current.Deadline
	}
	edit := gitea.EditMilestoneOption{Title: deref(opts.Title), Description: opts.Description, Deadline: deadline}
	if opts.State != nil {
		state := gitea.StateType(*opts.State)
		edit.State = &state
	}
	milestone, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Milestone, *gitea.Response, error) {
		return c.Repositories.EditMilestone(ctx, owner, repo, id, edit)
	})
	if err != nil {
		return Milestone{}, err
	}
	return milestoneFrom(milestone), nil
}

// Delete deletes one milestone.
func (s Milestones) Delete(ctx context.Context, id int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Repositories.DeleteMilestone(ctx, owner, repo, id)
	})
}

func (s Milestones) get(ctx context.Context, owner, repo string, id int64) (*gitea.Milestone, error) {
	milestone, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Milestone, *gitea.Response, error) {
		return c.Repositories.GetMilestone(ctx, owner, repo, id)
	})
	return milestone, err
}

// IssueEdit distinguishes omitted fields from explicit clears.
type IssueEdit struct {
	Labels         *[]int64         `json:"-"`
	Ref            *string          `json:"ref,omitempty"`
	Title          *string          `json:"title,omitempty"`
	Body           *string          `json:"body,omitempty"`
	Assignees      *[]string        `json:"assignees,omitempty"`
	Milestone      *int64           `json:"milestone,omitempty"`
	Deadline       *time.Time       `json:"due_date,omitempty"`
	RemoveDeadline *bool            `json:"unset_due_date,omitempty"`
	Changes        *MetadataChanges `json:"-"`
}

// MetadataChanges adds and removes labels and assignees relative to an issue's current state.
type MetadataChanges struct {
	AddLabels, RemoveLabels       []int64
	AddAssignees, RemoveAssignees []string
}

// Edit updates only supplied issue fields.
func (s Issues) Edit(ctx context.Context, n int64, opts IssueEdit) (Issue, error) {
	if opts.Changes != nil {
		var err error
		if opts, err = s.applyChanges(ctx, n, opts); err != nil {
			return Issue{}, err
		}
	}
	if opts.Assignees != nil {
		names := mergeUnique(*opts.Assignees, nil, nil, strings.EqualFold)
		opts.Assignees = &names
	}
	if opts.Labels != nil {
		// The other fields go first: Gitea refuses label changes from people who cannot write
		// to the issues, and still takes a title or body from the author.
		ids := append([]int64{}, (*opts.Labels)...)
		opts.Labels = nil
		var item Issue
		edited := opts != (IssueEdit{})
		if edited {
			var err error
			if item, err = s.Edit(ctx, n, opts); err != nil {
				return Issue{}, err
			}
		}
		labels, err := s.replaceLabels(ctx, n, ids)
		if err != nil {
			saved := ""
			if edited {
				saved = otherChangesSent
			}
			return item, s.labelsError(ctx, n, item.Kind, saved, err)
		}
		if !edited {
			return s.View(ctx, n)
		}
		item.Labels = labels
		return item, nil
	}
	edit := gitea.EditIssueOption{
		Title: deref(opts.Title), Body: opts.Body, Ref: opts.Ref, Milestone: opts.Milestone,
		Deadline: opts.Deadline, RemoveDeadline: opts.RemoveDeadline,
	}
	if opts.Assignees != nil {
		edit.Assignees = *opts.Assignees
	}
	return s.edit(ctx, n, edit)
}

func (s Issues) replaceLabels(ctx context.Context, n int64, ids []int64) ([]Label, error) {
	labels, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Label, *gitea.Response, error) {
		return c.Issues.ReplaceIssueLabels(ctx, owner, repo, n, gitea.IssueLabelsOption{Labels: ids})
	})
	return convertAll(labels, labelFrom), err
}

// otherChangesSent words, in a refused label change, that the other changes of an edit were sent.
const otherChangesSent = "the other changes were sent"

// labelsError words a failed label change of issue n of kind, or "" when unknown. saved says
// which changes were saved before, such as "the other changes were sent", or is empty when there
// were none.
func (s Issues) labelsError(ctx context.Context, n int64, kind, saved string, err error) error {
	refusal := s.labelsRefusal(ctx, n, kind, err)
	if refusal == "" {
		if saved != "" {
			return fmt.Errorf("%s, but changing the labels failed: %w", saved, err)
		}
		return err
	}
	text := refusal
	if saved != "" {
		text = saved + ", but the labels were left unchanged: " + text
	} else {
		text += "; nothing was changed"
	}
	return &RefusedError{Text: text, Err: err}
}

// labelsRefusal words why Gitea refused a label change of issue n of kind with err, or returns
// "" when err is no such refusal. Gitea answers a 403 to people who cannot write to the issues,
// or pull requests, of the repository, without naming the labels.
func (s Issues) labelsRefusal(ctx context.Context, n int64, kind string, err error) string {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok || apiErr.Status != http.StatusForbidden || strings.Contains(apiErr.Message, "required scope") {
		return ""
	}
	if kind == "" {
		if current, viewErr := s.View(ctx, n); viewErr == nil {
			kind = current.Kind
		}
	}
	noun := "issues"
	if kind == KindPull {
		noun = "pull requests"
	}
	return fmt.Sprintf("Gitea refuses label changes from people who cannot write to the %s of %s", noun, s.Repository)
}

// applyChanges replaces incremental changes with the full label and assignee sets Gitea
// expects, read from the issue's current state; Gitea cannot add or remove single assignees.
func (s Issues) applyChanges(ctx context.Context, n int64, opts IssueEdit) (IssueEdit, error) {
	current, err := s.View(ctx, n)
	if err != nil {
		return opts, err
	}
	return s.applyChangesTo(ctx, current.Labels, current.Assignees, opts)
}

// applyChangesTo is applyChanges for an issue that holds labels and assignees.
func (s Issues) applyChangesTo(ctx context.Context, labels []Label, assignees []string, opts IssueEdit) (IssueEdit, error) {
	changes := *opts.Changes
	opts.Changes = nil
	if len(changes.AddLabels)+len(changes.RemoveLabels) > 0 {
		kept, err := s.withoutReplacedExclusive(ctx, labels, changes.AddLabels)
		if err != nil {
			return opts, err
		}
		ids := make([]int64, len(kept))
		for i, label := range kept {
			ids[i] = label.ID
		}
		ids = mergeUnique(ids, changes.AddLabels, changes.RemoveLabels, func(a, b int64) bool { return a == b })
		opts.Labels = &ids
	}
	if len(changes.AddAssignees)+len(changes.RemoveAssignees) > 0 {
		names := mergeUnique(assignees, changes.AddAssignees, changes.RemoveAssignees, strings.EqualFold)
		opts.Assignees = &names
	}
	return opts, nil
}

// withoutReplacedExclusive drops the current labels that an added label of the same exclusive
// scope replaces. Gitea keeps one label per scope but picks it by name, not by recency, so
// sending both would keep whichever sorts last.
func (s Issues) withoutReplacedExclusive(ctx context.Context, current []Label, add []int64) ([]Label, error) {
	if len(add) == 0 || !slices.ContainsFunc(current, func(l Label) bool { return l.Exclusive }) {
		return current, nil
	}
	all, err := (Labels{Client: s.Client, Repository: s.Repository}).All(ctx)
	if err != nil {
		return nil, err
	}
	owner, _, _ := strings.Cut(s.Repository, "/")
	if _, isOrg, err := organization(ctx, s.Client, owner); err != nil {
		return nil, err
	} else if isOrg {
		shared, err := (Labels{Client: s.Client, Organization: owner}).All(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, shared...)
	}
	replaced := map[string]bool{}
	for _, label := range all {
		if scope := exclusiveScope(label); scope != "" && slices.Contains(add, label.ID) {
			replaced[scope] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(current), func(l Label) bool { return replaced[exclusiveScope(l)] }), nil
}

// exclusiveScope is the name of an exclusive label up to its last slash, or "" for any other label.
func exclusiveScope(label Label) string {
	if i := strings.LastIndex(label.Name, "/"); label.Exclusive && i > 0 {
		return label.Name[:i]
	}
	return ""
}

// mergeUnique applies additions and removals to current values, preserving order.
func mergeUnique[T any](current, add, remove []T, equal func(T, T) bool) []T {
	result := []T{}
	for _, item := range slices.Concat(current, add) {
		matches := func(other T) bool { return equal(item, other) }
		if !slices.ContainsFunc(remove, matches) && !slices.ContainsFunc(result, matches) {
			result = append(result, item)
		}
	}
	return result
}

// NotInMilestoneError reports an issue that RemoveMilestone left unchanged, as it is not in the
// milestone.
type NotInMilestoneError struct {
	Number    int64
	Milestone string
}

func (e *NotInMilestoneError) Error() string {
	return fmt.Sprintf("#%d is not in milestone %q", e.Number, e.Milestone)
}

// RemoveMilestone clears the milestone of issue n when it is milestone. It refuses to clear an
// issue belonging to another milestone or to none, returning it with a *NotInMilestoneError.
func (s Issues) RemoveMilestone(ctx context.Context, n int64, milestone Milestone) (Issue, error) {
	current, err := s.View(ctx, n)
	if err != nil {
		return Issue{}, err
	}
	if current.Milestone == nil || current.Milestone.ID != milestone.ID {
		return current, &NotInMilestoneError{Number: n, Milestone: milestone.Title}
	}
	cleared := int64(0)
	return s.Edit(ctx, n, IssueEdit{Milestone: &cleared})
}
