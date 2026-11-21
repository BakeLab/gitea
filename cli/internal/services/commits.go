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

// Commit is a commit of a repository.
type Commit struct {
	SHA            string   `json:"sha"`
	Message        string   `json:"message"`
	Author         string   `json:"author"`
	AuthorEmail    string   `json:"author_email"`
	AuthorLogin    string   `json:"author_login"`
	Committer      string   `json:"committer"`
	CommitterEmail string   `json:"committer_email"`
	CommitterLogin string   `json:"committer_login"`
	AuthoredAt     string   `json:"authored_at"`
	CommittedAt    string   `json:"committed_at"`
	URL            string   `json:"url"`
	Parents        []string `json:"parents"`
	// Verified and VerificationReason tell whether Gitea could check the signature of the commit.
	Verified           bool   `json:"verified"`
	VerificationReason string `json:"verification_reason"`
	// Additions, Deletions and Files are read only when asked for: a list leaves them out unless
	// the output includes them.
	Additions int      `json:"additions"`
	Deletions int      `json:"deletions"`
	Files     []string `json:"files"`
}

// CommitListOptions selects the commits of a listing.
type CommitListOptions struct {
	// Ref is the branch, tag or commit to list from; the default branch when empty.
	Ref string
	// Path keeps the commits that changed this file or directory.
	Path string
	// Not leaves out the commits reachable from this ref.
	Not string
	// Stat, Files and Verification add what costs the server extra work to read.
	Stat, Files, Verification bool
}

func commitFrom(commit *gitea.Commit) Commit {
	item := Commit{Parents: []string{}, Files: []string{}}
	if commit == nil {
		return item
	}
	if commit.CommitMeta != nil {
		item.SHA, item.URL = commit.SHA, commit.CommitMeta.URL
	}
	if commit.HTMLURL != "" {
		item.URL = commit.HTMLURL
	}
	if repo := commit.RepoCommit; repo != nil {
		item.Message = repo.Message
		if repo.Author != nil {
			item.Author, item.AuthorEmail, item.AuthoredAt = repo.Author.Name, repo.Author.Email, repo.Author.Date
		}
		if repo.Committer != nil {
			item.Committer, item.CommitterEmail, item.CommittedAt = repo.Committer.Name, repo.Committer.Email, repo.Committer.Date
		}
		if repo.Verification != nil {
			item.Verified, item.VerificationReason = repo.Verification.Verified, repo.Verification.Reason
		}
	}
	if commit.Author != nil {
		item.AuthorLogin = commit.Author.UserName
	}
	if commit.Committer != nil {
		item.CommitterLogin = commit.Committer.UserName
	}
	item.Parents = convertAll(commit.Parents, func(parent *gitea.CommitMeta) string { return parent.SHA })
	item.Files = convertAll(commit.Files, func(file *gitea.CommitAffectedFiles) string { return file.Filename })
	if commit.Stats != nil {
		item.Additions, item.Deletions = commit.Stats.Additions, commit.Stats.Deletions
	}
	return item
}

// Commits reads the commits of repositories.
type Commits struct{ Client *transport.Client }

// List lists a page of commits, newest first.
func (s Commits) List(ctx context.Context, repo string, opts CommitListOptions, limit, page int) (Page[Commit], error) {
	window, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Commit, http.Header, error) {
		var headers http.Header
		commits, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Commit, *gitea.Response, error) {
			commits, resp, err := c.Repositories.ListRepoCommits(ctx, owner, name, gitea.ListCommitOptions{
				ListOptions: listOptions(limit, page), SHA: opts.Ref, Path: opts.Path, Not: opts.Not,
				Stat: opts.Stat, Files: opts.Files, Verification: opts.Verification,
			})
			headers = headerOf(resp)
			return commits, resp, err
		})
		return commits, headers, err
	})
	if err != nil {
		if opts.Ref != "" {
			return Page[Commit]{}, repositoryItemError(ctx, s.Client, repo, err, "ref", strconv.Quote(opts.Ref))
		}
		return Page[Commit]{}, repositoryItemError(ctx, s.Client, repo, err, "repository", repo)
	}
	return convertPage(window, commitFrom), nil
}

// View returns a commit, named by its SHA, a branch or a tag, with the files it changed.
func (s Commits) View(ctx context.Context, repo, ref string) (Commit, error) {
	commit, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Commit, *gitea.Response, error) {
		return c.Repositories.GetSingleCommit(ctx, owner, name, ref)
	})
	if err != nil {
		return Commit{}, repositoryItemError(ctx, s.Client, repo, err, "commit", strconv.Quote(ref))
	}
	return commitFrom(commit), nil
}

// sha resolves a ref to its commit: the diff route reads the dot of a tag as its extension, and
// statuses are stored by SHA.
func (s Commits) sha(ctx context.Context, repo, ref string) (string, error) {
	commit, err := s.View(ctx, repo, ref)
	if err == nil && commit.SHA == "" {
		err = errors.New("Gitea did not return the commit SHA")
	}
	return commit.SHA, err
}

// Diff returns the unified diff of a commit; with patch it is the patch that git am takes, with the
// message and author.
func (s Commits) Diff(ctx context.Context, repo, ref string, patch bool) ([]byte, error) {
	sha, err := s.sha(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	data, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]byte, *gitea.Response, error) {
		if patch {
			return c.Repositories.GetCommitPatch(ctx, owner, name, sha)
		}
		return c.Repositories.GetCommitDiff(ctx, owner, name, sha)
	})
	if err != nil {
		return nil, repositoryItemError(ctx, s.Client, repo, err, "commit", strconv.Quote(ref))
	}
	return data, nil
}

// Comparison is what a ref has that another has not: the commits between them.
type Comparison struct {
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	TotalCommits int      `json:"total_commits"`
	Commits      []Commit `json:"commits"`
}

// Compare lists the commits that head has and base has not.
func (s Commits) Compare(ctx context.Context, repo, base, head string) (Comparison, error) {
	comparison, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Compare, *gitea.Response, error) {
		return c.Repositories.CompareCommits(ctx, owner, name, base, head)
	})
	if err != nil {
		return Comparison{}, repositoryItemError(ctx, s.Client, repo, err, "ref", strconv.Quote(base+"..."+head))
	}
	return Comparison{Base: base, Head: head, TotalCommits: comparison.TotalCommits, Commits: convertAll(comparison.Commits, commitFrom)}, nil
}

// Status returns the latest status of each check context of a commit, read in full: the combined
// status of Gitea covers only its first page.
func (s Commits) Status(ctx context.Context, repo, ref string) ([]Check, error) {
	sha, err := s.sha(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	return Pulls{Client: s.Client, Repository: repo}.CommitChecks(ctx, sha)
}

// PullRequestOf returns the pull request that merged a commit, if any.
func (s Commits) PullRequestOf(ctx context.Context, repo, sha string) (Pull, error) {
	pull, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullRequest, *gitea.Response, error) {
		return c.PullRequests.GetCommitPullRequest(ctx, owner, name, sha)
	})
	if err != nil {
		return Pull{}, repositoryItemError(ctx, s.Client, repo, err, "pull request of commit", strconv.Quote(sha))
	}
	return pullFrom(pull), nil
}
