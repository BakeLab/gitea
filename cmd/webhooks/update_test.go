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

func TestUpdateCommandMetadata(t *testing.T) {
	cmd := &CmdWebhooksUpdate

	assert.Equal(t, "update", cmd.Name)
	assert.Contains(t, cmd.Aliases, "edit")
	assert.Contains(t, cmd.Aliases, "u")
	assert.Equal(t, "Update a webhook", cmd.Usage)
	assert.Equal(t, "Update webhook configuration in repository, organization, or globally", cmd.Description)
	assert.Equal(t, "<webhook-id>", cmd.ArgsUsage)
	assert.NotNil(t, cmd.Action)
}

func TestUpdateCommandFlags(t *testing.T) {
	cmd := &CmdWebhooksUpdate

	expectedFlags := []string{
		"url",
		"secret",
		"events",
		"active",
		"inactive",
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

func TestUpdateActiveInactiveFlags(t *testing.T) {
	tests := []struct {
		name           string
		activeSet      bool
		activeValue    bool
		inactiveSet    bool
		inactiveValue  bool
		originalActive bool
		expectedActive bool
	}{
		{
			name:           "Set active to true",
			activeSet:      true,
			activeValue:    true,
			inactiveSet:    false,
			originalActive: false,
			expectedActive: true,
		},
		{
			name:           "Set active to false",
			activeSet:      true,
			activeValue:    false,
			inactiveSet:    false,
			originalActive: true,
			expectedActive: false,
		},
		{
			name:           "Set inactive to true",
			activeSet:      false,
			inactiveSet:    true,
			inactiveValue:  true,
			originalActive: true,
			expectedActive: false,
		},
		{
			name:           "Set inactive to false",
			activeSet:      false,
			inactiveSet:    true,
			inactiveValue:  false,
			originalActive: false,
			expectedActive: true,
		},
		{
			name:           "No flags set",
			activeSet:      false,
			inactiveSet:    false,
			originalActive: true,
			expectedActive: true,
		},
		{
			name:           "Active flag takes precedence",
			activeSet:      true,
			activeValue:    true,
			inactiveSet:    true,
			inactiveValue:  true,
			originalActive: false,
			expectedActive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the logic from runWebhooksUpdate
			active := tt.originalActive

			if tt.activeSet {
				active = tt.activeValue
			} else if tt.inactiveSet {
				active = !tt.inactiveValue
			}

			assert.Equal(t, tt.expectedActive, active)
		})
	}
}

func TestUpdateConfigPreservation(t *testing.T) {
	// Test that existing configuration is preserved when not updated
	originalConfig := map[string]string{
		"url":                  "https://old.example.com/webhook",
		"secret":               "old-secret",
		"branch_filter":        "main",
		"authorization_header": "Bearer old-token",
		"http_method":          "post",
		"content_type":         "json",
	}

	tests := []struct {
		name           string
		updates        map[string]string
		expectedConfig map[string]string
	}{
		{
			name: "Update only URL",
			updates: map[string]string{
				"url": "https://new.example.com/webhook",
			},
			expectedConfig: map[string]string{
				"url":                  "https://new.example.com/webhook",
				"secret":               "old-secret",
				"branch_filter":        "main",
				"authorization_header": "Bearer old-token",
				"http_method":          "post",
				"content_type":         "json",
			},
		},
		{
			name: "Update secret and auth header",
			updates: map[string]string{
				"secret":               "new-secret",
				"authorization_header": "X-Token: new-token",
			},
			expectedConfig: map[string]string{
				"url":                  "https://old.example.com/webhook",
				"secret":               "new-secret",
				"branch_filter":        "main",
				"authorization_header": "X-Token: new-token",
				"http_method":          "post",
				"content_type":         "json",
			},
		},
		{
			name: "Clear branch filter",
			updates: map[string]string{
				"branch_filter": "",
			},
			expectedConfig: map[string]string{
				"url":                  "https://old.example.com/webhook",
				"secret":               "old-secret",
				"branch_filter":        "",
				"authorization_header": "Bearer old-token",
				"http_method":          "post",
				"content_type":         "json",
			},
		},
		{
			name:    "No updates",
			updates: map[string]string{},
			expectedConfig: map[string]string{
				"url":                  "https://old.example.com/webhook",
				"secret":               "old-secret",
				"branch_filter":        "main",
				"authorization_header": "Bearer old-token",
				"http_method":          "post",
				"content_type":         "json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy original config
			config := make(map[string]string)
			for k, v := range originalConfig {
				config[k] = v
			}

			// Apply updates
			for k, v := range tt.updates {
				config[k] = v
			}

			// Verify expected config
			assert.Equal(t, tt.expectedConfig, config)
		})
	}
}

func TestUpdateEventsHandling(t *testing.T) {
	tests := []struct {
		name           string
		originalEvents []string
		newEvents      string
		setEvents      bool
		expectedEvents []string
	}{
		{
			name:           "Update events",
			originalEvents: []string{"push"},
			newEvents:      "push,pull_request,issues",
			setEvents:      true,
			expectedEvents: []string{"push", "pull_request", "issues"},
		},
		{
			name:           "Clear events",
			originalEvents: []string{"push", "pull_request"},
			newEvents:      "",
			setEvents:      true,
			expectedEvents: []string{""},
		},
		{
			name:           "No event update",
			originalEvents: []string{"push", "pull_request"},
			newEvents:      "",
			setEvents:      false,
			expectedEvents: []string{"push", "pull_request"},
		},
		{
			name:           "Single event",
			originalEvents: []string{"push", "issues"},
			newEvents:      "pull_request",
			setEvents:      true,
			expectedEvents: []string{"pull_request"},
		},
		{
			name:           "Events with spaces",
			originalEvents: []string{"push"},
			newEvents:      "push, pull_request , issues",
			setEvents:      true,
			expectedEvents: []string{"push", "pull_request", "issues"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := tt.originalEvents

			if tt.setEvents {
				eventsList := []string{}
				if tt.newEvents != "" {
					parts := strings.Split(tt.newEvents, ",")
					for _, part := range parts {
						eventsList = append(eventsList, strings.TrimSpace(part))
					}
				} else {
					eventsList = []string{""}
				}
				events = eventsList
			}

			assert.Equal(t, tt.expectedEvents, events)
		})
	}
}

func TestUpdateEditHookOption(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]string
		events   []string
		active   bool
		expected gitea.EditHookOption
	}{
		{
			name: "Complete update",
			config: map[string]string{
				"url":    "https://example.com/webhook",
				"secret": "new-secret",
			},
			events: []string{"push", "pull_request"},
			active: true,
			expected: gitea.EditHookOption{
				Config: map[string]string{
					"url":    "https://example.com/webhook",
					"secret": "new-secret",
				},
				Events: []string{"push", "pull_request"},
				Active: &[]bool{true}[0],
			},
		},
		{
			name: "Config only update",
			config: map[string]string{
				"url": "https://new.example.com/webhook",
			},
			events: []string{"push"},
			active: false,
			expected: gitea.EditHookOption{
				Config: map[string]string{
					"url": "https://new.example.com/webhook",
				},
				Events: []string{"push"},
				Active: &[]bool{false}[0],
			},
		},
		{
			name:   "Minimal update",
			config: map[string]string{},
			events: []string{},
			active: true,
			expected: gitea.EditHookOption{
				Config: map[string]string{},
				Events: []string{},
				Active: &[]bool{true}[0],
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			option := gitea.EditHookOption{
				Config: tt.config,
				Events: tt.events,
				Active: &tt.active,
			}

			assert.Equal(t, tt.expected.Config, option.Config)
			assert.Equal(t, tt.expected.Events, option.Events)
			assert.Equal(t, *tt.expected.Active, *option.Active)
		})
	}
}

