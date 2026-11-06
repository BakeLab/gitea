// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Activity is one entry of an activity feed: something that someone did.
type Activity struct {
	ID int64 `json:"id"`
	// Type is what was done, as Gitea names it: create_issue, comment_pull, commit_repo and so on.
	Type       string `json:"type"`
	Actor      string `json:"actor"`
	Repository string `json:"repository"`
	// Ref is the branch or tag that the entry is about, as refs/heads/main.
	Ref string `json:"ref"`
	// Content is what Gitea records of it: "12|Title" for an issue or pull request, JSON for a push.
	Content   string `json:"content"`
	Private   bool   `json:"private"`
	CommentID int64  `json:"comment_id"`
	CreatedAt string `json:"created_at"`
}

func activityFrom(item *gitea.Activity) Activity {
	entry := Activity{ID: item.ID, Type: item.OpType, Ref: item.RefName, Content: item.Content, Private: item.IsPrivate, CommentID: item.CommentID, CreatedAt: timestamp(item.Created)}
	if item.ActUser != nil {
		entry.Actor = item.ActUser.UserName
	}
	if item.Repo != nil {
		entry.Repository = item.Repo.FullName
	}
	return entry
}

// ActivityScope selects a feed: the repository's, else that of Owner, a user or an organization, else
// that of the account of the client.
type ActivityScope struct {
	Repository, Owner string
	// Date keeps the entries of one day, as YYYY-MM-DD.
	Date string
	// OnlyPerformedBy keeps, in the feed of a user, what that user did, leaving out what happened in
	// repositories they watch.
	OnlyPerformedBy bool
}

// ErrOrganizationFeedPerformedBy reports an OnlyPerformedBy scope whose owner is an organization, whose
// feed Gitea cannot narrow that way.
var ErrOrganizationFeedPerformedBy = errors.New("the feed of an organization cannot be narrowed to what it did")

// Activities reads activity feeds.
type Activities struct{ Client *transport.Client }

// ListPage lists a page of a feed, newest first.
func (s Activities) ListPage(ctx context.Context, scope ActivityScope, limit, page int) (Page[Activity], error) {
	owner, isOrg := scope.Owner, false
	if scope.Repository == "" {
		var err error
		if owner == "" {
			owner, err = CurrentAccount(ctx, s.Client)
		} else {
			_, isOrg, err = organization(ctx, s.Client, owner)
		}
		if err != nil {
			return Page[Activity]{}, err
		}
		if isOrg && scope.OnlyPerformedBy {
			return Page[Activity]{}, ErrOrganizationFeedPerformedBy
		}
	}
	kind, key := "user", strconv.Quote(owner)
	fetch := func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Activity, *gitea.Response, error) {
		return c.Activity.ListUserFeeds(ctx, owner, gitea.ListUserActivityFeedsOptions{ListOptions: list, Date: scope.Date, OnlyPerformedBy: scope.OnlyPerformedBy})
	}
	switch {
	case scope.Repository != "":
		kind, key = "repository", scope.Repository
		fetch = func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Activity, *gitea.Response, error) {
			repoOwner, repo, err := splitRepository(scope.Repository)
			if err != nil {
				return nil, nil, err
			}
			return c.Activity.ListRepoFeeds(ctx, repoOwner, repo, gitea.ListRepoActivityFeedsOptions{ListOptions: list, Date: scope.Date})
		}
	case isOrg:
		kind = "organization"
		fetch = func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Activity, *gitea.Response, error) {
			return c.Activity.ListOrgFeeds(ctx, owner, gitea.ListOrgActivityFeedsOptions{ListOptions: list, Date: scope.Date})
		}
	}
	window, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Activity, http.Header, error) {
		list := listOptions(limit, page)
		feed, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Activity, *gitea.Response, error) {
			return fetch(ctx, c, list)
		})
		return feed, headerOf(resp), err
	})
	if err != nil {
		return Page[Activity]{}, Classify(err, kind, key, "")
	}
	return convertPage(window, activityFrom), nil
}
