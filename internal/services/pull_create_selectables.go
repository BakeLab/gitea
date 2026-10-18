// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strings"

	"gitea.dev/cli/internal/transport"
)

// CreateSelectables are the metadata choices offered by create forms; the lists stay empty unless CanPush.
type CreateSelectables struct {
	CanPush    bool
	Assignees  []string
	Milestones []Milestone
	Labels     []Label
}

// CreateSelectables loads assignees/milestones/labels when the user can push.
func (s Pulls) CreateSelectables(ctx context.Context) (CreateSelectables, error) {
	return loadCreateSelectables(ctx, s.Client, s.Repository)
}

// PullCreateChoices seed the interactive create form: the repository, its pull request template
// on the default branch, and the metadata selectables.
type PullCreateChoices struct {
	Repository Repository
	Template   string
	CreateSelectables
}

// CreateChoices fetches the repository once for its default branch and permissions, then loads
// the pull request template and the selectables concurrently.
func (s Pulls) CreateChoices(ctx context.Context) (PullCreateChoices, error) {
	item, err := (Repositories{Client: s.Client}).get(ctx, s.Repository)
	if err != nil {
		return PullCreateChoices{}, err
	}
	result := PullCreateChoices{Repository: repositoryFrom(item)}
	err = loadTogether(
		func() (err error) {
			result.Template, err = s.Template(ctx, result.Repository.DefaultBranch)
			return err
		},
		func() (err error) {
			result.CreateSelectables, err = createSelectablesFor(ctx, s.Client, s.Repository, permissionsFrom(item).Push)
			return err
		},
	)
	if err != nil {
		return PullCreateChoices{}, err
	}
	return result, nil
}

func loadCreateSelectables(ctx context.Context, client *transport.Client, repository string) (CreateSelectables, error) {
	permissions, err := (Repositories{Client: client}).viewPermissions(ctx, repository)
	if err != nil {
		return CreateSelectables{Assignees: []string{}, Milestones: []Milestone{}, Labels: []Label{}}, err
	}
	return createSelectablesFor(ctx, client, repository, permissions.Push)
}

// createSelectablesFor loads assignees, milestones, and labels concurrently when canPush.
func createSelectablesFor(ctx context.Context, client *transport.Client, repository string, canPush bool) (CreateSelectables, error) {
	result := CreateSelectables{Assignees: []string{}, Milestones: []Milestone{}, Labels: []Label{}}
	if !canPush {
		return result, nil
	}
	result.CanPush = true
	err := loadTogether(
		func() (err error) {
			result.Assignees, err = (Repositories{Client: client}).assignees(ctx, repository)
			return err
		},
		func() (err error) {
			result.Milestones, err = listAll(func(page int) ([]Milestone, error) {
				return (Milestones{Client: client, Repository: repository}).List(ctx, "open", 100, page)
			})
			return err
		},
		func() (err error) {
			if result.Labels, err = (Labels{Client: client, Repository: repository}).All(ctx); err != nil {
				return err
			}
			// Like --label, offer the labels shared by the organization that owns the repository.
			owner, _, _ := strings.Cut(repository, "/")
			if _, isOrg, err := organization(ctx, client, owner); err != nil || !isOrg {
				return err
			}
			orgLabels, err := (Labels{Client: client, Organization: owner}).All(ctx)
			result.Labels = append(result.Labels, orgLabels...)
			return err
		},
	)
	return result, err
}
