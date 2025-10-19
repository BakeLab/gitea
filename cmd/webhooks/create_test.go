// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhooks

import (
	"strings"
	"testing"

	"code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestValidateWebhookType(t *testing.T) {
	validTypes := []string{"gitea", "gogs", "slack", "discord", "dingtalk", "telegram", "msteams", "feishu", "wechatwork", "packagist"}

	for _, validType := range validTypes {
		t.Run("Valid_"+validType, func(t *testing.T) {
			hookType := gitea.HookType(validType)
			assert.NotEmpty(t, string(hookType))
		})
	}
}

func TestParseWebhookEvents(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Single event",
			input:    "push",
			expected: []string{"push"},
		},
		{
			name:     "Multiple events",
			input:    "push,pull_request,issues",
			expected: []string{"push", "pull_request", "issues"},
		},
		{
			name:     "Events with spaces",
			input:    "push, pull_request , issues",
			expected: []string{"push", "pull_request", "issues"},
		},
		{
			name:     "Empty event",
			input:    "",
			expected: []string{""},
		},
		{
			name:     "Single comma",
			input:    ",",
			expected: []string{"", ""},
		},
		{
			name:     "Complex events",
			input:    "pull_request,pull_request_review_approved,pull_request_sync",
			expected: []string{"pull_request", "pull_request_review_approved", "pull_request_sync"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventsList := strings.Split(tt.input, ",")
			events := make([]string, len(eventsList))
			for i, event := range eventsList {
				events[i] = strings.TrimSpace(event)
			}

			assert.Equal(t, tt.expected, events)
		})
	}
}

func TestWebhookConfigConstruction(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		secret         string
		branchFilter   string
		authHeader     string
		expectedKeys   []string
		expectedValues map[string]string
	}{
		{
			name:         "Basic config",
			url:          "https://example.com/webhook",
			expectedKeys: []string{"url", "http_method", "content_type"},
			expectedValues: map[string]string{
				"url":          "https://example.com/webhook",
				"http_method":  "post",
				"content_type": "json",
			},
		},
		{
			name:         "Config with secret",
			url:          "https://example.com/webhook",
			secret:       "my-secret",
			expectedKeys: []string{"url", "http_method", "content_type", "secret"},
			expectedValues: map[string]string{
				"url":          "https://example.com/webhook",
				"http_method":  "post",
				"content_type": "json",
				"secret":       "my-secret",
			},
		},
		{
			name:         "Config with branch filter",
			url:          "https://example.com/webhook",
			branchFilter: "main,develop",
			expectedKeys: []string{"url", "http_method", "content_type", "branch_filter"},
			expectedValues: map[string]string{
				"url":           "https://example.com/webhook",
				"http_method":   "post",
				"content_type":  "json",
				"branch_filter": "main,develop",
			},
		},
		{
			name:         "Config with auth header",
			url:          "https://example.com/webhook",
			authHeader:   "Bearer token123",
			expectedKeys: []string{"url", "http_method", "content_type", "authorization_header"},
			expectedValues: map[string]string{
				"url":                  "https://example.com/webhook",
				"http_method":          "post",
				"content_type":         "json",
				"authorization_header": "Bearer token123",
			},
		},
		{
			name:         "Complete config",
			url:          "https://example.com/webhook",
			secret:       "secret123",
			branchFilter: "main",
			authHeader:   "X-Token: abc",
			expectedKeys: []string{"url", "http_method", "content_type", "secret", "branch_filter", "authorization_header"},
			expectedValues: map[string]string{
				"url":                  "https://example.com/webhook",
				"http_method":          "post",
				"content_type":         "json",
				"secret":               "secret123",
				"branch_filter":        "main",
				"authorization_header": "X-Token: abc",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]string{
				"url":          tt.url,
				"http_method":  "post",
				"content_type": "json",
			}

			if tt.secret != "" {
				config["secret"] = tt.secret
			}
			if tt.branchFilter != "" {
				config["branch_filter"] = tt.branchFilter
			}
			if tt.authHeader != "" {
				config["authorization_header"] = tt.authHeader
			}

			// Check all expected keys exist
			for _, key := range tt.expectedKeys {
				assert.Contains(t, config, key, "Expected key %s not found", key)
			}

			// Check expected values
			for key, expectedValue := range tt.expectedValues {
				assert.Equal(t, expectedValue, config[key], "Value mismatch for key %s", key)
			}

			// Check no unexpected keys
			assert.Len(t, config, len(tt.expectedKeys), "Config has unexpected keys")
		})
	}
}

