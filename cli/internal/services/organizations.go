// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
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
	if username != "" && !mine {
		err = Classify(err, "user", strconv.Quote(username), "")
	}
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
		total, counted, _ := listTotal(ctx, s.Client, counts[i])
		if counted {
			totals[i] = &total
		}
		return nil
	})
	if ctx.Err() != nil {
		return OrganizationSize{}, ctx.Err()
	}
	return OrganizationSize{Members: totals[0], Teams: totals[1], Repositories: totals[2]}, nil
}

// RepositoryCount counts the repositories of an organization and reports whether the count is available.
func (s Organizations) RepositoryCount(ctx context.Context, name string) (int, bool, error) {
	total, counted, err := listTotal(ctx, s.Client, orgRepos(name))
	if ctx.Err() != nil {
		return 0, false, ctx.Err()
	}
	if err != nil {
		return 0, false, nil
	}
	return total, counted, nil
}

// orgRepos requests the first repository of an organization, to count them.
func orgRepos(name string) func(context.Context, *gitea.Client) (*gitea.Response, error) {
	return func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		_, resp, err := c.Repositories.ListOrgRepos(ctx, name, gitea.ListOrgReposOptions{ListOptions: listOptions(1, 1)})
		return resp, err
	}
}

// listTotal returns the collection size a list request reports and whether the server counted it.
func listTotal(ctx context.Context, client *transport.Client, list func(context.Context, *gitea.Client) (*gitea.Response, error)) (int, bool, error) {
	_, resp, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (struct{}, *gitea.Response, error) {
		resp, err := list(ctx, c)
		return struct{}{}, resp, err
	})
	if err != nil {
		return 0, false, err
	}
	if _, total, counted := pageHints(headerOf(resp)); counted {
		return total, true, nil
	}
	return 0, false, nil
}

func createOrgOption(opts OrganizationOptions) gitea.CreateOrgOption {
	return gitea.CreateOrgOption{
		Name: deref(opts.Name), FullName: deref(opts.FullName), Description: deref(opts.Description),
		Website: deref(opts.Website), Location: deref(opts.Location), Visibility: gitea.VisibleType(deref(opts.Visibility)),
		RepoAdminChangeTeamAccess: deref(opts.RepoAdminChangeTeamAccess),
	}
}

// Create creates an organization owned by the selected account.
func (s Organizations) Create(ctx context.Context, opts OrganizationOptions) (Organization, error) {
	org, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Organizations.CreateOrg(ctx, createOrgOption(opts))
	})
	if err != nil {
		return Organization{}, Classify(err, "name", strconv.Quote(deref(opts.Name)), "")
	}
	return organizationFrom(org), nil
}

// Edit changes only supplied organization preferences.
func (s Organizations) Edit(ctx context.Context, name string, opts OrganizationOptions) (Organization, error) {
	option := gitea.UpdateOrgOption{
		FullName: opts.FullName, Description: opts.Description, Website: opts.Website, Location: opts.Location,
		RepoAdminChangeTeamAccess: opts.RepoAdminChangeTeamAccess,
	}
	if opts.Visibility != nil {
		visibility := gitea.VisibleType(*opts.Visibility)
		option.Visibility = &visibility
	}
	org, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Organization, *gitea.Response, error) {
		return c.Organizations.UpdateOrg(ctx, name, option)
	})
	if err != nil {
		return Organization{}, Classify(err, "organization", strconv.Quote(name), "")
	}
	return organizationFrom(org), nil
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
	// Gitea lists concealed members as public to administrators, so for them both check each
	// membership on its own, and Public drops the server's total, which counts concealed members
	// too.
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
	// A 404 here can only mean the organization is missing or invisible.
	return convertPage(users, memberFrom), Classify(err, "organization", strconv.Quote(name), "")
}

// CanGrantTeamAccess reports whether the account may grant team of org access to a repository it
// creates. Gitea allows site administrators and owners of org, and members of the team when org
// lets repository administrators change team access.
func (s Organizations) CanGrantTeamAccess(ctx context.Context, org string, team int64) (bool, error) {
	user, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	if err != nil || user.IsAdmin {
		return err == nil, err
	}
	permissions, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.OrgPermissions, *gitea.Response, error) {
		return c.Organizations.GetOrgPermissions(ctx, org, login(user))
	})
	if err != nil || permissions.IsOwner {
		return err == nil, Classify(err, "organization", strconv.Quote(org), "")
	}
	organization, err := s.View(ctx, org)
	if err != nil || !organization.RepoAdminChangeTeamAccess {
		return false, err
	}
	_, _, err = call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Organizations.GetTeamMember(ctx, team, login(user))
	})
	if IsNotFound(err) || IsForbidden(err) { // Gitea refuses non-members with a 403
		return false, nil
	}
	return err == nil, err
}

// publicListExact reports whether Gitea lists exactly the public memberships to the caller; it
// lists every membership as public to an administrator. A caller that cannot be read counts as one.
func (s Organizations) publicListExact(ctx context.Context) bool {
	if s.Client.Token == "" && s.Client.Signer == nil {
		return true
	}
	user, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	return err == nil && !user.IsAdmin
}

