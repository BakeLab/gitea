// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Tag is a tag of a repository.
type Tag struct {
	Name string `json:"name"`
	// Message is the message of an annotated tag; a lightweight tag has none.
	Message string `json:"message"`
	// Commit is the commit that the tag points to; ID is the SHA of the tag itself, which differs
	// from Commit for an annotated tag.
	Commit     string `json:"commit"`
	ID         string `json:"id"`
	Annotated  bool   `json:"annotated"`
	ZipballURL string `json:"zipball_url"`
	TarballURL string `json:"tarball_url"`
	// Tagger, TaggedAt and the verification are read with gt tag view only, for an annotated tag.
	// Verified and VerificationReason are of the tagged commit: Gitea does not check the tag's signature.
	Tagger             string `json:"tagger"`
	TaggerEmail        string `json:"tagger_email"`
	TaggedAt           string `json:"tagged_at"`
	Verified           bool   `json:"verified"`
	VerificationReason string `json:"verification_reason"`
}

func tagFrom(tag *gitea.Tag) Tag {
	item := Tag{Name: tag.Name, ID: tag.ID, ZipballURL: tag.ZipballURL, TarballURL: tag.TarballURL}
	if tag.Commit != nil {
		item.Commit = tag.Commit.SHA
	}
	item.Annotated = item.ID != "" && item.Commit != "" && item.ID != item.Commit
	if item.Annotated { // for a lightweight tag, Gitea answers the message of the commit
		item.Message = tag.Message
	}
	return item
}

// Tags manages the tags of repositories.
type Tags struct{ Client *transport.Client }

// List lists a page of tags.
func (s Tags) List(ctx context.Context, repo string, limit, page int) (Page[Tag], error) {
	window, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Tag, http.Header, error) {
		var headers http.Header
		tags, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Tag, *gitea.Response, error) {
			tags, resp, err := c.Repositories.ListRepoTags(ctx, owner, name, gitea.ListRepoTagsOptions{ListOptions: listOptions(limit, page)})
			headers = headerOf(resp)
			return tags, resp, err
		})
		return tags, headers, err
	})
	if err != nil {
		return Page[Tag]{}, repositoryItemError(ctx, s.Client, repo, err, "repository", repo)
	}
	return convertPage(window, tagFrom), nil
}

// View returns a tag.
func (s Tags) View(ctx context.Context, repo, name string) (Tag, error) {
	tag, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, repoName string) (*gitea.Tag, *gitea.Response, error) {
		return c.Repositories.GetTag(ctx, owner, repoName, name)
	})
	if err != nil {
		return Tag{}, repositoryItemError(ctx, s.Client, repo, err, "tag", strconv.Quote(name))
	}
	return tagFrom(tag), nil
}

// WithTagObject adds who tagged an annotated tag and when, and the signature check of its commit.
func (s Tags) WithTagObject(ctx context.Context, repo string, item Tag) (Tag, error) {
	if !item.Annotated {
		return item, nil
	}
	annotated, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, repoName string) (*gitea.AnnotatedTag, *gitea.Response, error) {
		return c.Repositories.GetAnnotatedTag(ctx, owner, repoName, item.ID)
	})
	if IsNotFound(err) {
		return item, nil // the tag is shown without who made it
	}
	if err != nil {
		return Tag{}, Classify(err, "tag", strconv.Quote(item.Name), repo)
	}
	if annotated.Tagger != nil {
		item.Tagger, item.TaggerEmail, item.TaggedAt = annotated.Tagger.Name, annotated.Tagger.Email, annotated.Tagger.Date
	}
	if annotated.Verification != nil {
		item.Verified, item.VerificationReason = annotated.Verification.Verified, annotated.Verification.Reason
	}
	return item, nil
}

// Create creates a tag on target, a branch or commit, which is the default branch when empty. A
// message makes the tag annotated.
func (s Tags) Create(ctx context.Context, repo, name, target, message string) (Tag, error) {
	tag, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, repoName string) (*gitea.Tag, *gitea.Response, error) {
		return c.Repositories.CreateTag(ctx, owner, repoName, gitea.CreateTagOption{TagName: name, Target: target, Message: message})
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusNotFound && strings.Contains(apiErr.Message, "target not found") {
		key := "" // no target means the default branch, which an empty repository lacks
		if target != "" {
			key = strconv.Quote(target)
		}
		return Tag{}, &NotFoundError{Kind: "target", Key: key, Scope: repo, Err: err}
	}
	if err != nil {
		return Tag{}, repositoryItemError(ctx, s.Client, repo, err, "tag", strconv.Quote(name))
	}
	return tagFrom(tag), nil
}