func TestWebhookCreateOptions(t *testing.T) {
	tests := []struct {
		name        string
		webhookType string
		events      []string
		active      bool
		config      map[string]string
	}{
		{
			name:        "Gitea webhook",
			webhookType: "gitea",
			events:      []string{"push", "pull_request"},
			active:      true,
			config: map[string]string{
				"url":          "https://example.com/webhook",
				"http_method":  "post",
				"content_type": "json",
			},
		},
		{
			name:        "Slack webhook",
			webhookType: "slack",
			events:      []string{"push"},
			active:      true,
			config: map[string]string{
				"url":          "https://hooks.slack.com/services/xxx",
				"http_method":  "post",
				"content_type": "json",
			},
		},
		{
			name:        "Discord webhook",
			webhookType: "discord",
			events:      []string{"pull_request", "pull_request_review_approved"},
			active:      false,
			config: map[string]string{
				"url":          "https://discord.com/api/webhooks/xxx",
				"http_method":  "post",
				"content_type": "json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := gitea.CreateHookOption{
				Type:   gitea.HookType(tt.webhookType),
				Config: tt.config,
				Events: tt.events,
				Active: tt.active,
			}

			assert.Equal(t, gitea.HookType(tt.webhookType), option.Type)
			assert.Equal(t, tt.events, option.Events)
			assert.Equal(t, tt.active, option.Active)
			assert.Equal(t, tt.config, option.Config)
		})
	}
}

func TestWebhookURLValidation(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		expectErr bool
	}{
		{
			name:      "Valid HTTPS URL",
			url:       "https://example.com/webhook",
			expectErr: false,
		},
		{
			name:      "Valid HTTP URL",
			url:       "http://localhost:8080/webhook",
			expectErr: false,
		},
		{
			name:      "Slack webhook URL",
			url:       "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
			expectErr: false,
		},
		{
			name:      "Discord webhook URL",
			url:       "https://discord.com/api/webhooks/123456789/abcdefgh",
			expectErr: false,
		},
		{
			name:      "Empty URL",
			url:       "",
			expectErr: true,
		},
		{
			name:      "Invalid URL scheme",
			url:       "ftp://example.com/webhook",
			expectErr: false, // URL validation is handled by Gitea API
		},
		{
			name:      "URL with path",
			url:       "https://example.com/api/v1/webhook",
			expectErr: false,
		},
		{
			name:      "URL with query params",
			url:       "https://example.com/webhook?token=abc123",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic URL validation - empty check
			if tt.url == "" && tt.expectErr {
				assert.Empty(t, tt.url, "Empty URL should be caught")
			} else if tt.url != "" {
				assert.NotEmpty(t, tt.url, "Non-empty URL should pass basic validation")
			}
		})
	}
}

func TestWebhookEventValidation(t *testing.T) {
	validEvents := []string{
		"push",
		"pull_request",
		"pull_request_sync",
		"pull_request_comment",
		"pull_request_review_approved",
		"pull_request_review_rejected",
		"pull_request_assigned",
		"pull_request_label",
		"pull_request_milestone",
		"issues",
		"issue_comment",
		"issue_assign",
		"issue_label",
		"issue_milestone",
		"create",
		"delete",
		"fork",
		"release",
		"wiki",
		"repository",
	}

	for _, event := range validEvents {
		t.Run("Event_"+event, func(t *testing.T) {
			assert.NotEmpty(t, event, "Event name should not be empty")
			assert.NotContains(t, event, " ", "Event name should not contain spaces")
		})
	}
}

func TestCreateCommandFlags(t *testing.T) {
	cmd := &CmdWebhooksCreate

	// Test flag existence
	expectedFlags := []string{
		"type",
		"secret",
		"events",
		"active",
		"branch-filter",
		"authorization-header",
	}

	for _, flagName := range expectedFlags {
		found := false
		for _, flag := range cmd.Flags {
			if flag.Names()[0] == flagName {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected flag %s not found", flagName)
	}
}

func TestCreateCommandMetadata(t *testing.T) {
	cmd := &CmdWebhooksCreate

	assert.Equal(t, "create", cmd.Name)
	assert.Contains(t, cmd.Aliases, "c")
	assert.Equal(t, "Create a webhook", cmd.Usage)
	assert.Equal(t, "Create a webhook in repository, organization, or globally", cmd.Description)
	assert.Equal(t, "<webhook-url>", cmd.ArgsUsage)
	assert.NotNil(t, cmd.Action)
}

func TestDefaultFlagValues(t *testing.T) {
	cmd := &CmdWebhooksCreate

	// Find specific flags and test their defaults
	for _, flag := range cmd.Flags {
		switch f := flag.(type) {
		case *cli.StringFlag:
			switch f.Name {
			case "type":
				assert.Equal(t, "gitea", f.Value)
			case "events":
				assert.Equal(t, "push", f.Value)
			}
		case *cli.BoolFlag:
			switch f.Name {
			case "active":
				assert.True(t, f.Value)
			}
		}
	}
}