// publicIDs returns the IDs of the public members of an organization, and false when Gitea does
// not list them exactly or listing them takes more than budget requests.
func (s Organizations) publicIDs(ctx context.Context, name string, budget int) (map[int64]bool, bool, error) {
	const size = 50
	if budget < 1 || !s.publicListExact(ctx) {
		return nil, false, nil
	}
	ids := map[int64]bool{}
	for page := 1; page <= min(budget, maxListPages); page++ {
		batch, err := s.memberPage(ctx, name, size, page, true)
		if err != nil {
			return nil, false, err
		}
		for _, member := range batch.Items {
			ids[member.ID] = true
		}
		if !batch.More {
			return ids, true, nil
		}
		if batch.Counted && (batch.Total+size-1)/size > budget {
			break
		}
	}
	return nil, false, nil
}

// withVisibility reports which members are public: from the list of public members, or, when
// Gitea does not list them exactly or the list is longer than the members, by checking each
// membership.
func (s Organizations) withVisibility(ctx context.Context, name string, members []OrganizationMember) ([]OrganizationMember, []bool, error) {
	public := make([]bool, len(members))
	publicIDs, listed, err := s.publicIDs(ctx, name, len(members))
	if err != nil {
		return nil, nil, err
	}
	if listed {
		for i, member := range members {
			public[i] = publicIDs[member.ID]
		}
	} else if err := concurrently(len(members), func(i int) (err error) {
		public[i], err = s.IsPublicMember(ctx, name, members[i].Username)
		return err
	}); err != nil {
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
// administrators, so for them it reads server pages until the requested page of confirmed ones is
// full.
func (s Organizations) publicMembers(ctx context.Context, name string, opts MemberListOptions) (Page[OrganizationMember], error) {
	const size = 50
	if s.publicListExact(ctx) {
		members, err := s.memberPage(ctx, name, opts.Limit, opts.Page, true)
		for i := range members.Items {
			members.Items[i].Visibility = MembershipPublic
		}
		return members, err
	}
	skip := (opts.Page - 1) * opts.Limit
	matches := []OrganizationMember{}
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

// IsMember reports whether username belongs to the organization. It fails with a *NotFoundError
// when the organization or the user does not exist.
func (s Organizations) IsMember(ctx context.Context, name, username string) (bool, error) {
	member, err := s.check(ctx, name, "", func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		_, resp, err := c.Organizations.CheckOrgMembership(ctx, name, username)
		return resp, err
	})
	if err == nil && !member {
		// Gitea answers a missing organization with the same 404 as a missing membership.
		if _, err := s.View(ctx, name); err != nil {
			return false, err
		}
		return false, userExists(ctx, s.Client, username)
	}
	return member, err
}

// IsPublicMember reports whether username's membership of the organization is public.
func (s Organizations) IsPublicMember(ctx context.Context, name, username string) (bool, error) {
	return s.check(ctx, name, "", func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		_, resp, err := c.Organizations.CheckPublicOrgMembership(ctx, name, username)
		return resp, err
	})
}

// check runs a membership check of org that answers no with a 404. Given a username, it fails
// with a *NotFoundError when that user does not exist.
func (s Organizations) check(ctx context.Context, org, username string, op func(context.Context, *gitea.Client) (*gitea.Response, error)) (bool, error) {
	// Gitea redirects callers who are not members to the public membership check.
	err := do(transport.WithoutRedirects(ctx), s.Client, op)
	if failure, ok := errors.AsType[*transport.APIError](err); ok && failure.Status == http.StatusSeeOther {
		forbidden := &transport.APIError{Status: http.StatusForbidden, Method: failure.Method, Endpoint: failure.Endpoint, Message: failure.Message}
		org = cmp.Or(org, "the organization")
		return false, &RefusedError{Text: "checking memberships of " + org + " requires membership of " + org, Err: forbidden}
	}
	if IsNotFound(err) {
		// A mistyped user is not a missing membership.
		if missing, ok := errors.AsType[*NotFoundError](Classify(err, "", "", "")); ok && missing.Kind == "user" {
			return false, missing
		}
		// Gitea 28 answers a missing user with the same bare 404 as a missing membership.
		if username == "" {
			return false, nil
		}
		return false, userExists(ctx, s.Client, username)
	}
	return err == nil, err
}

// userExists fails with a *NotFoundError when no user is called username. A user the account
// may not see counts as existing.
func userExists(ctx context.Context, client *transport.Client, username string) error {
	_, _, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetUserInfo(ctx, username)
	})
	if IsNotFound(err) {
		return &NotFoundError{Kind: "user", Key: strconv.Quote(username), Err: err}
	}
	if IsForbidden(err) {
		return nil
	}
	return err
}

// MissingSudoUser returns a *NotFoundError when the Sudo user of client does not exist, which
// Gitea answers like any missing resource, with a bare 404.
func MissingSudoUser(ctx context.Context, client *transport.Client) error {
	if client == nil || client.Sudo == "" {
		return nil
	}
	plain := *client
	plain.Sudo = ""
	if err := userExists(ctx, &plain, client.Sudo); IsNotFound(err) {
		if missing, ok := errors.AsType[*NotFoundError](err); ok {
			missing.Kind = "--sudo user"
		}
		return err
	}
	return nil
}

// OwnerExists fails with a *NotFoundError when no user or organization is called name.
func OwnerExists(ctx context.Context, client *transport.Client, name string) error {
	err := userExists(ctx, client, name)
	if missing, ok := errors.AsType[*NotFoundError](err); ok {
		missing.Kind = "user or organization"
	}
	return err
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
