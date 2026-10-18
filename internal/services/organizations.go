// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
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
	if IsNotFound(err) {
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
func (s Organizations) List(ctx context.Context, mine bool, username string, limit, page int) (Page[Organization], error) {
	orgs, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Organization, *gitea.Response, error) {
		opts := gitea.ListOrgsOptions{ListOptions: list}
		switch {
		case mine:
			return c.Organizations.ListMyOrgs(ctx, opts)
		case username != "":
			return c.Organizations.ListUserOrgs(ctx, username, opts)
		}
		return c.Organizations.ListOrgs(ctx, opts)
	})
	return convertPage(orgs, organizationFrom), err
}

// View retrieves a named organization.
func (s Organizations) View(ctx context.Context, name string) (Organization, error) {
	org, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Organizations.GetOrg(ctx, name)
	})
	if err != nil {
		return Organization{}, Classify(err, "organization", strconv.Quote(name), "")
	}
	return organizationFrom(org), nil
}

// OrganizationSize counts what an organization holds; a count the account cannot read is nil.
type OrganizationSize struct {
	Members, Teams, Repositories *int
}

// Size counts the members, teams and repositories of an organization; a count the account cannot
// read is nil.
func (s Organizations) Size(ctx context.Context, name string) (OrganizationSize, error) {
	first := listOptions(1, 1)
	counts := []func(context.Context, *gitea.Client) (*gitea.Response, error){
		func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			_, resp, err := c.Organizations.ListOrgMembership(ctx, name, gitea.ListOrgMembershipOption{ListOptions: first})
			return resp, err
		},
		func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
			_, resp, err := c.Organizations.ListOrgTeams(ctx, name, gitea.ListTeamsOptions{ListOptions: first})
			return resp, err
		},
		orgRepos(name),
	}
	totals := make([]*int, len(counts))
	_ = concurrently(len(counts), func(i int) error {
		totals[i], _ = listTotal(ctx, s.Client, counts[i])
		return nil
	})
	if ctx.Err() != nil {
		return OrganizationSize{}, ctx.Err()
	}
	return OrganizationSize{Members: totals[0], Teams: totals[1], Repositories: totals[2]}, nil
}

// RepositoryCount counts the repositories of an organization, or returns nil when the account
// cannot read the count.
func (s Organizations) RepositoryCount(ctx context.Context, name string) (*int, error) {
	total, err := listTotal(ctx, s.Client, orgRepos(name))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, nil
	}
	return total, nil
}

// orgRepos requests the first repository of an organization, to count them.
func orgRepos(name string) func(context.Context, *gitea.Client) (*gitea.Response, error) {
	return func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		_, resp, err := c.Repositories.ListOrgRepos(ctx, name, gitea.ListOrgReposOptions{ListOptions: listOptions(1, 1)})
		return resp, err
	}
}

// listTotal returns the collection size a list request reports, or nil when the server does not count it.
func listTotal(ctx context.Context, client *transport.Client, list func(context.Context, *gitea.Client) (*gitea.Response, error)) (*int, error) {
	_, resp, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (struct{}, *gitea.Response, error) {
		resp, err := list(ctx, c)
		return struct{}{}, resp, err
	})
	if err != nil {
		return nil, err
	}
	if _, total, counted := pageHints(headerOf(resp)); counted {
		return &total, nil
	}
	return nil, nil
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
		return Organization{}, Classify(err, "name", strconv.Quote(deref(opts.Name)), "")
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
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Organizations.DeleteOrg(ctx, name)
	})
	return Classify(err, "organization", strconv.Quote(name), "")
}

// OrganizationMember identifies a member without exposing private profile fields.
type OrganizationMember struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	FullName string `json:"full_name"`
	// Visibility of an organization membership is public or concealed; team members have none.
	Visibility string `json:"visibility,omitempty"`
}

// Membership visibilities.
const (
	MembershipPublic    = "public"
	MembershipConcealed = "concealed"
)

// MemberListOptions selects a page of organization members.
type MemberListOptions struct {
	// Public lists only public memberships, and Visibility reports the visibility of each one.
	// Gitea lists concealed members as public to administrators, so both check each membership on
	// its own, and Public drops the server's total, which counts concealed members too.
	Public, Visibility bool
	Limit, Page        int
}

// publicScanPages bounds the server pages Members reads to find public memberships.
const publicScanPages = 20

