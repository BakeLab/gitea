// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package wiki

import (
	stdctx "context"
	"testing"

	"gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
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

func TestGetWikiCreateOptionsRequiresTitle(t *testing.T) {
	_, err := buildWikiWriteOptions("", "# Home", "", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title")
}

func TestGetWikiCreateOptionsEncodesContent(t *testing.T) {
	opts, err := buildWikiWriteOptions("Home", "# Home", "create home", true)
	require.NoError(t, err)
	assert.Equal(t, "Home", opts.Title)
	assert.Equal(t, "IyBIb21l", opts.ContentBase64)
	assert.Equal(t, "create home", opts.Message)
}

func TestGetWikiEditOptionsAllowsEmptyTitleButRequiresContent(t *testing.T) {
	_, err := buildWikiWriteOptions("", "", "edit home", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "content")

	opts, err := buildWikiWriteOptions("", "# Updated", "edit home", false)
	require.NoError(t, err)
	assert.Equal(t, "", opts.Title)
	assert.Equal(t, "IyBVcGRhdGVk", opts.ContentBase64)
	assert.Equal(t, "edit home", opts.Message)
}

func TestWikiWriteCommandsRegistered(t *testing.T) {
	assert.Equal(t, "create", CmdWikiCreate.Name)
	assert.Equal(t, "edit", CmdWikiEdit.Name)
	assert.Equal(t, "delete", CmdWikiDelete.Name)
}

func TestRunWikiCreateWithClientUsesRepoAndOptions(t *testing.T) {
	ctx := newWikiTestContext("json")
	client := &fakeWikiWriteClient{created: &gitea.WikiPage{Title: "Home", ContentBase64: "IyBIb21l"}}
	cmd := &cli.Command{Flags: []cli.Flag{wikiTitleFlag, wikiContentFlag, wikiMessageFlag}}
	require.NoError(t, cmd.Set("title", "Home"))
	require.NoError(t, cmd.Set("content", "# Home"))
	require.NoError(t, cmd.Set("message", "create home"))

	err := runWikiCreateWithClient(ctx, client, cmd)
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.create.Title)
	assert.Equal(t, "IyBIb21l", client.create.ContentBase64)
	assert.Equal(t, "create home", client.create.Message)
}

func TestRunWikiEditWithClientUsesPageAndOptions(t *testing.T) {
	ctx := newWikiTestContext("json")
	client := &fakeWikiWriteClient{edited: &gitea.WikiPage{Title: "Home", ContentBase64: "IyBVcGRhdGVk"}}
	cmd := &cli.Command{Flags: []cli.Flag{wikiTitleFlag, wikiContentFlag, wikiMessageFlag}}
	require.NoError(t, cmd.Set("content", "# Updated"))
	require.NoError(t, cmd.Set("message", "edit home"))

	err := runWikiEditWithClient(ctx, "Home", client, cmd)
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.pageName)
	assert.Equal(t, "Home", client.edit.Title)
	assert.Equal(t, "IyBVcGRhdGVk", client.edit.ContentBase64)
	assert.Equal(t, "edit home", client.edit.Message)
}

func TestRunWikiDeleteWithClientRequiresConfirm(t *testing.T) {
	ctx := newWikiTestContext("json")
	client := &fakeWikiWriteClient{}

	err := runWikiDeleteWithClient(ctx, "Home", client, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--confirm")
	assert.False(t, client.deleted)

	err = runWikiDeleteWithClient(ctx, "Home", client, true)
	require.NoError(t, err)
	assert.True(t, client.deleted)
	assert.Equal(t, "Home", client.pageName)
}

var _ = gitea.CreateWikiPageOptions{}
