// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package wiki

import (
	"bytes"
	stdctx "context"
	"errors"
	"net/http"
	"testing"

	"gitea.dev/sdk"
	"gitea.dev/tea/cmd/flags"
	teaContext "gitea.dev/tea/modules/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type fakeWikiReadClient struct {
	owner        string
	repo         string
	pageName     string
	listOpts     gitea.ListWikiPagesOptions
	revisionOpts gitea.ListWikiPageRevisionsOptions
	pages        []*gitea.WikiPageMetaData
	page         *gitea.WikiPage
	revisions    *gitea.WikiCommitList
	listResp     *gitea.Response
	listErr      error
}

func (f *fakeWikiReadClient) ListPages(_ stdctx.Context, owner, repo string, opt gitea.ListWikiPagesOptions) ([]*gitea.WikiPageMetaData, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.listOpts = opt
	return f.pages, f.listResp, f.listErr
}

func (f *fakeWikiReadClient) GetPage(_ stdctx.Context, owner, repo, pageName string) (*gitea.WikiPage, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.pageName = pageName
	return f.page, nil, nil
}

func (f *fakeWikiReadClient) GetPageRevisions(_ stdctx.Context, owner, repo, pageName string, opt gitea.ListWikiPageRevisionsOptions) (*gitea.WikiCommitList, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.pageName = pageName
	f.revisionOpts = opt
	return f.revisions, nil, nil
}

func newWikiTestContext(output string) *teaContext.TeaContext {
	cmd := &cli.Command{
		Flags: []cli.Flag{
			&flags.PaginationPageFlag,
			&flags.PaginationLimitFlag,
			&flags.OutputFlag,
		},
	}
	cmd.Writer = &bytes.Buffer{}
	requireNoError := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	requireNoError(cmd.Set("output", output))
	return &teaContext.TeaContext{
		Command: cmd,
		Owner:   "octo",
		Repo:    "tea",
		Output:  output,
	}
}

func TestRunWikiListWithClientUsesRepoAndPagination(t *testing.T) {
	ctx := newWikiTestContext("json")
	require.NoError(t, ctx.Set("page", "2"))
	require.NoError(t, ctx.Set("limit", "5"))

	client := &fakeWikiReadClient{
		pages: []*gitea.WikiPageMetaData{{Title: "Home"}},
	}

	err := runWikiListWithClient(ctx, client, []string{"title"})
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, 2, client.listOpts.Page)
	assert.Equal(t, 5, client.listOpts.PageSize)
}

func TestRunWikiListWithClientTreats404AsEmptyWiki(t *testing.T) {
	ctx := newWikiTestContext("json")
	client := &fakeWikiReadClient{
		listResp: &gitea.Response{Response: &http.Response{StatusCode: http.StatusNotFound}},
		listErr:  errors.New("not found"),
	}

	err := runWikiListWithClient(ctx, client, []string{"title"})
	require.NoError(t, err)
}

func TestRunWikiViewWithClientUsesRequestedPage(t *testing.T) {
	ctx := newWikiTestContext("json")
	client := &fakeWikiReadClient{
		page: &gitea.WikiPage{Title: "Home", ContentBase64: "IyBIZWxsbw=="},
	}

	err := runWikiViewWithClient(ctx, "Home", client)
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.pageName)
}

func TestRunWikiRevisionsWithClientUsesRequestedPageAndPageFlag(t *testing.T) {
	ctx := newWikiTestContext("json")
	require.NoError(t, ctx.Set("page", "3"))
	client := &fakeWikiReadClient{
		revisions: &gitea.WikiCommitList{WikiCommits: []*gitea.WikiCommit{}},
	}

	err := runWikiRevisionsWithClient(ctx, "Home", client, []string{"sha"})
	require.NoError(t, err)
	assert.Equal(t, "octo", client.owner)
	assert.Equal(t, "tea", client.repo)
	assert.Equal(t, "Home", client.pageName)
	assert.Equal(t, 3, client.revisionOpts.Page)
}
