// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strings"

	"gitea.dev/cli/internal/transport"
)

// CreateSelectables are the metadata choices offered by forms. Where they depend on push access, the
// lists stay empty unless CanPush.
type CreateSelectables struct {
	CanPush    bool
	Assignees  []string
	Milestones []Milestone
	Labels     []Label
}

// PullCreateChoices seed the interactive create form: the repository, its pull request template
// on the default branch, and the metadata selectables.
type PullCreateChoices struct {
	Repository Repository
	Base       PullRepository
	Template   string
	CreateSelectables
}

// CreateChoices fetches the repository once for its default branch and permissions, refusing one
// with its pull requests turned off, then loads the pull request template and the selectables
// concurrently.
func (s Pulls) CreateChoices(ctx context.Context) (PullCreateChoices, error) {
	item, err := (Repositories{Client: s.Client}).get(ctx, s.Repository)
	if err != nil {
		return PullCreateChoices{}, err
	}
	result := PullCreateChoices{Repository: repositoryFrom(item), Base: pullRepositoryFrom(s.Repository, item)}
	if err := result.Base.CheckEnabled(); err != nil {
		return PullCreateChoices{}, err
	}
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

// createSelectablesFor loads assignees, milestones, and labels concurrently when canPush.
func createSelectablesFor(ctx context.Context, client *transport.Client, repository string, canPush bool) (CreateSelectables, error) {
	if !canPush {
		return CreateSelectables{Assignees: []string{}, Milestones: []Milestone{}, Labels: []Label{}}, nil
	}
	result, err := loadMetadataChoices(ctx, client, repository)
	result.CanPush = true
	return result, err
}

// loadMetadataChoices loads assignees, milestones, and labels concurrently, whatever the user may
// do with them; CanPush stays false.
func loadMetadataChoices(ctx context.Context, client *transport.Client, repository string) (CreateSelectables, error) {
	result := CreateSelectables{Assignees: []string{}, Milestones: []Milestone{}, Labels: []Label{}}
	err := loadTogether(
		func() (err error) {
			result.Assignees, err = (Repositories{Client: client}).assignees(ctx, repository)
			return err
		},
		func() (err error) {
			result.Milestones, err = (Milestones{Client: client, Repository: repository}).AllIn(ctx, "open")
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
