// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"gitea.dev/sdk"
)

// WikiPageFields are all available fields to print with WikiPagesList().
var WikiPageFields = []string{
	"title",
	"path",
	"url",
	"sha",
	"author",
	"updated",
	"message",
}

// WikiRevisionFields are all available fields to print with WikiRevisionsList().
var WikiRevisionFields = []string{
	"sha",
	"message",
	"author",
	"date",
}

// WikiPagesList prints a listing of wiki pages.
func WikiPagesList(pages []*gitea.WikiPageMetaData, output string, fields []string) error {
	if len(pages) == 0 && !isMachineReadable(output) {
		fmt.Println("No wiki pages found")
		return nil
	}

	t := wikiPagesTable(pages, fields, isMachineReadable(output))
	return t.print(output)
}

// WikiPageDetails prints a wiki page.
func WikiPageDetails(page *gitea.WikiPage, output string) error {
	if output == "" {
		markdown, err := renderWikiPageMarkdown(page)
		if err != nil {
			return err
		}
		return outputMarkdown(markdown, getRepoURL(page.HTMLURL))
	}

	t := wikiPageDetailsTable(page, isMachineReadable(output))
	return t.print(output)
}

// WikiRevisionsList prints a listing of wiki page revisions.
func WikiRevisionsList(revisions []*gitea.WikiCommit, output string, fields []string) error {
	t := wikiRevisionsTable(revisions, fields, isMachineReadable(output))
	return t.print(output)
}

func wikiPagesTable(pages []*gitea.WikiPageMetaData, fields []string, machineReadable bool) table {
	printables := make([]printable, len(pages))
	for i, page := range pages {
		printables[i] = printableWikiPageMetaData{page}
	}
	return tableFromItems(fields, printables, machineReadable)
}

func wikiPageDetailsTable(page *gitea.WikiPage, machineReadable bool) table {
	content, _ := decodeWikiContent(page.ContentBase64)
	fields := []string{"title", "content", "commits", "path", "url", "sha", "author", "updated", "message"}
	return tableFromItems(fields, []printable{printableWikiPage{page: page, content: content}}, machineReadable)
}

func wikiRevisionsTable(revisions []*gitea.WikiCommit, fields []string, machineReadable bool) table {
	printables := make([]printable, len(revisions))
	for i, revision := range revisions {
		printables[i] = printableWikiRevision{revision}
	}
	return tableFromItems(fields, printables, machineReadable)
}

func renderWikiPageMarkdown(page *gitea.WikiPage) (string, error) {
	content, err := decodeWikiContent(page.ContentBase64)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, nil
}

func decodeWikiContent(contentBase64 string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(contentBase64)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

type printableWikiPageMetaData struct{ *gitea.WikiPageMetaData }

type printableWikiPage struct {
	page    *gitea.WikiPage
	content string
}

type printableWikiRevision struct{ *gitea.WikiCommit }

func (x printableWikiPageMetaData) FormatField(field string, machineReadable bool) string {
	switch field {
	case "title":
		return x.Title
	case "path":
		return x.SubURL
	case "url":
		return x.HTMLURL
	case "sha":
		return shortWikiCommitID(x.LastCommit)
	case "author":
		return wikiCommitAuthor(x.LastCommit)
	case "updated":
		return wikiCommitDate(x.LastCommit, machineReadable)
	case "message":
		return wikiCommitMessage(x.LastCommit)
	}
	return ""
}

func (x printableWikiPage) FormatField(field string, machineReadable bool) string {
	switch field {
	case "title":
		return x.page.Title
	case "content":
		return x.content
	case "commits":
		return fmt.Sprintf("%d", x.page.CommitCount)
	case "path":
		return x.page.SubURL
	case "url":
		return x.page.HTMLURL
	case "sha":
		return shortWikiCommitID(x.page.LastCommit)
	case "author":
		return wikiCommitAuthor(x.page.LastCommit)
	case "updated":
		return wikiCommitDate(x.page.LastCommit, machineReadable)
	case "message":
		return wikiCommitMessage(x.page.LastCommit)
	}
	return ""
}

func (x printableWikiRevision) FormatField(field string, machineReadable bool) string {
	switch field {
	case "sha":
		return shortWikiCommitID(x.WikiCommit)
	case "message":
		return x.Message
	case "author":
		return wikiCommitAuthor(x.WikiCommit)
	case "date":
		return wikiCommitDate(x.WikiCommit, machineReadable)
	}
	return ""
}

func shortWikiCommitID(commit *gitea.WikiCommit) string {
	if commit == nil {
		return ""
	}
	if len(commit.ID) <= 7 {
		return commit.ID
	}
	return commit.ID[:7]
}

func wikiCommitAuthor(commit *gitea.WikiCommit) string {
	if commit == nil || commit.Author == nil {
		return ""
	}
	if commit.Author.Name != "" {
		return commit.Author.Name
	}
	return commit.Author.Email
}

func wikiCommitDate(commit *gitea.WikiCommit, machineReadable bool) string {
	if commit == nil || commit.Author == nil || commit.Author.Date == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, commit.Author.Date)
	if err != nil {
		return commit.Author.Date
	}
	return FormatTime(parsed, machineReadable)
}

func wikiCommitMessage(commit *gitea.WikiCommit) string {
	if commit == nil {
		return ""
	}
	return commit.Message
}
