// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// AdminUser is the explicit account-management result, without authentication secrets.
type AdminUser struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	Email         string `json:"email"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Website       string `json:"website"`
	Location      string `json:"location"`
	URL           string `json:"url"`
	Admin         bool   `json:"admin"`
	Active        bool   `json:"active"`
	Restricted    bool   `json:"restricted"`
	ProhibitLogin bool   `json:"prohibit_login"`
	Visibility    string `json:"visibility"`
	CreatedAt     string `json:"created_at"`
}

func adminUserFrom(user *gitea.User) AdminUser {
	return AdminUser{
		ID: user.ID, Username: user.UserName, Email: user.Email, FullName: user.FullName, Description: user.Description,
		Website: user.Website, Location: user.Location, URL: user.HTMLURL, Admin: user.IsAdmin, Active: user.IsActive,
		Restricted: user.Restricted, ProhibitLogin: user.ProhibitLogin, Visibility: string(user.Visibility), CreatedAt: timestamp(user.Created),
	}
}

// AdminUserOptions distinguishes omitted fields from explicit empty or false values.
type AdminUserOptions struct {
	Email                   *string `json:"email,omitempty"`
	FullName                *string `json:"full_name,omitempty"`
	Description             *string `json:"description,omitempty"`
	Website                 *string `json:"website,omitempty"`
	Location                *string `json:"location,omitempty"`
	Visibility              *string `json:"visibility,omitempty"`
	Password                string  `json:"password,omitempty"`
	Admin                   *bool   `json:"admin,omitempty"`
	Active                  *bool   `json:"active,omitempty"`
	Restricted              *bool   `json:"restricted,omitempty"`
	ProhibitLogin           *bool   `json:"prohibit_login,omitempty"`
	MustChangePassword      *bool   `json:"must_change_password,omitempty"`
	AllowGitHook            *bool   `json:"allow_git_hook,omitempty"`
	AllowImportLocal        *bool   `json:"allow_import_local,omitempty"`
	AllowCreateOrganization *bool   `json:"allow_create_organization,omitempty"`
	MaxRepoCreation         *int    `json:"max_repo_creation,omitempty"`
}

func visibilityOf(visibility *string) *gitea.VisibleType {
	if visibility == nil {
		return nil
	}
	value := gitea.VisibleType(*visibility)
	return &value
}

// AdminUsers provides administrator account operations.
type AdminUsers struct{ Client *transport.Client }

// List returns a page of users visible to the instance administrator.
func (s AdminUsers) List(ctx context.Context, limit, page int) ([]AdminUser, error) {
	users, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.User, *gitea.Response, error) {
		return c.Admin.ListUsers(ctx, gitea.AdminListUsersOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(users, adminUserFrom), nil
}

func (s AdminUsers) get(ctx context.Context, username string) (*gitea.User, error) {
	user, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetUserInfo(ctx, username)
	})
	return user, err
}

// View retrieves one account.
func (s AdminUsers) View(ctx context.Context, username string) (AdminUser, error) {
	user, err := s.get(ctx, username)
	if err != nil {
		return AdminUser{}, err
	}
	return adminUserFrom(user), nil
}

// Create creates an account, then applies administrative fields unavailable during creation.
func (s AdminUsers) Create(ctx context.Context, username string, opts AdminUserOptions) (AdminUser, error) {
	created, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Admin.CreateUser(ctx, gitea.CreateUserOption{
			Username: username, LoginName: username, Email: deref(opts.Email), FullName: deref(opts.FullName),
			Password: opts.Password, MustChangePassword: opts.MustChangePassword, Visibility: visibilityOf(opts.Visibility),
		})
	})
	if err != nil {
		return AdminUser{}, err
	}
	if opts.Admin == nil && opts.Restricted == nil && opts.ProhibitLogin == nil {
		return adminUserFrom(created), nil
	}
	followup := AdminUserOptions{Admin: opts.Admin, Restricted: opts.Restricted, ProhibitLogin: opts.ProhibitLogin}
	if err := s.edit(ctx, username, created, followup); err != nil {
		return adminUserFrom(created), fmt.Errorf("user %s created but account restrictions could not be applied: %w", username, err)
	}
	item, err := s.View(ctx, username)
	if err != nil {
		return adminUserFrom(created), fmt.Errorf("user %s created but could not be reloaded: %w", username, err)
	}
	return item, nil
}

func (s AdminUsers) edit(ctx context.Context, username string, current *gitea.User, opts AdminUserOptions) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.EditUser(ctx, username, gitea.EditUserOption{
			SourceID: current.SourceID, LoginName: current.LoginName, Email: opts.Email, FullName: opts.FullName, Password: opts.Password,
			Description: opts.Description, MustChangePassword: opts.MustChangePassword, Website: opts.Website, Location: opts.Location,
			Active: opts.Active, Admin: opts.Admin, AllowGitHook: opts.AllowGitHook, AllowImportLocal: opts.AllowImportLocal,
			MaxRepoCreation: opts.MaxRepoCreation, ProhibitLogin: opts.ProhibitLogin, AllowCreateOrganization: opts.AllowCreateOrganization,
			Restricted: opts.Restricted, Visibility: visibilityOf(opts.Visibility),
		})
	})
}

// Edit preserves the authentication identity while updating account properties.
func (s AdminUsers) Edit(ctx context.Context, username string, opts AdminUserOptions) (AdminUser, error) {
	current, err := s.get(ctx, username)
	if err != nil {
		return AdminUser{}, err
	}
	if err := s.edit(ctx, username, current, opts); err != nil {
		return AdminUser{}, err
	}
	return s.View(ctx, username)
}

// Delete removes an account. Gitea enforces ownership constraints.
func (s AdminUsers) Delete(ctx context.Context, username string) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Admin.DeleteUser(ctx, username)
	})
}
