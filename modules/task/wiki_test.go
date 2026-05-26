// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"testing"

	"gitea.dev/sdk"
	teaContext "gitea.dev/tea/modules/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWikiWriteClient struct {
	owner    string
	repo     string
	pageName string
	create   gitea.CreateWikiPageOptions
	edit     gitea.CreateWikiPageOptions
	created  *gitea.WikiPage
	edited   *gitea.WikiPage
	deleted  bool
}

func (f *fakeWikiWriteClient) CreatePage(_ stdctx.Context, owner, repo string, opt gitea.CreateWikiPageOptions) (*gitea.WikiPage, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.create = opt
	return f.created, nil, nil
}

func (f *fakeWikiWriteClient) EditPage(_ stdctx.Context, owner, repo, pageName string, opt gitea.CreateWikiPageOptions) (*gitea.WikiPage, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.pageName = pageName
	f.edit = opt
	return f.edited, nil, nil
}

func (f *fakeWikiWriteClient) DeletePage(_ stdctx.Context, owner, repo, pageName string) (*gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.pageName = pageName
	f.deleted = true
	return nil, nil
}

func newWikiTaskContext() *teaContext.TeaContext {
	return &teaContext.TeaContext{Owner: "octo", Repo: "tea"}
}

func TestCreateWikiPageCallsSDK(t *testing.T) {
	client := &fakeWikiWriteClient{created: &gitea.WikiPage{Title: "Home"}}
	page, err := CreateWikiPage(newWikiTaskContext(), client, gitea.CreateWikiPageOptions{Title: "Home", ContentBase64: "IyBIb21l"})
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.create.Title)
	assert.Equal(t, "Home", page.Title)
}

func TestEditWikiPageCallsSDK(t *testing.T) {
	client := &fakeWikiWriteClient{edited: &gitea.WikiPage{Title: "Home"}}
	page, err := EditWikiPage(newWikiTaskContext(), client, "Home", gitea.CreateWikiPageOptions{ContentBase64: "IyBVcGRhdGVk"})
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.pageName)
	assert.Equal(t, "IyBVcGRhdGVk", client.edit.ContentBase64)
	assert.Equal(t, "Home", page.Title)
}

func TestDeleteWikiPageCallsSDK(t *testing.T) {
	client := &fakeWikiWriteClient{}
	err := DeleteWikiPage(newWikiTaskContext(), client, "Home")
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.pageName)
	assert.True(t, client.deleted)
}
