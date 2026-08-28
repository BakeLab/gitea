// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoginStatusFormatField(t *testing.T) {
	status := LoginStatus{
		Name:        "gitea",
		URL:         "https://gitea.com",
		User:        "alice",
		Valid:       true,
		AuthMethod:  "oauth",
		TokenExpiry: time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC),
		Helper:      true,
		Default:     true,
	}

	assert.Equal(t, "gitea", status.FormatField("name", false))
	assert.Equal(t, "https://gitea.com", status.FormatField("url", false))
	assert.Equal(t, "alice", status.FormatField("user", false))
	assert.Equal(t, "true", status.FormatField("valid", true))
	assert.Equal(t, "✔", status.FormatField("valid", false))
	assert.Equal(t, "oauth", status.FormatField("auth_method", true))
	assert.Equal(t, "2026-08-27T12:00:00Z", status.FormatField("token_expiry", true))
	assert.Equal(t, "true", status.FormatField("helper", true))
	assert.Equal(t, "✔", status.FormatField("default", false))
}

func TestFormatExpiryDurationExpired(t *testing.T) {
	assert.Equal(t, "expired", formatExpiryDuration(time.Now().Add(-time.Hour)))
}