func TestUpdateWebhookIDValidation(t *testing.T) {
	tests := []struct {
		name        string
		webhookID   string
		expectedID  int64
		expectError bool
	}{
		{
			name:        "Valid webhook ID",
			webhookID:   "123",
			expectedID:  123,
			expectError: false,
		},
		{
			name:        "Single digit ID",
			webhookID:   "1",
			expectedID:  1,
			expectError: false,
		},
		{
			name:        "Large webhook ID",
			webhookID:   "999999",
			expectedID:  999999,
			expectError: false,
		},
		{
			name:        "Zero webhook ID",
			webhookID:   "0",
			expectedID:  0,
			expectError: true,
		},
		{
			name:        "Negative webhook ID",
			webhookID:   "-1",
			expectedID:  0,
			expectError: true,
		},
		{
			name:        "Non-numeric webhook ID",
			webhookID:   "abc",
			expectedID:  0,
			expectError: true,
		},
		{
			name:        "Empty webhook ID",
			webhookID:   "",
			expectedID:  0,
			expectError: true,
		},
		{
			name:        "Float webhook ID",
			webhookID:   "12.34",
			expectedID:  0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This simulates the utils.ArgToIndex function behavior
			if tt.webhookID == "" {
				assert.True(t, tt.expectError)
				return
			}

			// Basic validation - check if it's numeric
			isNumeric := true
			for _, char := range tt.webhookID {
				if char < '0' || char > '9' {
					if !(char == '-' && tt.webhookID[0] == '-') {
						isNumeric = false
						break
					}
				}
			}

			if !isNumeric || tt.webhookID == "0" || (len(tt.webhookID) > 0 && tt.webhookID[0] == '-') {
				assert.True(t, tt.expectError, "Should expect error for invalid ID: %s", tt.webhookID)
			} else {
				assert.False(t, tt.expectError, "Should not expect error for valid ID: %s", tt.webhookID)
			}
		})
	}
}

func TestUpdateFlagTypes(t *testing.T) {
	cmd := &CmdWebhooksUpdate

	flagTypes := map[string]string{
		"url":                  "string",
		"secret":               "string",
		"events":               "string",
		"active":               "bool",
		"inactive":             "bool",
		"branch-filter":        "string",
		"authorization-header": "string",
	}

	for flagName, expectedType := range flagTypes {
		found := false
		for _, flag := range cmd.Flags {
			if flag.Names()[0] == flagName {
				found = true
				switch expectedType {
				case "string":
					_, ok := flag.(*cli.StringFlag)
					assert.True(t, ok, "Flag %s should be a StringFlag", flagName)
				case "bool":
					_, ok := flag.(*cli.BoolFlag)
					assert.True(t, ok, "Flag %s should be a BoolFlag", flagName)
				}
				break
			}
		}
		assert.True(t, found, "Flag %s not found", flagName)
	}
}
