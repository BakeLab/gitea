// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireRepo1Tarball(t *testing.T, data []byte) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	contents, err := io.ReadAll(gz)
	require.NoError(t, err)
	require.NoError(t, gz.Close())

	archive := tar.NewReader(bytes.NewReader(contents))
	dir, err := archive.Next()
	require.NoError(t, err)
	// Git may include the commit ID in a global PAX header.
	if dir.Typeflag == tar.TypeXGlobalHeader {
		dir, err = archive.Next()
		require.NoError(t, err)
	}
	require.Equal(t, "repo1/", dir.Name)
	require.Equal(t, byte(tar.TypeDir), dir.Typeflag)

	file, err := archive.Next()
	require.NoError(t, err)
	require.Equal(t, "repo1/README.md", file.Name)
	require.Equal(t, byte(tar.TypeReg), file.Typeflag)
	readme, err := io.ReadAll(archive)
	require.NoError(t, err)
	require.Equal(t, "# repo1\n\nDescription for repo1", string(readme))
	_, err = archive.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestAPIDownloadArchive(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

	link, _ := url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/archive/master.zip", user2.Name, repo.Name))
	resp := MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	bs, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Len(t, bs, 320)

	link, _ = url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/archive/master.tar.gz", user2.Name, repo.Name))
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	bs, err = io.ReadAll(resp.Body)
	assert.NoError(t, err)
	requireRepo1Tarball(t, bs)

	// Must return a link to a commit ID as the "immutable" archive link
	linkHeaderRe := regexp.MustCompile(`^<(https?://.*/api/v1/repos/user2/repo1/archive/[a-f0-9]+\.tar\.gz.*)>; rel="immutable"$`)
	m := linkHeaderRe.FindStringSubmatch(resp.Header().Get("Link"))
	assert.NotEmpty(t, m[1])
	resp = MakeRequest(t, NewRequest(t, "GET", m[1]).AddTokenAuth(token), http.StatusOK)
	bs2, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	// The locked URL should give the same bytes as the non-locked one
	assert.Equal(t, bs, bs2)

	link, _ = url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/archive/master.bundle", user2.Name, repo.Name))
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	bs, err = io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Len(t, bs, 382)

	link, _ = url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/archive/master", user2.Name, repo.Name))
	MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusBadRequest)

	t.Run("GitHubStyle", testAPIDownloadArchiveGitHubStyle)
	t.Run("PrivateRepo", testAPIDownloadArchivePrivateRepo)
}

func testAPIDownloadArchiveGitHubStyle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	session := loginUser(t, user2.LowerName)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)

	link, _ := url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/zipball/master", user2.Name, repo.Name))
	resp := MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	bs, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Len(t, bs, 320)

	link, _ = url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/tarball/master", user2.Name, repo.Name))
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	bs, err = io.ReadAll(resp.Body)
	assert.NoError(t, err)
	requireRepo1Tarball(t, bs)

	// Must return a link to a commit ID as the "immutable" archive link
	linkHeaderRe := regexp.MustCompile(`^<(https?://.*/api/v1/repos/user2/repo1/archive/[a-f0-9]+\.tar\.gz.*)>; rel="immutable"$`)
	m := linkHeaderRe.FindStringSubmatch(resp.Header().Get("Link"))
	assert.NotEmpty(t, m[1])
	resp = MakeRequest(t, NewRequest(t, "GET", m[1]).AddTokenAuth(token), http.StatusOK)
	bs2, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	// The locked URL should give the same bytes as the non-locked one
	assert.Equal(t, bs, bs2)

	link, _ = url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/bundle/master", user2.Name, repo.Name))
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()).AddTokenAuth(token), http.StatusOK)
	bs, err = io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Len(t, bs, 382)
}

func testAPIDownloadArchivePrivateRepo(t *testing.T) {
	_ = repo_model.UpdateRepositoryColsNoAutoTime(t.Context(), &repo_model.Repository{ID: 1, IsPrivate: true}, "is_private")
	MakeRequest(t, NewRequest(t, "HEAD", "/api/v1/repos/user2/repo1/archive/master.zip"), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "HEAD", "/api/v1/repos/user2/repo1/zipball/master"), http.StatusNotFound)
	_ = repo_model.UpdateRepoUnitPublicAccess(t.Context(), &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeCode, AnonymousAccessMode: perm.AccessModeRead})
	MakeRequest(t, NewRequest(t, "HEAD", "/api/v1/repos/user2/repo1/archive/master.zip"), http.StatusOK)
	MakeRequest(t, NewRequest(t, "HEAD", "/api/v1/repos/user2/repo1/zipball/master"), http.StatusOK)
}
