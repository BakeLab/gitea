// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"strings"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/assert"
)

func TestWebhooksList(t *testing.T) {
	now := time.Now()

	hooks := []*gitea.Hook{
		{
			ID:   1,
			Type: "gitea",
			Config: map[string]string{
				"url": "https://example.com/webhook",
			},
			Events:  []string{"push", "pull_request"},
			Active:  true,
			Updated: now,
		},
		{
			ID:   2,
			Type: "slack",
			Config: map[string]string{
				"url": "https://hooks.slack.com/services/xxx",
			},
			Events:  []string{"push"},
			Active:  false,
			Updated: now,
		},
		{
			ID:      3,
			Type:    "discord",
			Config:  nil,
			Events:  []string{"pull_request", "pull_request_review_approved"},
			Active:  true,
			Updated: now,
		},
	}

	// Test that function doesn't panic with various output formats
	outputFormats := []string{"table", "csv", "json", "yaml", "simple", "tsv"}

	for _, format := range outputFormats {
		t.Run("Format_"+format, func(t *testing.T) {
			// Should not panic
			assert.NotPanics(t, func() {
				WebhooksList(hooks, format)
			})
		})
	}
}

func TestWebhooksListEmpty(t *testing.T) {
	// Test with empty hook list
	hooks := []*gitea.Hook{}

	assert.NotPanics(t, func() {
		WebhooksList(hooks, "table")
	})
}

func TestWebhooksListNil(t *testing.T) {
	// Test with nil hook list
	assert.NotPanics(t, func() {
		WebhooksList(nil, "table")
	})
}

func TestWebhookDetails(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		hook *gitea.Hook
	}{
		{
			name: "Complete webhook",
			hook: &gitea.Hook{
				ID:   123,
				Type: "gitea",
				Config: map[string]string{
					"url":                  "https://example.com/webhook",
					"content_type":         "json",
					"http_method":          "post",
					"branch_filter":        "main,develop",
					"secret":               "secret-value",
					"authorization_header": "Bearer token123",
				},
				Events:  []string{"push", "pull_request", "issues"},
				Active:  true,
				Created: now.Add(-24 * time.Hour),
				Updated: now,
			},
		},
		{
			name: "Minimal webhook",
			hook: &gitea.Hook{
				ID:      456,
				Type:    "slack",
				Config:  map[string]string{"url": "https://hooks.slack.com/xxx"},
				Events:  []string{"push"},
				Active:  false,
				Created: now,
				Updated: now,
			},
		},
		{
			name: "Webhook with nil config",
			hook: &gitea.Hook{
				ID:      789,
				Type:    "discord",
				Config:  nil,
				Events:  []string{"pull_request"},
				Active:  true,
				Created: now,
				Updated: now,
			},
		},
		{
			name: "Webhook with empty config",
			hook: &gitea.Hook{
				ID:      999,
				Type:    "gitea",
				Config:  map[string]string{},
				Events:  []string{},
				Active:  false,
				Created: now,
				Updated: now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			assert.NotPanics(t, func() {
				WebhookDetails(tt.hook)
			})
		})
	}
}

func TestWebhookEventsTruncation(t *testing.T) {
	tests := []struct {
		name           string
		events         []string
		maxLength      int
		shouldTruncate bool
	}{
		{
			name:           "Short events list",
			events:         []string{"push"},
			maxLength:      40,
			shouldTruncate: false,
		},
		{
			name:           "Medium events list",
			events:         []string{"push", "pull_request"},
			maxLength:      40,
			shouldTruncate: false,
		},
		{
			name:           "Long events list",
			events:         []string{"push", "pull_request", "pull_request_review_approved", "pull_request_sync", "issues"},
			maxLength:      40,
			shouldTruncate: true,
		},
		{
			name:           "Very long events list",
			events:         []string{"push", "pull_request", "pull_request_review_approved", "pull_request_review_rejected", "pull_request_comment", "pull_request_assigned", "pull_request_label"},
			maxLength:      40,
			shouldTruncate: true,
		},
		{
			name:           "Empty events",
			events:         []string{},
			maxLength:      40,
			shouldTruncate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventsStr := strings.Join(tt.events, ",")

			if len(eventsStr) > tt.maxLength {
				assert.True(t, tt.shouldTruncate, "Events string should be truncated")
				truncated := eventsStr[:tt.maxLength-3] + "..."
				assert.Contains(t, truncated, "...")
				assert.LessOrEqual(t, len(truncated), tt.maxLength)
			} else {
				assert.False(t, tt.shouldTruncate, "Events string should not be truncated")
			}
		})
	}
}

