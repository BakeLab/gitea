// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"maps"
	"net/url"
	"slices"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Webhook is webhook metadata. Its delivery URL omits credentials unless revealed; signing secrets
// and authorization headers are never returned.
type Webhook struct {
	ID           int64    `json:"id"`
	Type         string   `json:"type"`
	URL          string   `json:"url"`
	ContentType  string   `json:"content_type"`
	Active       bool     `json:"active"`
	Events       []string `json:"events"`
	BranchFilter string   `json:"branch_filter"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// WebhookEvents are the events that webhooks accept. issues and pull_request select every event of
// their group; issues_only and pull_request_only select the events of the issue or pull request itself.
var WebhookEvents = []string{
	"create", "delete", "fork", "push",
	"issues", "issues_only", "issue_assign", "issue_label", "issue_milestone", "issue_comment",
	"pull_request", "pull_request_only", "pull_request_assign", "pull_request_label", "pull_request_milestone",
	"pull_request_comment", "pull_request_review", "pull_request_sync", "pull_request_review_request",
	"wiki", "repository", "release", "package", "status", "workflow_run", "workflow_job",
}

// WebhookTypes are the adapters that deliver webhooks.
var WebhookTypes = []string{"gitea", "gogs", "slack", "discord", "dingtalk", "telegram", "msteams", "feishu", "matrix", "wechatwork", "packagist"}

// tokenPaths are the adapters whose delivery URL paths carry access tokens.
var tokenPaths = []string{"slack", "discord", "telegram", "msteams", "feishu"}

// redactURL drops the credentials a delivery URL may carry: user information, the query, and the
// path of adapters with a token in it. "…" marks what was dropped.
func redactURL(raw, adapter string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	path := parsed.EscapedPath()
	if slices.Contains(tokenPaths, adapter) && path != "" && path != "/" {
		path = "/…"
	}
	redacted := parsed.Scheme + "://" + parsed.Host + path
	if parsed.RawQuery != "" {
		redacted += "?…"
	}
	return redacted
}

func webhookFrom(hook *gitea.Hook, reveal bool) Webhook {
	events := slices.Sorted(slices.Values(hook.Events))
	if events == nil {
		events = []string{}
	}
	address := hook.Config["url"]
	if !reveal {
		address = redactURL(address, hook.Type)
	}
	return Webhook{
		ID: hook.ID, Type: hook.Type, URL: address, ContentType: hook.Config["content_type"], Active: hook.Active,
		Events: events, BranchFilter: hook.BranchFilter, CreatedAt: timestamp(hook.Created), UpdatedAt: timestamp(hook.Updated),
	}
}

// WebhookEventGroups are the events that sending issues or pull_request enables besides that event.
var WebhookEventGroups = map[string][]string{
	"issues":       {"issue_assign", "issue_comment", "issue_label", "issue_milestone"},
	"pull_request": {"pull_request_assign", "pull_request_comment", "pull_request_label", "pull_request_milestone", "pull_request_review", "pull_request_review_request", "pull_request_sync"},
}

// narrowedEvents turns the events Gitea reports into events that select the same set when sent
// back. Gitea reports a hook of the issue or pull request events alone as issues or pull_request,
// but taking those names back enables their whole group, so they become issues_only and
// pull_request_only unless every member of the group is there too.
func narrowedEvents(events []string) []string {
	narrowed := slices.Clone(events)
	for head, members := range WebhookEventGroups {
		index := slices.Index(narrowed, head)
		if index >= 0 && slices.ContainsFunc(members, func(member string) bool { return !slices.Contains(events, member) }) {
			narrowed[index] = head + "_only"
		}
	}
	return narrowed
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
	// RevealURL returns delivery URLs whole, with the credentials they may carry.
	RevealURL bool
}

func (s Webhooks) from(hook *gitea.Hook) Webhook {
	return webhookFrom(hook, s.RevealURL)
}

// List returns a page of hook metadata.
func (s Webhooks) List(ctx context.Context, limit, page int) (Page[Webhook], error) {
	hooks, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Hook, *gitea.Response, error) {
		opts := gitea.ListHooksOptions{ListOptions: list}
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
	return convertPage(hooks, s.from), err
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
	return hook, Classify(err, "webhook", strconv.FormatInt(id, 10), s.errorScope())
}

// errorScope names the repository or organization the selected scope looks in, for error messages.
func (s Webhooks) errorScope() string {
	switch {
	case s.Scope.Global, s.Scope.Account:
		return ""
	case s.Scope.Organization != "":
		return s.Scope.Organization
	}
	return s.Scope.Repository
}

// View retrieves hook metadata without exposing its delivery configuration.
func (s Webhooks) View(ctx context.Context, id int64) (Webhook, error) {
	hook, err := s.get(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	return s.from(hook), nil
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
	return s.from(hook), nil
}

// Edit preserves settings that Gitea replaces rather than patching individually.
func (s Webhooks) Edit(ctx context.Context, id int64, opts WebhookOptions) (Webhook, error) {
	if _, ok := opts.Config["secret"]; ok {
		return Webhook{}, &CapabilityError{Feature: "webhook secret editing", Reason: "Gitea's edit API does not update the signing secret; recreate the hook to rotate it"}
	}
	if opts.Authorization == nil {
		return Webhook{}, &CapabilityError{Feature: "webhook editing without an authorization choice", Reason: "Gitea drops the stored authorization header on every edit and never returns it, so the caller must send it again or clear it"}
	}
	current, err := s.get(ctx, id)
	if err != nil {
		return Webhook{}, err
	}
	edit := gitea.EditHookOption{Events: narrowedEvents(current.Events), BranchFilter: current.BranchFilter, Active: opts.Active, AuthorizationHeader: *opts.Authorization}
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
	return s.from(hook), nil
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
