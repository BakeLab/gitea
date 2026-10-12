// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
)

// CapabilityError reports an operation absent from the Gitea REST contract.
type CapabilityError struct{ Feature, Reason string }

func (e *CapabilityError) Error() string { return e.Feature + " is unsupported: " + e.Reason }

// NotFoundError reports a missing resource by the name the user gave it, such as
// "issue #999 not found in alice/project".
type NotFoundError struct {
	// Kind names the resource, such as "issue" or "label".
	Kind string
	// Key identifies the resource as the user wrote it, such as "#999", "v1.0" or `"bug"`.
	Key string
	// Scope is the repository or organization that was searched; empty for instance-wide resources.
	Scope string
	// Err is the underlying failure, usually a 404 *transport.APIError.
	Err error
}

func (e *NotFoundError) Error() string {
	text := strings.TrimSpace(e.Kind+" "+e.Key) + " not found"
	if e.Scope != "" {
		text += " in " + e.Scope
	}
	return text
}

func (e *NotFoundError) Unwrap() error { return e.Err }

// NotFound reports that no kind named key exists in scope.
func NotFound(kind, key, scope string) *NotFoundError {
	return &NotFoundError{Kind: kind, Key: key, Scope: scope}
}

// Reasons a ConflictError gives for refusing a request.
const (
	ReasonArchived  = "archived"          // the repository is archived and read-only
	ReasonExists    = "exists"            // a resource with the same identity already exists
	ReasonMerged    = "merged"            // the pull request is already merged
	ReasonDraft     = "draft"             // a draft pull request cannot be merged
	ReasonOwnReview = "own review"        // authors cannot approve or reject their own pull request
	ReasonNoChanges = "no changes"        // the head branch has no commits the base lacks
	ReasonDisabled  = "disabled"          // the workflow is disabled
	ReasonCompleted = "completed"         // the run already finished
	ReasonNotDone   = "not done"          // the run has not finished yet
	ReasonKeyInUse  = "key in use"        // the SSH key is registered already
	ReasonOwnsRepos = "owns repositories" // the user still owns repositories
)

// ConflictError reports a request the current state of a resource refuses, such as closing a
// merged pull request or writing to an archived repository.
type ConflictError struct {
	// Reason is one of the Reason constants.
	Reason string
	// Kind, Key and Scope name the refused resource like NotFoundError; any may be empty.
	Kind, Key, Scope string
	// Hint is the next step, such as "use gt release edit v1.0"; callers add it.
	Hint string
	// Err is the underlying *transport.APIError.
	Err error
}

func (e *ConflictError) Error() string {
	subject := strings.TrimSpace(e.Kind + " " + e.Key)
	var text string
	switch e.Reason {
	case ReasonArchived:
		text = cmp.Or(e.Scope, "the repository") + " is archived (read-only)"
	case ReasonExists:
		text = cmp.Or(subject, "the resource") + " already exists"
		if e.Scope != "" {
			text += " in " + e.Scope
		}
	case ReasonMerged:
		text = cmp.Or(subject, "the pull request") + " is already merged"
	case ReasonDraft:
		text = cmp.Or(subject, "the pull request") + " is a draft"
	case ReasonOwnReview:
		text = "you can't approve or request changes on your own pull request"
	case ReasonNoChanges:
		text = "no commits between the base and head branches"
	case ReasonDisabled:
		text = cmp.Or(subject, "the workflow") + " is disabled"
	case ReasonCompleted:
		text = cmp.Or(subject, "the run") + " is already completed"
	case ReasonNotDone:
		text = cmp.Or(subject, "the run") + " has not finished"
	case ReasonKeyInUse:
		text = "this SSH key is already registered"
	case ReasonOwnsRepos:
		text = cmp.Or(subject, "the user") + " still owns repositories"
	default:
		text = e.Err.Error()
	}
	if e.Hint != "" {
		text += "; " + e.Hint
	}
	return text
}

func (e *ConflictError) Unwrap() error { return e.Err }

// AmbiguousError reports a name that matches several resources.
type AmbiguousError struct {
	Kind, Key, Scope string
	IDs              []int64
}

