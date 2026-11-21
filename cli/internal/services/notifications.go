// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Notification is the stable representation of an account notification thread.
type Notification struct {
	ID         int64  `json:"id"`
	Repository string `json:"repository"`
	// Number is the issue or pull request number of the subject, when it is one.
	Number    int64  `json:"number,omitempty"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Type      string `json:"type"`
	State     string `json:"state"`
	Unread    bool   `json:"unread"`
	Pinned    bool   `json:"pinned"`
	UpdatedAt string `json:"updated_at"`
	// latestComment is the API URL of the latest comment on the subject.
	latestComment string
}

func notificationFrom(thread *gitea.NotificationThread) Notification {
	result := Notification{ID: thread.ID, Unread: thread.Unread, Pinned: thread.Pinned, UpdatedAt: timestamp(thread.UpdatedAt)}
	if thread.Repository != nil {
		result.Repository = thread.Repository.FullName
	}
	if subject := thread.Subject; subject != nil {
		result.Title = subject.Title
		result.URL = subject.HTMLURL
		result.Type = strings.ToLower(string(subject.Type))
		result.State = string(subject.State)
		result.latestComment = subject.LatestCommentURL
		if subject.Type == gitea.NotificationSubjectIssue || subject.Type == gitea.NotificationSubjectPull {
			result.Number = lastPathNumber(subject.URL)
		}
	}
	return result
}

// lastPathNumber returns the number that ends the path of a URL, such as 17 of …/issues/17, or 0.
func lastPathNumber(raw string) int64 {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	number, err := strconv.ParseInt(path.Base(u.Path), 10, 64)
	if err != nil {
		return 0
	}
	return number
}

func notificationStatuses(statuses []string) []gitea.NotificationStatus {
	result := make([]gitea.NotificationStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, gitea.NotificationStatus(status))
	}
	return result
}

// NotificationOptions supplies pagination and server-supported filters.
type NotificationOptions struct {
	Limit, Page     int
	Statuses, Types []string
	Since, Before   *time.Time
}

func (o NotificationOptions) sdk() gitea.ListNotificationOptions {
	result := gitea.ListNotificationOptions{ListOptions: listOptions(o.Limit, o.Page), Status: notificationStatuses(o.Statuses)}
	for _, kind := range o.Types {
		result.SubjectTypes = append(result.SubjectTypes, gitea.NotificationSubjectType(kind))
	}
	if o.Since != nil {
		result.Since = *o.Since
	}
	if o.Before != nil {
		result.Before = *o.Before
	}
	return result
}

// Notifications manages account threads, optionally scoped to one repository.
type Notifications struct {
	Client     *transport.Client
	Repository string
}

// List returns a non-nil page of notifications and what the server reported about the rest.
func (s Notifications) List(ctx context.Context, opts NotificationOptions) (Page[Notification], error) {
	return instancePage(ctx, s.Client, opts.Limit, opts.Page, func(limit, page int) ([]Notification, http.Header, error) {
		window := opts
		window.Limit, window.Page = limit, page
		threads, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.NotificationThread, *gitea.Response, error) {
			if s.Repository == "" {
				return c.Notifications.List(ctx, window.sdk())
			}
			owner, name, err := splitRepository(s.Repository)
			if err != nil {
				return nil, nil, err
			}
			return c.Notifications.ListByRepo(ctx, owner, name, window.sdk())
		})
		if err != nil {
			if s.Repository != "" {
				// A 404 here can only mean the repository is missing or invisible.
				err = Classify(err, "repository", s.Repository, "")
			}
			return nil, nil, err
		}
		return convertAll(threads, notificationFrom), headerOf(resp), nil
	})
}

// View retrieves an account-owned notification thread.
func (s Notifications) View(ctx context.Context, id int64) (Notification, error) {
	thread, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.NotificationThread, *gitea.Response, error) {
		return c.Notifications.GetByID(ctx, id)
	})
	if err != nil {
		return Notification{}, Classify(err, "notification", strconv.FormatInt(id, 10), "")
	}
	item := notificationFrom(thread)
	if s.Repository != "" && !strings.EqualFold(item.Repository, s.Repository) {
		return Notification{}, &ElsewhereError{ID: id, Repository: item.Repository, Selected: s.Repository}
	}
	return item, nil
}

// ElsewhereError reports a notification of another repository than the selected one.
type ElsewhereError struct {
	ID                   int64
	Repository, Selected string
}

func (e *ElsewhereError) Error() string {
	return fmt.Sprintf("notification %d belongs to %s, not selected repository %s", e.ID, e.Repository, e.Selected)
}

// LatestComment returns the latest comment on the subject of a notification and whether it exists.
func (s Notifications) LatestComment(ctx context.Context, item Notification) (Comment, bool, error) {
	id := lastPathNumber(item.latestComment)
	if id == 0 || item.Repository == "" {
		return Comment{}, false, nil
	}
	comment, err := (Comments{Client: s.Client, Repository: item.Repository}).View(ctx, id)
	if err != nil {
		return Comment{}, false, err
	}
	return comment, true, nil
}

// Mark changes the selected notification status.
func (s Notifications) Mark(ctx context.Context, id int64, status string) (Notification, error) {
	if s.Repository != "" {
		if _, err := s.View(ctx, id); err != nil {
			return Notification{}, err
		}
	}
	thread, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.NotificationThread, *gitea.Response, error) {
		return c.Notifications.MarkReadByID(ctx, id, gitea.NotificationStatus(status))
	})
	if err != nil {
		return Notification{}, Classify(err, "notification", strconv.FormatInt(id, 10), "")
	}
	return notificationFrom(thread), nil
}

// MarkAll applies a status to all matching notifications within the selected scope.
func (s Notifications) MarkAll(ctx context.Context, status string, filters []string, before time.Time) ([]Notification, error) {
	opts := gitea.MarkNotificationOptions{LastReadAt: before, Status: notificationStatuses(filters), ToStatus: gitea.NotificationStatus(status)}
	threads, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.NotificationThread, *gitea.Response, error) {
		if s.Repository == "" {
			return c.Notifications.MarkRead(ctx, opts)
		}
		owner, name, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Notifications.MarkReadByRepo(ctx, owner, name, opts)
	})
	if err != nil && s.Repository != "" {
		return nil, Classify(err, "repository", s.Repository, "")
	}
	if err != nil {
		return nil, err
	}
	return convertAll(threads, notificationFrom), nil
}

// Watch reports new or changed threads and propagates cancellation and callback errors. It polls
// on through an outage of the instance of up to pollOutage once a poll succeeded. The first poll
// reports the page opts selects; later polls read on past it while every thread on a page is new,
// so a burst of more than a page of notifications between two polls is reported whole.
func (s Notifications) Watch(ctx context.Context, opts NotificationOptions, interval time.Duration, onChange func([]Notification) error) error {
	if interval <= 0 {
		return errors.New("notification polling interval must be positive")
	}
	seen := map[int64]Notification{}
	var newest time.Time
	var failures outage
	for polled := false; ; polled = true {
		changes, next, err := s.poll(ctx, opts, seen, newest, polled)
		if failures.retry(ctx, err) {
			if err := sleepCtx(ctx, interval); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		failures.passed()
		seen = next
		for _, item := range next {
			if updated := updatedAt(item); updated.After(newest) {
				newest = updated
			}
		}
		if len(changes) > 0 {
			if err := onChange(changes); err != nil {
				return err
			}
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
	}
}

// poll reads the page opts selects and returns the threads on it that are new or changed since
// seen, together with every thread it read. With readOn, it reads on while a page holds only such
// threads, up to the first thread that is unchanged or was last updated before newest, the latest
// update the previous polls read: notifications come newest first, so the threads past it are
// older ones.
func (s Notifications) poll(ctx context.Context, opts NotificationOptions, seen map[int64]Notification, newest time.Time, readOn bool) ([]Notification, map[int64]Notification, error) {
	changes := []Notification{}
	next := map[int64]Notification{}
	for page := opts.Page; ; page++ {
		window := opts
		window.Page = page
		listed, err := s.List(ctx, window)
		if err != nil {
			return nil, nil, err
		}
		fresh := true
		for _, item := range listed.Items {
			previous, exists := seen[item.ID]
			changed := !exists || previous != item
			if page != opts.Page && (!changed || updatedAt(item).Before(newest)) {
				return changes, next, nil
			}
			next[item.ID] = item
			if changed {
				changes = append(changes, item)
			} else {
				fresh = false
			}
		}
		if !readOn || !fresh || !listed.More || len(listed.Items) == 0 || page-opts.Page+1 >= maxListPages {
			return changes, next, nil
		}
	}
}

// updatedAt is the time a thread was last updated, or the zero time when it is unknown.
func updatedAt(item Notification) time.Time {
	updated, _ := time.Parse(time.RFC3339Nano, item.UpdatedAt)
	return updated
}
