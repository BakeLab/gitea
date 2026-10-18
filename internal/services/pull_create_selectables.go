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

func loadCreateSelectables(ctx context.Context, client *transport.Client, repository string) (CreateSelectables, error) {
	result := CreateSelectables{Assignees: []string{}, Milestones: []Milestone{}, Labels: []Label{}}
	permissions, err := (Repositories{Client: client}).viewPermissions(ctx, repository)
	if err != nil || !permissions.Push {
		return result, err
	}
	result.CanPush = true
	if result.Assignees, err = (Repositories{Client: client}).assignees(ctx, repository); err != nil {
		return result, err
	}
	if result.Milestones, err = listAll(func(page int) ([]Milestone, error) {
		return (Milestones{Client: client, Repository: repository}).List(ctx, "open", 100, page)
	}); err != nil {
		return result, err
	}
	if result.Labels, err = (Labels{Client: client, Repository: repository}).All(ctx); err != nil {
		return result, err
	}
	// Like --label, offer the labels shared by the organization that owns the repository.
	owner, _, _ := strings.Cut(repository, "/")
	if _, isOrg, err := organization(ctx, client, owner); err != nil || !isOrg {
		return result, err
	}
	orgLabels, err := (Labels{Client: client, Organization: owner}).All(ctx)
	result.Labels = append(result.Labels, orgLabels...)
	return result, err
}
