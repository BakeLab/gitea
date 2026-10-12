// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"

	gitea "gitea.dev/sdk"
)

// RepositoryListOptions selects a repository collection and page.
type RepositoryListOptions struct {
	Owner, Kind      string
	Starred, Watched bool
	Limit, Page      int
}

// RepositorySearchOptions supplies explicit repository search filters.
type RepositorySearchOptions struct {
	Query, Owner, Kind, Sort, Order string
	Topic                           bool
	Private, Archived               *bool
	Limit, Page                     int
}

func (s Repositories) ownerID(ctx context.Context, owner string) (int64, bool, error) {
	if id, isOrg, err := organization(ctx, s.Client, owner); err != nil || isOrg {
		return id, isOrg, err
	}
	user, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetUserInfo(ctx, owner)
	})
	if err != nil {
		return 0, false, err
	}
	return user.ID, false, nil
}

func repositoryKind(item Repository, kind string) bool {
	switch kind {
	case "fork":
		return item.Fork
	case "mirror":
		return item.Mirror
	case "source":
		return !item.Fork && !item.Mirror
	default:
		return true
	}
}

// List returns repositories accessible to the account, or its selected collection.
// It reports whether more repositories follow the selected page.
func (s Repositories) List(ctx context.Context, opts RepositoryListOptions) ([]Repository, bool, error) {
	page, err := s.ListPage(ctx, opts)
	return page.Items, page.More, err
}

// ListPage is List that also reports the size of the collection when the server counts it.
// A kind filter applies to the page, so the collection is not counted then.
func (s Repositories) ListPage(ctx context.Context, opts RepositoryListOptions) (Page[Repository], error) {
	isOrg := false
	var err error
	if opts.Owner != "" {
		if _, isOrg, err = s.ownerID(ctx, opts.Owner); err != nil {
			return Page[Repository]{}, err
		}
	}
	var window Page[*gitea.Repository]
	if opts.Owner == "" && (opts.Starred || opts.Watched) {
		// TODO(sdk): GetMyStarredRepos and GetMyWatchedRepos accept no ListOptions.
		endpoint := "user/subscriptions"
		if opts.Starred {
			endpoint = "user/starred"
		}
		window, err = rawPage[*gitea.Repository](ctx, s.Client, endpoint, nil, opts.Limit, opts.Page)
	} else {
		window, err = callPage(ctx, s.Client, opts.Limit, opts.Page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Repository, *gitea.Response, error) {
			switch {
			case isOrg:
				return c.Repositories.ListOrgRepos(ctx, opts.Owner, gitea.ListOrgReposOptions{ListOptions: list})
			case opts.Owner != "":
				return c.Repositories.ListUserRepos(ctx, opts.Owner, gitea.ListReposOptions{ListOptions: list})
			default:
				return c.Repositories.ListMyRepos(ctx, gitea.ListReposOptions{ListOptions: list})
			}
		})
	}
	if err != nil {
		return Page[Repository]{}, err
	}
	result := Page[Repository]{Items: make([]Repository, 0, len(window.Items)), More: window.More, Total: window.Total, Counted: window.Counted && opts.Kind == ""}
	for _, item := range window.Items {
		if repo := repositoryFrom(item); repositoryKind(repo, opts.Kind) {
			result.Items = append(result.Items, repo)
		}
	}
	return result, nil
}

// Search searches repository names, descriptions or topics within one instance.
func (s Repositories) Search(ctx context.Context, opts RepositorySearchOptions) (Page[Repository], error) {
	options := gitea.SearchRepoOptions{
		Keyword: opts.Query, KeywordIsTopic: opts.Topic, KeywordInDescription: true,
		IsPrivate: opts.Private, IsArchived: opts.Archived, Type: gitea.RepoType(opts.Kind),
	}
	if opts.Sort != "" {
		options.Sort, options.Order = opts.Sort, opts.Order
	}
	if opts.Owner != "" {
		id, _, err := s.ownerID(ctx, opts.Owner)
		if err != nil {
			return Page[Repository]{}, err
		}
		options.OwnerID = id
	}
	account, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	if err != nil {
		return Page[Repository]{}, err
	}
	options.PrioritizedByOwnerID = account.ID
	window, err := callPage(ctx, s.Client, opts.Limit, opts.Page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Repository, *gitea.Response, error) {
		options.ListOptions = list
		return c.Repositories.SearchRepos(ctx, options)
	})
	if err != nil {
		return Page[Repository]{}, err
	}
	return convertPage(window, repositoryFrom), nil
}
