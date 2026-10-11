// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Notification is the stable representation of an account notification thread.
type Notification struct {
	ID         int64  `json:"id"`
	Repository string `json:"repository"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Type       string `json:"type"`
	State      string `json:"state"`
	Unread     bool   `json:"unread"`
	Pinned     bool   `json:"pinned"`
	UpdatedAt  string `json:"updated_at"`
}

func notificationFrom(thread *gitea.NotificationThread) Notification {
	result := Notification{ID: thread.ID, Unread: thread.Unread, Pinned: thread.Pinned, UpdatedAt: timestamp(thread.UpdatedAt)}
	if thread.Repository != nil {
		result.Repository = thread.Repository.FullName
	}
	if thread.Subject != nil {
		result.Title = thread.Subject.Title
		result.URL = thread.Subject.HTMLURL
		result.Type = string(thread.Subject.Type)
		result.State = string(thread.Subject.State)
	}
	return result
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

// List returns a non-nil page of notifications.
func (s Notifications) List(ctx context.Context, opts NotificationOptions) ([]Notification, error) {
	threads, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.NotificationThread, *gitea.Response, error) {
		if s.Repository == "" {
			return c.Notifications.List(ctx, opts.sdk())
		}
		owner, name, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Notifications.ListByRepo(ctx, owner, name, opts.sdk())
	})
	if err != nil {
		return nil, err
	}
	return convertAll(threads, notificationFrom), nil
}

// View retrieves an account-owned notification thread.
func (s Notifications) View(ctx context.Context, id int64) (Notification, error) {
	thread, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.NotificationThread, *gitea.Response, error) {
		return c.Notifications.GetByID(ctx, id)
	})
	if err != nil {
		return Notification{}, err
	}
	item := notificationFrom(thread)
	if s.Repository != "" && !strings.EqualFold(item.Repository, s.Repository) {
		return Notification{}, fmt.Errorf("notification %d belongs to %s, not selected repository %s", id, item.Repository, s.Repository)
	}
	return item, nil
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
		return Notification{}, err
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
	if err != nil {
		return nil, err
	}
	return convertAll(threads, notificationFrom), nil
}

// Watch reports new or changed threads and propagates cancellation and callback errors.
func (s Notifications) Watch(ctx context.Context, opts NotificationOptions, interval time.Duration, onChange func([]Notification) error) error {
	if interval <= 0 {
		return errors.New("notification polling interval must be positive")
	}
	seen := map[int64]Notification{}
	for {
		items, err := s.List(ctx, opts)
		if err != nil {
			return err
		}
		changes := []Notification{}
		next := map[int64]Notification{}
		for _, item := range items {
			next[item.ID] = item
			if previous, exists := seen[item.ID]; !exists || previous != item {
				changes = append(changes, item)
			}
		}
		seen = next
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
