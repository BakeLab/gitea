// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"gitea.dev/cli/internal/settings"
	gitea "gitea.dev/sdk"
)

// convertAll maps SDK items onto gt results as a non-nil slice, skipping absent items.
func convertAll[S, T any](items []*S, convert func(*S) T) []T {
	result := make([]T, 0, len(items))
	for _, item := range items {
		if item != nil {
			result = append(result, convert(item))
		}
	}
	return result
}

func labelFrom(label *gitea.Label) Label {
	return Label{ID: label.ID, Name: label.Name, Color: label.Color, Description: label.Description, Exclusive: label.Exclusive, Archived: label.IsArchived}
}

func repositoryFrom(repo *gitea.Repository) Repository {
	result := Repository{
		ActionsEnabled: repo.HasActions, Website: repo.Website, ID: repo.ID, Name: repo.Name,
		FullName: repo.FullName, Description: repo.Description, URL: repo.HTMLURL, Private: repo.Private,
		DefaultBranch: repo.DefaultBranch, CloneURL: repo.CloneURL, SSHURL: repo.SSHURL,
		Archived: repo.Archived, Template: repo.Template, Fork: repo.Fork, Mirror: repo.Mirror, OriginalURL: repo.OriginalURL,
		Empty: repo.Empty, Stars: repo.Stars, Forks: repo.Forks, OpenIssues: repo.OpenIssues, OpenPulls: repo.OpenPulls,
		Topics: repo.Topics, UpdatedAt: timestamp(repo.Updated), Source: settings.WithoutCredentials(repo.OriginalURL), MirrorInterval: repo.MirrorInterval,
	}
	if repo.Parent != nil {
		parent := repositoryFrom(repo.Parent)
		result.Parent = &parent
	}
	return result
}

func organizationFrom(org *gitea.Organization) Organization {
	name := org.Name
	if name == "" {
		name = org.UserName
	}
	return Organization{
		ID: org.ID, Name: name, FullName: org.FullName, Description: org.Description, Website: org.Website,
		Location: org.Location, Visibility: org.Visibility, RepoAdminChangeTeamAccess: org.RepoAdminChangeTeamAccess,
	}
}

func memberFrom(user *gitea.User) OrganizationMember {
	return OrganizationMember{ID: user.ID, Username: user.UserName, FullName: user.FullName}
}

func issueMilestoneFrom(milestone *gitea.Milestone) *IssueMilestone {
	if milestone == nil {
		return nil
	}
	return &IssueMilestone{ID: milestone.ID, Title: milestone.Title}
}

func issueFrom(issue *gitea.Issue) Issue {
	return Issue{
		Author: login(issue.Poster), Labels: convertAll(issue.Labels, labelFrom), Ref: issue.Ref, ID: issue.ID, Number: issue.Index,
		Title: issue.Title, Body: issue.Body, URL: issue.HTMLURL, State: string(issue.State),
		CreatedAt: timestamp(issue.Created), UpdatedAt: timestamp(issue.Updated), Deadline: optionalTimestamp(issue.Deadline),
		Assignees: convertAll(issue.Assignees, login), Milestone: issueMilestoneFrom(issue.Milestone), Comments: int64(issue.Comments), Locked: issue.IsLocked,
	}
}

// convertPage maps a page of SDK items onto gt results.
func convertPage[S, T any](page Page[*S], convert func(*S) T) Page[T] {
	return Page[T]{Items: convertAll(page.Items, convert), More: page.More, Total: page.Total, Counted: page.Counted}
}