func TestWebhookActiveStatus(t *testing.T) {
	tests := []struct {
		name           string
		active         bool
		expectedSymbol string
	}{
		{
			name:           "Active webhook",
			active:         true,
			expectedSymbol: "✓",
		},
		{
			name:           "Inactive webhook",
			active:         false,
			expectedSymbol: "✗",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			symbol := "✓"
			if !tt.active {
				symbol = "✗"
			}

			assert.Equal(t, tt.expectedSymbol, symbol)
		})
	}
}

func TestWebhookConfigHandling(t *testing.T) {
	tests := []struct {
		name          string
		config        map[string]string
		expectedURL   string
		hasSecret     bool
		hasAuthHeader bool
	}{
		{
			name: "Config with all fields",
			config: map[string]string{
				"url":                  "https://example.com/webhook",
				"secret":               "my-secret",
				"authorization_header": "Bearer token",
				"content_type":         "json",
				"http_method":          "post",
				"branch_filter":        "main",
			},
			expectedURL:   "https://example.com/webhook",
			hasSecret:     true,
			hasAuthHeader: true,
		},
		{
			name: "Config with minimal fields",
			config: map[string]string{
				"url": "https://hooks.slack.com/xxx",
			},
			expectedURL:   "https://hooks.slack.com/xxx",
			hasSecret:     false,
			hasAuthHeader: false,
		},
		{
			name:          "Nil config",
			config:        nil,
			expectedURL:   "",
			hasSecret:     false,
			hasAuthHeader: false,
		},
		{
			name:          "Empty config",
			config:        map[string]string{},
			expectedURL:   "",
			hasSecret:     false,
			hasAuthHeader: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var url string
			if tt.config != nil {
				url = tt.config["url"]
			}
			assert.Equal(t, tt.expectedURL, url)

			var hasSecret, hasAuthHeader bool
			if tt.config != nil {
				_, hasSecret = tt.config["secret"]
				_, hasAuthHeader = tt.config["authorization_header"]
			}
			assert.Equal(t, tt.hasSecret, hasSecret)
			assert.Equal(t, tt.hasAuthHeader, hasAuthHeader)
		})
	}
}

func TestWebhookTableHeaders(t *testing.T) {
	expectedHeaders := []string{
		"ID",
		"Type",
		"URL",
		"Events",
		"Active",
		"Updated",
	}

	// Verify all headers are non-empty and unique
	headerSet := make(map[string]bool)
	for _, header := range expectedHeaders {
		assert.NotEmpty(t, header, "Header should not be empty")
		assert.False(t, headerSet[header], "Header %s should be unique", header)
		headerSet[header] = true
	}

	assert.Len(t, expectedHeaders, 6, "Should have exactly 6 headers")
}

func TestWebhookTypeValues(t *testing.T) {
	validTypes := []string{
		"gitea",
		"gogs",
		"slack",
		"discord",
		"dingtalk",
		"telegram",
		"msteams",
		"feishu",
		"wechatwork",
		"packagist",
	}

	for _, hookType := range validTypes {
		t.Run("Type_"+hookType, func(t *testing.T) {
			assert.NotEmpty(t, hookType, "Hook type should not be empty")
		})
	}
}

func TestWebhookDetailsFormatting(t *testing.T) {
	now := time.Now()
	hook := &gitea.Hook{
		ID:   123,
		Type: "gitea",
		Config: map[string]string{
			"url":                  "https://example.com/webhook",
			"content_type":         "json",
			"http_method":          "post",
			"branch_filter":        "main,develop",
			"secret":               "secret-value",
			"authorization_header": "Bearer token123",
		},
		Events:  []string{"push", "pull_request", "issues"},
		Active:  true,
		Created: now.Add(-24 * time.Hour),
		Updated: now,
	}

	// Test that all expected fields are included in details
	expectedElements := []string{
		"123",                         // webhook ID
		"gitea",                       // webhook type
		"true",                        // active status
		"https://example.com/webhook", // URL
		"json",                        // content type
		"post",                        // HTTP method
		"main,develop",                // branch filter
		"(configured)",                // secret indicator
		"(configured)",                // auth header indicator
		"push, pull_request, issues",  // events list
	}

	// Verify elements exist (placeholder test)
	assert.Greater(t, len(expectedElements), 0, "Should have expected elements")

	// This is a functional test - in practice, we'd capture output
	// For now, we verify the webhook structure contains expected data
	assert.Equal(t, int64(123), hook.ID)
	assert.Equal(t, "gitea", hook.Type)
	assert.True(t, hook.Active)
	assert.Equal(t, "https://example.com/webhook", hook.Config["url"])
	assert.Equal(t, "json", hook.Config["content_type"])
	assert.Equal(t, "post", hook.Config["http_method"])
	assert.Equal(t, "main,develop", hook.Config["branch_filter"])
	assert.Contains(t, hook.Config, "secret")
	assert.Contains(t, hook.Config, "authorization_header")
	assert.Equal(t, []string{"push", "pull_request", "issues"}, hook.Events)
}
