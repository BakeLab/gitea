// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiPagesTableJSON(t *testing.T) {
	pages := []*gitea.WikiPageMetaData{{
		Title:   "Home",
		HTMLURL: "https://gitea.example.com/octo/tea/wiki/Home",
		SubURL:  "/octo/tea/wiki/Home",
		LastCommit: &gitea.WikiCommit{
			ID:      "abcdef123456",
			Message: "update home",
			Author:  &gitea.CommitUser{GitIdentity: gitea.GitIdentity{Name: "alice"}, Date: "2026-05-21T01:02:03Z"},
		},
	}}

	tbl := wikiPagesTable(pages, []string{"title", "author", "updated", "sha", "url"}, true)
	buf := &bytes.Buffer{}
	require.NoError(t, tbl.fprint(buf, "json"))

	var rows []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "Home", rows[0]["title"])
	assert.Equal(t, "alice", rows[0]["author"])
	assert.Equal(t, "2026-05-21T01:02:03Z", rows[0]["updated"])
	assert.Equal(t, "abcdef1", rows[0]["sha"])
	assert.Equal(t, "https://gitea.example.com/octo/tea/wiki/Home", rows[0]["url"])
}

func TestWikiRevisionsTableJSON(t *testing.T) {
	revisions := []*gitea.WikiCommit{{
		ID:      "1234567890abcdef",
		Message: "second revision",
		Author:  &gitea.CommitUser{GitIdentity: gitea.GitIdentity{Name: "bob"}, Date: "2026-05-22T03:04:05Z"},
	}}

	tbl := wikiRevisionsTable(revisions, []string{"sha", "message", "author", "date"}, true)
	buf := &bytes.Buffer{}
	require.NoError(t, tbl.fprint(buf, "json"))

	var rows []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "1234567", rows[0]["sha"])
	assert.Equal(t, "second revision", rows[0]["message"])
	assert.Equal(t, "bob", rows[0]["author"])
	assert.Equal(t, "2026-05-22T03:04:05Z", rows[0]["date"])
}

func TestRenderWikiPageMarkdownReturnsDecodedWikiContent(t *testing.T) {
	page := &gitea.WikiPage{
		Title:         "Home",
		HTMLURL:       "https://gitea.example.com/octo/tea/wiki/Home",
		ContentBase64: "IyBIZWxsbyB3aWtp",
		LastCommit: &gitea.WikiCommit{
			Author: &gitea.CommitUser{GitIdentity: gitea.GitIdentity{Name: "carol"}, Date: "2026-05-23T06:07:08Z"},
		},
	}

	out, err := renderWikiPageMarkdown(page)
	require.NoError(t, err)
	assert.Equal(t, "# Hello wiki\n", out)
}

func TestWikiPagesListEmptyHumanOutput(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, WikiPagesList(nil, "", []string{"title"}))
	})

	assert.Equal(t, "No wiki pages found\n", out)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan string, 1)
	go func() {
		var outBuf bytes.Buffer
		_, _ = io.Copy(&outBuf, reader)
		done <- outBuf.String()
	}()

	fn()
	require.NoError(t, writer.Close())
	output := <-done
	require.NoError(t, reader.Close())
	return output
}
