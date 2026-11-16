// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
)

func TestAPIActionsCrossRepositoryPermissions(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	org := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "org3"})
	repository := unittest.AssertExistsAndLoadBean(t, &repo.Repository{ID: 3})
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteOrganization)
	permissionsURL := fmt.Sprintf("/api/v1/orgs/%s/actions/permissions", org.Name)
	repositoryURL := permissionsURL + "/repositories/" + repository.Name

	getPermissions := func() api.ActionsCrossRepositoryPermissions {
		resp := MakeRequest(t, NewRequest(t, http.MethodGet, permissionsURL).AddTokenAuth(token), http.StatusOK)
		return *DecodeJSON(t, resp, &api.ActionsCrossRepositoryPermissions{})
	}

	assert.Empty(t, getPermissions().AllowedRepositories)

	request := NewRequest(t, http.MethodPut, repositoryURL).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)
	request = NewRequest(t, http.MethodPut, repositoryURL).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)
	assert.Equal(t, []string{repository.FullName()}, getPermissions().AllowedRepositories)

	readToken := getUserToken(t, "user2", auth_model.AccessTokenScopeReadOrganization)
	request = NewRequest(t, http.MethodPut, repositoryURL).AddTokenAuth(readToken)
	MakeRequest(t, request, http.StatusForbidden)

	request = NewRequest(t, http.MethodDelete, repositoryURL).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)
	request = NewRequest(t, http.MethodDelete, repositoryURL).AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNoContent)
	assert.Empty(t, getPermissions().AllowedRepositories)

	request = NewRequest(t, http.MethodPut, permissionsURL+"/repositories/missing").AddTokenAuth(token)
	MakeRequest(t, request, http.StatusNotFound)
}