// Delete deletes a tag.
func (s Tags) Delete(ctx context.Context, repo, name string) error {
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, repoName string) (*gitea.Response, error) {
		return c.Repositories.DeleteTag(ctx, owner, repoName, name)
	})
	return repositoryItemError(ctx, s.Client, repo, err, "tag", strconv.Quote(name))
}

// TagProtection is a rule that lets only some users and teams create the tags matching a pattern.
type TagProtection struct {
	ID          int64  `json:"id"`
	NamePattern string `json:"name_pattern"`
	// Users and Teams may create the matching tags; without them nobody may.
	Users     []string `json:"users"`
	Teams     []string `json:"teams"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func tagProtectionFrom(rule *gitea.TagProtection) TagProtection {
	return TagProtection{
		ID: rule.ID, NamePattern: rule.NamePattern, Users: append([]string{}, rule.WhitelistUsernames...), Teams: append([]string{}, rule.WhitelistTeams...),
		CreatedAt: timestamp(rule.Created), UpdatedAt: timestamp(rule.Updated),
	}
}

// TagProtections manages the tag protection rules of repositories.
type TagProtections struct{ Client *transport.Client }

// List lists every rule, in the order that Gitea applies them. Gitea answers all of them at once and
// ignores page and limit, so a read in pages would never end.
func (s TagProtections) List(ctx context.Context, repo string) ([]TagProtection, error) {
	rules, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.TagProtection, *gitea.Response, error) {
		return c.Repositories.ListTagProtection(ctx, owner, name, gitea.ListRepoTagProtectionsOptions{})
	})
	if err != nil {
		return nil, repositoryItemError(ctx, s.Client, repo, err, "repository", repo)
	}
	return convertAll(rules, tagProtectionFrom), nil
}

// RuleIDs returns the ID of the rule of each pattern, for a batch of Protect and Unprotect calls to
// share and keep current.
func (s TagProtections) RuleIDs(ctx context.Context, repo string) (map[string]int64, error) {
	rules, err := s.List(ctx, repo)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]int64, len(rules))
	for _, rule := range slices.Backward(rules) { // the first rule of a pattern is the one that applies
		ids[rule.NamePattern] = rule.ID
	}
	return ids, nil
}

// ErrTagProtectionNeedsAllowed is the failure to add a tag protection rule that lets nobody create tags.
var ErrTagProtectionNeedsAllowed = errors.New("a tag protection rule needs at least one user or team that may create the tags")

// TagProtectionChanges are the parts of a rule to set; nil leaves one as it is and empty clears it.
type TagProtectionChanges struct {
	Users, Teams []string
}

// OwnerIsOrganization reports whether owner is an organization, the only owner with teams.
func (s TagProtections) OwnerIsOrganization(ctx context.Context, owner string) (bool, error) {
	_, isOrg, err := organization(ctx, s.Client, owner)
	return isOrg, err
}

// Protect adds a rule for pattern, or changes the one in ids. It returns the rule and whether it
// added one.
func (s TagProtections) Protect(ctx context.Context, repo, pattern string, changes TagProtectionChanges, ids map[string]int64) (item TagProtection, created bool, err error) {
	if id := ids[pattern]; id != 0 { // a dry run's create answers without an ID, so it stays a create
		rule, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.TagProtection, *gitea.Response, error) {
			return c.Repositories.EditTagProtection(ctx, owner, name, id, gitea.EditTagProtectionOption{WhitelistUsernames: changes.Users, WhitelistTeams: changes.Teams})
		})
		if err != nil {
			return TagProtection{}, false, Classify(err, "tag protection", pattern, "")
		}
		return tagProtectionFrom(rule), false, nil
	}
	if len(changes.Users)+len(changes.Teams) == 0 {
		return TagProtection{}, false, ErrTagProtectionNeedsAllowed // Gitea refuses such a rule with a 400
	}
	rule, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.TagProtection, *gitea.Response, error) {
		return c.Repositories.CreateTagProtection(ctx, owner, name, gitea.CreateTagProtectionOption{
			NamePattern: pattern, WhitelistUsernames: append([]string{}, changes.Users...), WhitelistTeams: append([]string{}, changes.Teams...),
		})
	})
	if err != nil {
		return TagProtection{}, false, Classify(err, "tag protection", pattern, "")
	}
	ids[pattern] = rule.ID
	return tagProtectionFrom(rule), true, nil
}

// Unprotect deletes the rule for pattern in ids.
func (s TagProtections) Unprotect(ctx context.Context, repo, pattern string, ids map[string]int64) error {
	id, ok := ids[pattern]
	if !ok {
		return &NotFoundError{Kind: "tag protection", Key: strconv.Quote(pattern), Scope: repo}
	}
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.DeleteTagProtection(ctx, owner, name, id)
	})
	if err != nil {
		return Classify(err, "tag protection", pattern, "")
	}
	delete(ids, pattern)
	return nil
}