// Members lists the members of an organization.
func (s Organizations) Members(ctx context.Context, name string, opts MemberListOptions) (Page[OrganizationMember], error) {
	if opts.Public {
		return s.publicMembers(ctx, name, opts)
	}
	members, err := s.memberPage(ctx, name, opts.Limit, opts.Page, false)
	if err != nil || !opts.Visibility {
		return members, err
	}
	members.Items, _, err = s.withVisibility(ctx, name, members.Items)
	return members, err
}

// memberPage lists one server page of members, or of the memberships Gitea reports as public.
func (s Organizations) memberPage(ctx context.Context, name string, limit, page int, public bool) (Page[OrganizationMember], error) {
	users, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.User, *gitea.Response, error) {
		membership := gitea.ListOrgMembershipOption{ListOptions: list}
		if public {
			return c.Organizations.ListPublicOrgMembership(ctx, name, membership)
		}
		return c.Organizations.ListOrgMembership(ctx, name, membership)
	})
	return convertPage(users, memberFrom), err
}

// withVisibility checks the membership of each member and reports which ones are public.
func (s Organizations) withVisibility(ctx context.Context, name string, members []OrganizationMember) ([]OrganizationMember, []bool, error) {
	public := make([]bool, len(members))
	err := concurrently(len(members), func(i int) (err error) {
		public[i], err = s.IsPublicMember(ctx, name, members[i].Username)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	members = slices.Clone(members)
	for i := range members {
		members[i].Visibility = MembershipConcealed
		if public[i] {
			members[i].Visibility = MembershipPublic
		}
	}
	return members, public, nil
}

// publicMembers lists the public memberships. Gitea lists concealed members as public to
// administrators, so it reads server pages until the requested page of confirmed ones is full.
func (s Organizations) publicMembers(ctx context.Context, name string, opts MemberListOptions) (Page[OrganizationMember], error) {
	const size = 50
	skip := (opts.Page - 1) * opts.Limit
	var matches []OrganizationMember
	more := false
	for page := 1; page <= publicScanPages && len(matches) <= skip+opts.Limit; page++ {
		batch, err := s.memberPage(ctx, name, size, page, true)
		if err != nil {
			return Page[OrganizationMember]{}, err
		}
		checked, public, err := s.withVisibility(ctx, name, batch.Items)
		if err != nil {
			return Page[OrganizationMember]{}, err
		}
		for i, member := range checked {
			if public[i] {
				matches = append(matches, member)
			}
		}
		more = batch.More
		if !more {
			break
		}
	}
	// Running out of the scan bound leaves later pages unread, which counts as more results.
	more = more || len(matches) > skip+opts.Limit
	matches = matches[min(len(matches), skip):]
	return Page[OrganizationMember]{Items: matches[:min(len(matches), opts.Limit)], More: more}, nil
}

// concurrentRequests bounds the requests that concurrently keeps in flight.
const concurrentRequests = 8

// concurrently runs request for each index 0..n-1 with a few requests in flight, and returns their
// errors joined.
func concurrently(n int, request func(i int) error) error {
	errs := make([]error, n)
	slots := make(chan struct{}, concurrentRequests)
	var wg sync.WaitGroup
	for i := range n {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			errs[i] = request(i)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// IsMember reports whether username belongs to the organization.
func (s Organizations) IsMember(ctx context.Context, name, username string) (bool, error) {
	return s.check(ctx, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		_, resp, err := c.Organizations.CheckOrgMembership(ctx, name, username)
		return resp, err
	})
}

// IsPublicMember reports whether username's membership of the organization is public.
func (s Organizations) IsPublicMember(ctx context.Context, name, username string) (bool, error) {
	return s.check(ctx, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		_, resp, err := c.Organizations.CheckPublicOrgMembership(ctx, name, username)
		return resp, err
	})
}

// check runs a membership check that answers no with a 404.
func (s Organizations) check(ctx context.Context, op func(context.Context, *gitea.Client) (*gitea.Response, error)) (bool, error) {
	err := do(ctx, s.Client, op)
	// Gitea redirects callers who are not members, and gt does not follow the redirect.
	if failure, ok := errors.AsType[*transport.APIError](err); ok && failure.Status == http.StatusSeeOther {
		return false, &transport.APIError{Status: http.StatusForbidden, Method: failure.Method, Endpoint: failure.Endpoint, Message: "only organization members can check memberships"}
	}
	if IsNotFound(err) {
		// A mistyped user is not a missing membership.
		if missing, ok := errors.AsType[*NotFoundError](Classify(err, "", "", "")); ok && missing.Kind == "user" {
			return false, missing
		}
		return false, nil
	}
	return err == nil, err
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
