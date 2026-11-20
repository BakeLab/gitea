// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/actions"
	"gitea.dev/services/oauth2_provider"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTokenUsesAuthorizationHeaderOnly(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/user?token=query-token&access_token=query-access-token", nil)
	token, ok := parseToken(req)
	assert.False(t, ok)
	assert.Empty(t, token)

	req.Header.Set("Authorization", "token header-token")
	token, ok = parseToken(req)
	assert.True(t, ok)
	assert.Equal(t, "header-token", token)
}

func TestUserIDFromToken(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Actions JWT", func(t *testing.T) {
		oldKey := oauth2_provider.DefaultSigningKey
		t.Cleanup(func() { oauth2_provider.DefaultSigningKey = oldKey })
		signingKey, err := oauth2_provider.CreateJWTSigningKey("HS256", setting.GetGeneralTokenSigningSecret())
		require.NoError(t, err)
		oauth2_provider.DefaultSigningKey = signingKey

		const RunningTaskID int64 = 47
		token, err := actions.CreateAuthorizationToken(RunningTaskID, 1, 2)
		assert.NoError(t, err)

		o := OAuth2{}
		u, err := o.userFromToken(t.Context(), token)
		require.Error(t, err)
		assert.Nil(t, u)
	})

	t.Run("Access token", func(t *testing.T) {
		token := &auth_model.AccessToken{UID: 2, Name: "public-only", Scope: "public-only,read:repository"}
		require.NoError(t, auth_model.NewAccessToken(t.Context(), token))

		u, err := (&OAuth2{}).userFromToken(t.Context(), token.Token)
		require.NoError(t, err)
		scope, _ := user_model.GetDoerTokenScope(u)
		assert.Equal(t, token.Scope, scope)
		assert.Equal(t, fmt.Sprintf("access-token:%d", token.ID), user_model.GetDoerCredential(u))
	})
}
