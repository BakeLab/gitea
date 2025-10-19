// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"strconv"
	"strings"

	"code.gitea.io/sdk/gitea"
)

// WebhooksList prints a listing of webhooks
func WebhooksList(hooks []*gitea.Hook, output string) {
	t := tableWithHeader(
		"ID",
		"Type",
		"URL",
		"Events",
		"Active",
		"Updated",
	)

	for _, hook := range hooks {
		var url string
		if hook.Config != nil {
			url = hook.Config["url"]
		}

		events := strings.Join(hook.Events, ",")
		if len(events) > 40 {
			events = events[:37] + "..."
		}

		active := "✓"
		if !hook.Active {
			active = "✗"
		}

		t.addRow(
			strconv.FormatInt(hook.ID, 10),
			string(hook.Type),
			url,
			events,
			active,
			FormatTime(hook.Updated, false),
		)
	}

	t.print(output)
}

// WebhookDetails prints detailed information about a webhook
func WebhookDetails(hook *gitea.Hook) {
	fmt.Printf("# Webhook %d\n\n", hook.ID)
	fmt.Printf("- **Type**: %s\n", hook.Type)
	fmt.Printf("- **Active**: %t\n", hook.Active)
	fmt.Printf("- **Created**: %s\n", FormatTime(hook.Created, false))
	fmt.Printf("- **Updated**: %s\n", FormatTime(hook.Updated, false))

	if hook.Config != nil {
		fmt.Printf("- **URL**: %s\n", hook.Config["url"])
		if contentType, ok := hook.Config["content_type"]; ok {
			fmt.Printf("- **Content Type**: %s\n", contentType)
		}
		if method, ok := hook.Config["http_method"]; ok {
			fmt.Printf("- **HTTP Method**: %s\n", method)
		}
		if branchFilter, ok := hook.Config["branch_filter"]; ok && branchFilter != "" {
			fmt.Printf("- **Branch Filter**: %s\n", branchFilter)
		}
		if _, hasSecret := hook.Config["secret"]; hasSecret {
			fmt.Printf("- **Secret**: (configured)\n")
		}
		if _, hasAuth := hook.Config["authorization_header"]; hasAuth {
			fmt.Printf("- **Authorization Header**: (configured)\n")
		}
	}

	fmt.Printf("- **Events**: %s\n", strings.Join(hook.Events, ", "))
}
