// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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

func (s Labels) orgEndpoint() string { return "orgs/" + url.PathEscape(s.Organization) + "/labels" }

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
	labels, err := listPage(limit, page, func(limit, page int) ([]*gitea.Label, http.Header, error) {
		labels, resp, err := s.list(ctx, limit, page)
		return labels, headerOf(resp), err
	})
	return convertPage(labels, labelFrom), err
}

func (s Labels) list(ctx context.Context, limit, page int) ([]*gitea.Label, *gitea.Response, error) {
	return call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Label, *gitea.Response, error) {
		if s.Organization != "" {
			return c.Organizations.ListOrgLabels(ctx, s.Organization, gitea.ListOrgLabelsOptions{ListOptions: listOptions(limit, page)})
		}
		owner, repo, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Repositories.ListRepoLabels(ctx, owner, repo, gitea.ListLabelsOptions{ListOptions: listOptions(limit, page)})
	})
}

// All returns every label in the scope.
func (s Labels) All(ctx context.Context) ([]Label, error) {
	return listAll(func(page int) ([]Label, error) { return s.List(ctx, 100, page) })
}

// Create creates one label.
func (s Labels) Create(ctx context.Context, opts LabelOptions) (Label, error) {
	if s.Organization != "" && opts.Archived != nil && *opts.Archived {
		// TODO(sdk): CreateOrgLabelOption.IsArchived
		var result Label
		if err := sendJSON(ctx, s.Client, http.MethodPost, s.orgEndpoint(), opts, &result); err != nil {
			return result, err
		}
		if !result.Archived {
			// Gitea ignores is_archived when it creates an organization label.
			return s.Edit(ctx, result.ID, LabelOptions{Archived: opts.Archived})
		}
		return result, nil
	}
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
	return labelFrom(label), nil
}

// Edit updates only explicitly supplied fields.
func (s Labels) Edit(ctx context.Context, id int64, opts LabelOptions) (Label, error) {
	if s.Organization != "" && opts.Archived != nil {
		// TODO(sdk): EditOrgLabelOption.IsArchived
		var result Label
		err := sendJSON(ctx, s.Client, http.MethodPatch, fmt.Sprintf("%s/%d", s.orgEndpoint(), id), opts, &result)
		return result, err
	}
	label, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Label, *gitea.Response, error) {
		if s.Organization != "" {
			return c.Organizations.EditOrgLabel(ctx, s.Organization, id, gitea.EditOrgLabelOption{
				Name: opts.Name, Color: opts.Color, Description: opts.Description, Exclusive: opts.Exclusive,
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
		return nil, err
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
		if err := s.replaceLabels(ctx, n, append([]int64{}, (*opts.Labels)...)); err != nil {
			return Issue{}, err
		}
		opts.Labels = nil
		var item Issue
		var err error
		if opts == (IssueEdit{}) {
			item, err = s.View(ctx, n)
		} else {
			item, err = s.Edit(ctx, n, opts)
		}
		if err != nil {
			return Issue{}, fmt.Errorf("labels were updated, but completing the issue edit failed: %w", err)
		}
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

func (s Issues) replaceLabels(ctx context.Context, n int64, ids []int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		_, resp, err := c.Issues.ReplaceIssueLabels(ctx, owner, repo, n, gitea.IssueLabelsOption{Labels: ids})
		return resp, err
	})
}

// applyChanges replaces incremental changes with the full label and assignee sets Gitea
// expects, read from the issue's current state; Gitea cannot add or remove single assignees.
func (s Issues) applyChanges(ctx context.Context, n int64, opts IssueEdit) (IssueEdit, error) {
	changes := *opts.Changes
	opts.Changes = nil
	current, err := s.View(ctx, n)
	if err != nil {
		return opts, err
	}
	if len(changes.AddLabels)+len(changes.RemoveLabels) > 0 {
		kept, err := s.withoutReplacedExclusive(ctx, current.Labels, changes.AddLabels)
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
		names := mergeUnique(current.Assignees, changes.AddAssignees, changes.RemoveAssignees, strings.EqualFold)
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

// RemoveMilestone refuses to clear an issue belonging to another milestone.
func (s Issues) RemoveMilestone(ctx context.Context, n int64, milestone Milestone) (Issue, error) {
	current, err := s.View(ctx, n)
	if err != nil {
		return Issue{}, err
	}
	if current.Milestone == nil || current.Milestone.ID != milestone.ID {
		return Issue{}, fmt.Errorf("#%d is not in milestone %q", n, milestone.Title)
	}
	cleared := int64(0)
	return s.Edit(ctx, n, IssueEdit{Milestone: &cleared})
}
