// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Organization is the public command representation of a Gitea organization.
type Organization struct {
	ID                        int64  `json:"id"`
	Name                      string `json:"name"`
	FullName                  string `json:"full_name"`
	Description               string `json:"description"`
	Website                   string `json:"website"`
	Location                  string `json:"location"`
	Visibility                string `json:"visibility"`
	RepoAdminChangeTeamAccess bool   `json:"repo_admin_change_team_access"`
}

// organization returns the ID of owner when it is an organization; a missing organization
// means a user owns the namespace.
func organization(ctx context.Context, client *transport.Client, owner string) (id int64, isOrg bool, err error) {
	org, _, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Organizations.GetOrg(ctx, owner)
	})
	if failure, ok := errors.AsType[*transport.APIError](err); ok && failure.Status == http.StatusNotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if org.ID < 1 {
		return 0, false, fmt.Errorf("cannot verify the identity of organization %s", owner)
	}
	return org.ID, true, nil
}

// OrganizationOptions permits explicit empty and false preference updates.
type OrganizationOptions struct {
	Name                      *string `json:"username,omitempty"`
	FullName                  *string `json:"full_name,omitempty"`
	Description               *string `json:"description,omitempty"`
	Website                   *string `json:"website,omitempty"`
	Location                  *string `json:"location,omitempty"`
	Visibility                *string `json:"visibility,omitempty"`
	RepoAdminChangeTeamAccess *bool   `json:"repo_admin_change_team_access,omitempty"`
}

// Organizations implements instance-scoped organization workflows.
type Organizations struct{ Client *transport.Client }

// List selects public organizations, your organizations, or a named user's organizations.
// It reports whether more organizations follow the selected page.
func (s Organizations) List(ctx context.Context, mine bool, username string, limit, page int) ([]Organization, bool, error) {
	orgs, more, err := listWindow(limit, page, func(limit, page int) ([]*gitea.Organization, http.Header, error) {
		orgs, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Organization, *gitea.Response, error) {
			opts := gitea.ListOrgsOptions{ListOptions: listOptions(limit, page)}
			switch {
			case mine:
				return c.Organizations.ListMyOrgs(ctx, opts)
			case username != "":
				return c.Organizations.ListUserOrgs(ctx, username, opts)
			}
			return c.Organizations.ListOrgs(ctx, opts)
		})
		return orgs, headerOf(resp), err
	})
	if err != nil {
		return nil, false, err
	}
	return convertAll(orgs, organizationFrom), more, nil
}

// View retrieves a named organization.
func (s Organizations) View(ctx context.Context, name string) (Organization, error) {
	org, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Organizations.GetOrg(ctx, name)
	})
	if err != nil {
		return Organization{}, err
	}
	return organizationFrom(org), nil
}

// Create creates an organization owned by the selected account.
func (s Organizations) Create(ctx context.Context, opts OrganizationOptions) (Organization, error) {
	org, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Organizations.CreateOrg(ctx, gitea.CreateOrgOption{
			Name: deref(opts.Name), FullName: deref(opts.FullName), Description: deref(opts.Description),
			Website: deref(opts.Website), Location: deref(opts.Location), Visibility: gitea.VisibleType(deref(opts.Visibility)),
			RepoAdminChangeTeamAccess: deref(opts.RepoAdminChangeTeamAccess),
		})
	})
	if err != nil {
		return Organization{}, err
	}
	return organizationFrom(org), nil
}

// Edit changes only supplied organization preferences.
func (s Organizations) Edit(ctx context.Context, name string, opts OrganizationOptions) (Organization, error) {
	var org gitea.Organization
	// TODO(sdk): EditOrgOption needs pointer fields so omitted preferences stay unchanged, and EditOrg should return the organization.
	if err := sendJSON(ctx, s.Client, http.MethodPatch, "orgs/"+url.PathEscape(name), opts, &org); err != nil {
		return Organization{}, err
	}
	return organizationFrom(&org), nil
}

// Delete deletes the named organization.
func (s Organizations) Delete(ctx context.Context, name string) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Organizations.DeleteOrg(ctx, name)
	})
}

// OrganizationMember identifies a member without exposing private profile fields.
type OrganizationMember struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	FullName string `json:"full_name"`
}

// Members lists visible members in an organization.
func (s Organizations) Members(ctx context.Context, name string, public bool, limit, page int) ([]OrganizationMember, error) {
	users, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.User, *gitea.Response, error) {
		opts := gitea.ListOrgMembershipOption{ListOptions: listOptions(limit, page)}
		if public {
			return c.Organizations.ListPublicOrgMembership(ctx, name, opts)
		}
		return c.Organizations.ListOrgMembership(ctx, name, opts)
	})
	if err != nil {
		return nil, err
	}
	return convertAll(users, memberFrom), nil
}

// RemoveMember removes an organization member and their organization access.
func (s Organizations) RemoveMember(ctx context.Context, name, username string) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Organizations.DeleteOrgMembership(ctx, name, username)
	})
}

// SetMemberPublic changes public visibility of an existing membership.
func (s Organizations) SetMemberPublic(ctx context.Context, name, username string, public bool) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Organizations.SetPublicOrgMembership(ctx, name, username, public)
	})
}
