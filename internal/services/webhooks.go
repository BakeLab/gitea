// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"maps"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Webhook is safe webhook metadata. Delivery URLs and credentials are omitted.
type Webhook struct {
	ID           int64    `json:"id"`
	Type         string   `json:"type"`
	Active       bool     `json:"active"`
	Events       []string `json:"events"`
	BranchFilter string   `json:"branch_filter"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

func webhookFrom(hook *gitea.Hook) Webhook {
	events := hook.Events
	if events == nil {
		events = []string{}
	}
	return Webhook{
		ID: hook.ID, Type: hook.Type, Active: hook.Active, Events: events, BranchFilter: hook.BranchFilter,
		CreatedAt: timestamp(hook.Created), UpdatedAt: timestamp(hook.Updated),
	}
}

// WebhookScope selects one repository, organization, account or system scope.
type WebhookScope struct {
	Repository, Organization string
	Account, Global          bool
}

// WebhookOptions distinguishes omitted input from explicit empty or false values.
type WebhookOptions struct {
	Type          string            `json:"type,omitempty"`
	Config        map[string]string `json:"config,omitempty"`
	Events        *[]string         `json:"events,omitempty"`
	Active        *bool             `json:"active,omitempty"`
	BranchFilter  *string           `json:"branch_filter,omitempty"`
	Authorization *string           `json:"authorization_header,omitempty"`
}

// Webhooks manages hooks without returning delivery credentials.
type Webhooks struct {
	Client *transport.Client
	Scope  WebhookScope
}

// List returns a page of hook metadata.
func (s Webhooks) List(ctx context.Context, limit, page int) ([]Webhook, error) {
	hooks, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Hook, *gitea.Response, error) {
		opts := gitea.ListHooksOptions{ListOptions: listOptions(limit, page)}
		switch {
		case s.Scope.Global:
			return c.Hooks.ListGlobalHooks(ctx, gitea.ListGlobalHooksOptions{ListOptions: opts.ListOptions})
		case s.Scope.Account:
			return c.Hooks.ListMyHooks(ctx, opts)
		case s.Scope.Organization != "":
			return c.Hooks.ListOrgHooks(ctx, s.Scope.Organization, opts)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Hooks.ListRepoHooks(ctx, owner, name, opts)
	})
	if err != nil {
		return nil, err
	}
	return convertAll(hooks, webhookFrom), nil
}

func (s Webhooks) get(ctx context.Context, id int64) (*gitea.Hook, error) {
	hook, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Hook, *gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Hooks.GetGlobalHook(ctx, id)
		case s.Scope.Account:
			return c.Hooks.GetMyHook(ctx, id)
		case s.Scope.Organization != "":
			return c.Hooks.GetOrgHook(ctx, s.Scope.Organization, id)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Hooks.GetRepoHook(ctx, owner, name, id)
	})
	return hook, err
}

// View retrieves hook metadata without exposing its delivery configuration.
func (s Webhooks) View(ctx context.Context, id int64) (Webhook, error) {
	hook, err := s.get(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	return webhookFrom(hook), nil
}

// Create registers a new webhook.
func (s Webhooks) Create(ctx context.Context, opts WebhookOptions) (Webhook, error) {
	create := gitea.CreateHookOption{
		Type: gitea.HookType(opts.Type), Config: opts.Config, Events: deref(opts.Events), BranchFilter: deref(opts.BranchFilter),
		Active: deref(opts.Active), AuthorizationHeader: deref(opts.Authorization),
	}
	if s.Scope.Global {
		create.Config = make(map[string]string, len(opts.Config)+1)
		maps.Copy(create.Config, opts.Config)
		create.Config["is_system_webhook"] = "true"
	}
	hook, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Hook, *gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Hooks.CreateGlobalHook(ctx, create)
		case s.Scope.Account:
			return c.Hooks.CreateMyHook(ctx, create)
		case s.Scope.Organization != "":
			return c.Hooks.CreateOrgHook(ctx, s.Scope.Organization, create)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, nil, err
		}
		return c.Hooks.CreateRepoHook(ctx, owner, name, create)
	})
	if err != nil {
		return Webhook{}, err
	}
	return webhookFrom(hook), nil
}

// Edit preserves settings that Gitea replaces rather than patching individually.
func (s Webhooks) Edit(ctx context.Context, id int64, opts WebhookOptions) (Webhook, error) {
	if _, ok := opts.Config["secret"]; ok {
		return Webhook{}, &CapabilityError{Feature: "webhook secret editing", Reason: "Gitea's edit API does not update the signing secret; recreate the hook to rotate it"}
	}
	if opts.Authorization == nil {
		return Webhook{}, &CapabilityError{Feature: "webhook editing without an authorization choice", Reason: "Gitea replaces authorization and can hide its existing value; supply --authorization-header-file or --clear-authorization"}
	}
	current, err := s.get(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	edit := gitea.EditHookOption{Events: current.Events, BranchFilter: current.BranchFilter, Active: opts.Active, AuthorizationHeader: *opts.Authorization}
	if opts.Events != nil {
		edit.Events = *opts.Events
	}
	if opts.BranchFilter != nil {
		edit.BranchFilter = *opts.BranchFilter
	}
	// Adapter-specific configuration can be replaced as a group by the server.
	if opts.Config != nil {
		edit.Config = make(map[string]string, len(current.Config)+len(opts.Config))
		maps.Copy(edit.Config, current.Config)
		maps.Copy(edit.Config, opts.Config)
	}
	hook, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Hook, *gitea.Response, error) {
		var resp *gitea.Response
		var err error
		switch {
		case s.Scope.Global:
			return c.Hooks.EditGlobalHook(ctx, id, edit)
		case s.Scope.Account:
			resp, err = c.Hooks.EditMyHook(ctx, id, edit)
		case s.Scope.Organization != "":
			resp, err = c.Hooks.EditOrgHook(ctx, s.Scope.Organization, id, edit)
		default:
			owner, name, splitErr := splitRepository(s.Scope.Repository)
			if splitErr != nil {
				return nil, nil, splitErr
			}
			resp, err = c.Hooks.EditRepoHook(ctx, owner, name, id, edit)
		}
		return nil, resp, err
	})
	if err == nil && hook == nil {
		hook, err = s.get(ctx, id)
	}
	if err != nil {
		return Webhook{}, err
	}
	return webhookFrom(hook), nil
}

// Delete removes a webhook from the selected scope.
func (s Webhooks) Delete(ctx context.Context, id int64) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		switch {
		case s.Scope.Global:
			return c.Hooks.DeleteGlobalHook(ctx, id)
		case s.Scope.Account:
			return c.Hooks.DeleteMyHook(ctx, id)
		case s.Scope.Organization != "":
			return c.Hooks.DeleteOrgHook(ctx, s.Scope.Organization, id)
		}
		owner, name, err := splitRepository(s.Scope.Repository)
		if err != nil {
			return nil, err
		}
		return c.Hooks.DeleteRepoHook(ctx, owner, name, id)
	})
}
