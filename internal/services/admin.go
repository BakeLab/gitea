// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Rename changes the username of an account.
func (s AdminUsers) Rename(ctx context.Context, username, newUsername string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.RenameUser(ctx, username, gitea.RenameUserOption{NewUsername: newUsername})
	})
	return Classify(err, "user", strconv.Quote(newUsername), "")
}

// CreateKey adds a public key to another account.
func (s AdminUsers) CreateKey(ctx context.Context, username, title, key string) (SSHKey, error) {
	created, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.PublicKey, *gitea.Response, error) {
		return c.Admin.CreateUserPublicKey(ctx, username, gitea.CreateKeyOption{Title: title, Key: key}) // no read-only option: Gitea stores user keys with write access regardless
	})
	if err != nil {
		return SSHKey{}, Classify(err, "SSH key", "", "")
	}
	return sshKeyFrom(created), nil
}

// DeleteKey removes a public key of another account.
func (s AdminUsers) DeleteKey(ctx context.Context, username string, id int64) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.DeleteUserPublicKey(ctx, username, int(id))
	})
	return Classify(err, "SSH key", strconv.FormatInt(id, 10), "")
}

// AdminBadge is a badge that can be awarded to accounts.
type AdminBadge struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url"`
}

func adminBadgeFrom(badge *gitea.Badge) AdminBadge {
	return AdminBadge{ID: badge.ID, Slug: badge.Slug, Description: badge.Description, ImageURL: badge.ImageURL}
}

// Badges lists the badges of an account.
func (s AdminUsers) Badges(ctx context.Context, username string) ([]AdminBadge, error) {
	badges, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Badge, *gitea.Response, error) {
		return c.Admin.ListUserBadges(ctx, username)
	})
	if err != nil {
		return nil, Classify(err, "user", strconv.Quote(username), "")
	}
	return convertAll(badges, adminBadgeFrom), nil
}

// AddBadges awards badges, named by slug, to an account.
func (s AdminUsers) AddBadges(ctx context.Context, username string, slugs []string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.AddUserBadges(ctx, username, gitea.UserBadgeOption{BadgeSlugs: slugs})
	})
	return Classify(err, "user", strconv.Quote(username), "")
}

// RemoveBadges takes badges, named by slug, from an account.
func (s AdminUsers) RemoveBadges(ctx context.Context, username string, slugs []string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.DeleteUserBadge(ctx, username, gitea.UserBadgeOption{BadgeSlugs: slugs})
	})
	return Classify(err, "user", strconv.Quote(username), "")
}

// AdminOrganizations provides instance-wide organization operations for administrators.
type AdminOrganizations struct{ Client *transport.Client }

// List returns a page of every organization of the instance, private ones included.
func (s AdminOrganizations) List(ctx context.Context, limit, page int) (Page[Organization], error) {
	orgs, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Organization, *gitea.Response, error) {
		return c.Admin.ListOrgs(ctx, gitea.AdminListOrgsOptions{ListOptions: list})
	})
	return convertPage(orgs, organizationFrom), err
}

// Create creates an organization owned by another user.
func (s AdminOrganizations) Create(ctx context.Context, owner string, opts OrganizationOptions) (Organization, error) {
	org, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Admin.CreateOrg(ctx, owner, createOrgOption(opts))
	})
	if err != nil {
		return Organization{}, Classify(err, "name", strconv.Quote(deref(opts.Name)), "")
	}
	return organizationFrom(org), nil
}

// CronTask is a scheduled background task of the instance.
type CronTask struct {
	Name      string `json:"name"`
	Schedule  string `json:"schedule"`
	Next      string `json:"next"`
	Prev      string `json:"prev"`
	ExecTimes int64  `json:"exec_times"`
}

func cronTaskFrom(task *gitea.CronTask) CronTask {
	return CronTask{Name: task.Name, Schedule: task.Schedule, Next: timestamp(task.Next), Prev: timestamp(task.Prev), ExecTimes: task.ExecTimes}
}

// AdminCron lists and starts the scheduled tasks.
type AdminCron struct{ Client *transport.Client }

// List returns a page of scheduled tasks.
func (s AdminCron) List(ctx context.Context, limit, page int) (Page[CronTask], error) {
	tasks, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.CronTask, *gitea.Response, error) {
		return c.Admin.ListCronTasks(ctx, gitea.ListCronTaskOptions{ListOptions: list})
	})
	return convertPage(tasks, cronTaskFrom), err
}

// Run starts a scheduled task now.
func (s AdminCron) Run(ctx context.Context, task string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.RunCronTasks(ctx, task)
	})
	return Classify(err, "cron task", strconv.Quote(task), "")
}

// AdminEmail is an email address of an account.
type AdminEmail struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	UserID   int64  `json:"user_id"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func adminEmailFrom(email *gitea.Email) AdminEmail {
	return AdminEmail{Email: email.Email, Username: email.Username, UserID: email.UserID, Primary: email.Primary, Verified: email.Verified}
}

// AdminEmails lists the email addresses of all accounts.
type AdminEmails struct{ Client *transport.Client }

// List returns a page of the addresses whose address, username or full name contains query, or of
// every address without one.
func (s AdminEmails) List(ctx context.Context, query string, limit, page int) (Page[AdminEmail], error) {
	emails, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Email, *gitea.Response, error) {
		if query == "" {
			return c.Admin.ListEmails(ctx, gitea.ListAdminEmailsOptions{ListOptions: list})
		}
		return c.Admin.SearchEmails(ctx, gitea.SearchAdminEmailsOptions{ListOptions: list, Query: query})
	})
	return convertPage(emails, adminEmailFrom), err
}
