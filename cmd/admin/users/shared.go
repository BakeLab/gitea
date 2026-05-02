// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package users

import (
	"fmt"
	"strings"

	"code.gitea.io/sdk/gitea"
)

func parseUserVisibility(visibility string) (*gitea.VisibleType, error) {
	switch visibility {
	case "public":
		vis := gitea.VisibleTypePublic
		return &vis, nil
	case "limited":
		vis := gitea.VisibleTypeLimited
		return &vis, nil
	case "private":
		vis := gitea.VisibleTypePrivate
		return &vis, nil
	default:
		return nil, fmt.Errorf("invalid visibility: %s (must be public, limited, or private)", visibility)
	}
}

func isConfirmationAccepted(response string) bool {
	trimmed := strings.TrimSpace(response)
	return strings.EqualFold(trimmed, "y") || strings.EqualFold(trimmed, "yes")
}