func (e *AmbiguousError) Error() string {
	ids := make([]string, len(e.IDs))
	for i, id := range e.IDs {
		ids[i] = strconv.FormatInt(id, 10)
	}
	text := e.Kind + " " + e.Key + " is ambiguous"
	if e.Scope != "" {
		text += " in " + e.Scope
	}
	return text + " (IDs " + strings.Join(ids, ", ") + "); pass an ID"
}

// refusals maps the messages Gitea gives for refused requests onto conflict reasons. Each entry
// lists lowercase message fragments; statuses, when set, limit an entry to those responses.
var refusals = []struct {
	reason    string
	statuses  []int
	fragments []string
}{
	{ReasonArchived, nil, []string{"repo is archived", "repository is archived", "> is archived"}},
	{ReasonMerged, nil, []string{"already merged"}},
	{ReasonDraft, []int{http.StatusMethodNotAllowed}, []string{"work in progress", "draft"}},
	{ReasonOwnReview, nil, []string{"approve your own pull", "reject your own pull"}},
	{ReasonNoChanges, nil, []string{"no changes between the head and the base"}},
	{ReasonDisabled, nil, []string{"workflow is disabled"}},
	{ReasonCompleted, nil, []string{"already completed"}},
	{ReasonNotDone, []int{http.StatusBadRequest}, []string{"not done"}},
	{ReasonKeyInUse, nil, []string{"key content has been used"}},
	{ReasonOwnsRepos, nil, []string{"ownership of repositories"}},
	{ReasonExists, []int{http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity}, []string{"already exist", "release is has no tag"}},
}

var (
	// unknownUser finds the name in Gitea's "user does not exist [uid: 0, name: nobody]".
	unknownUser = regexp.MustCompile(`user does not exist \[uid: \d+, name: ([^\]]+)\]`)
	// pullTargets finds the branches in "pull request already exists for these targets [... head_branch: x, base_branch: y]".
	pullTargets = regexp.MustCompile(`head_branch: ([^,\]]+), base_branch: ([^,\]]+)`)
	// wikiTitle finds the page in "wiki page already exists [title: Home.md]".
	wikiTitle = regexp.MustCompile(`\[title: (.+?)(?:\.md)?\]`)
)

// Classify names the resource of a failed request for the user: a 404 becomes a NotFoundError
// for kind key in scope, a refusal Gitea explains (archived repository, merged pull request,
// existing resource, ...) becomes a ConflictError, and an unknown user the request named becomes
// a NotFoundError for that user. Other errors are returned unchanged.
func Classify(err error, kind, key, scope string) error {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok {
		return err
	}
	if match := unknownUser.FindStringSubmatch(apiErr.Message); match != nil {
		return &NotFoundError{Kind: "user", Key: strconv.Quote(match[1]), Err: err}
	}
	if reason := refusal(apiErr); reason != "" {
		conflict := &ConflictError{Reason: reason, Kind: kind, Key: key, Scope: scope, Err: err}
		if reason == ReasonArchived && kind == "repository" {
			conflict.Kind, conflict.Key, conflict.Scope = "", "", key
		}
		if match := pullTargets.FindStringSubmatch(apiErr.Message); reason == ReasonExists && match != nil {
			conflict.Kind, conflict.Key = "pull request", "for "+match[1]+" into "+match[2]
		}
		if match := wikiTitle.FindStringSubmatch(apiErr.Message); reason == ReasonExists && match != nil {
			conflict.Kind, conflict.Key = "wiki page", strconv.Quote(match[1])
		}
		return conflict
	}
	if apiErr.Status == http.StatusNotFound && kind != "" {
		return &NotFoundError{Kind: kind, Key: key, Scope: scope, Err: err}
	}
	return err
}

// IsNotFound reports whether err is a 404 response.
func IsNotFound(err error) bool {
	failure, ok := errors.AsType[*transport.APIError](err)
	return ok && failure.Status == http.StatusNotFound
}

// refusal returns the conflict reason of a refused request, or "".
func refusal(apiErr *transport.APIError) string {
	if apiErr.Status == http.StatusLocked {
		return ReasonArchived
	}
	message := strings.ToLower(apiErr.Message)
	for _, refusal := range refusals {
		if refusal.statuses != nil && !slices.Contains(refusal.statuses, apiErr.Status) {
			continue
		}
		for _, fragment := range refusal.fragments {
			if strings.Contains(message, fragment) {
				return refusal.reason
			}
		}
	}
	return ""
}
