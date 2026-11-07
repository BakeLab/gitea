// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectServer answers GET requests from routes, keyed by path with "?closed" for closed project
// listings and 404 for any other, and records every other request with its body.
func projectServer(t *testing.T, routes map[string]string) (*transport.Client, *[]string) {
	t.Helper()
	var sent []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			sent = append(sent, r.Method+" "+r.URL.Path+" "+string(body))
			_, _ = w.Write([]byte(`{"id":30,"title":"Uncategorized"}`))
			return
		}
		key := r.URL.Path
		if r.URL.Query().Get("state") == string(gitea.StateClosed) {
			key += "?closed"
		}
		page, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(page, "[") {
			writeFirstPage(w, r, page)
			return
		}
		_, _ = w.Write([]byte(page))
	}))
	return client, &sent
}

func roadmapRoutes(issueProjects string) map[string]string {
	return map[string]string{
		"/api/v1/repos/alice/project/issues/7":                     `{"id":70,"number":7,"projects":` + issueProjects + `}`,
		"/api/v1/repos/alice/project/projects":                     `[{"id":1,"title":"Roadmap"}]`,
		"/api/v1/repos/alice/project/projects/1/columns":           `[{"id":10,"title":"To do","default":true},{"id":11,"title":"In progress"}]`,
		"/api/v1/repos/alice/project/projects/1/columns/10/issues": `[{"id":70}]`,
		"/api/v1/repos/alice/project/projects/1/columns/11/issues": `[]`,
		"/api/v1/orgs/alice/projects":                              `[{"id":2,"title":"Org board"}]`,
		"/api/v1/orgs/alice/projects/2/columns":                    `[{"id":20,"title":"To do"}]`,
		"/api/v1/orgs/alice/projects/2/columns/20/issues":          `[{"id":70}]`,
	}
}

func TestProjectMoveSendsTheLooselyMatchedColumn(t *testing.T) {
	client, sent := projectServer(t, roadmapRoutes(`[{"id":1},{"id":2,"is_closed":true}]`))
	service := Projects{Client: client, Repository: "alice/project"}
	moved, err := service.Move(context.Background(), 7, "in-progress", "")
	require.NoError(t, err)
	require.Equal(t, "Roadmap", moved.Project)
	require.Equal(t, "In progress", moved.Column)
	require.False(t, moved.Unchanged)
	require.Equal(t, []string{`POST /api/v1/repos/alice/project/projects/1/issues/70/move {"column_id":11}`}, *sent)

	moved, err = service.Move(context.Background(), 7, "10", "Roadmap")
	require.NoError(t, err)
	require.True(t, moved.Unchanged)
	require.Equal(t, "To do", moved.Column)
	require.Len(t, *sent, 1, "an issue in the column already is not moved")
}

func TestProjectMoveRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, projects, column, project string
		change                          map[string]string
		err                             string
	}{
		{name: "no project", projects: `[{"id":1,"is_closed":true}]`, column: "To do", err: "issue #7 is not on any open project"},
		{name: "other project", projects: `[{"id":1}]`, column: "To do", project: "org board", err: `issue #7 is not on project "Org board"`},
		{
			name: "closed project", projects: `[{"id":1}]`, column: "To do", project: "Old",
			change: map[string]string{"/api/v1/repos/alice/project/projects?closed": `[{"id":5,"title":"old"}]`}, err: `project "Old" is closed`,
		},
		{name: "unknown project", projects: `[{"id":1}]`, column: "To do", project: "Nope", err: `project "Nope" not found in alice/project`},
		{name: "unknown column", projects: `[{"id":1}]`, column: "Done", err: "no column matches \"Done\"; available columns:\n  Roadmap: To do, In progress"},
		{name: "column on two projects", projects: `[{"id":1},{"id":2}]`, column: "to do", err: `column "to do" is on several projects of issue #7; select one with --project`},
		{
			name: "ambiguous column", projects: `[{"id":1}]`, column: "DONE",
			change: map[string]string{"/api/v1/repos/alice/project/projects/1/columns": `[{"id":10,"title":"Done"},{"id":11,"title":"done"}]`},
			err:    `project "Roadmap": column "DONE" is ambiguous (IDs 10, 11); pass an ID`,
		},
		{
			name: "unlisted issue", projects: `[{"id":1}]`, column: "In progress",
			change: map[string]string{"/api/v1/repos/alice/project/projects/1/columns/10/issues": `[]`},
			err:    `issue #7 is on project "Roadmap", but its columns do not list it; moving it needs write access to the project`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes := roadmapRoutes(tc.projects)
			maps.Copy(routes, tc.change)
			client, sent := projectServer(t, routes)
			_, err := (Projects{Client: client, Repository: "alice/project"}).Move(context.Background(), 7, tc.column, tc.project)
			require.EqualError(t, err, tc.err)
			require.Empty(t, *sent)
		})
	}
}

func TestFindColumnMatchesIDsTitlesThenSquashedTitles(t *testing.T) {
	columns := []ProjectColumn{{ID: 1, Title: "To do"}, {ID: 2, Title: "In progress"}, {ID: 3, Title: "In-Review"}, {ID: 4, Title: "in review"}}
	for _, tc := range []struct {
		name string
		want int64
	}{{"2", 2}, {"to do", 1}, {"in_progress", 2}, {"In Progress!", 2}, {"In-Review", 3}} {
		found, err := FindColumn(columns, tc.name)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, found.ID, tc.name)
	}
	for _, name := range []string{"inreview", "---", "done"} {
		_, err := FindColumn(columns, name)
		require.Error(t, err, name)
	}
}

func TestProjectBoardsFallBackToTheAccountsOwnProjects(t *testing.T) {
	client, sent := projectServer(t, map[string]string{
		"/api/v1/user":                              `{"login":"Alice"}`,
		"/api/v1/user/projects":                     `[{"id":3,"title":"Mine"}]`,
		"/api/v1/user/projects/3/columns":           `[{"id":31,"title":"Todo"},{"id":32,"title":"Doing","default":true}]`,
		"/api/v1/user/projects/3/columns/31/issues": `[{"id":70}]`,
		"/api/v1/user/projects/3/columns/32/issues": `[]`,
	})
	service := Projects{Client: client, Repository: "alice/project"}
	boards, err := service.Boards(context.Background(), []string{"mine", "MINE", "3"}, true)
	require.NoError(t, err)
	require.Len(t, boards, 3)
	for _, board := range boards {
		require.Equal(t, int64(3), board.ID)
		require.Equal(t, int64(32), board.defaultColumn)
	}

	added, err := service.Add(context.Background(), &boards[0], 70)
	require.NoError(t, err)
	require.False(t, added, "an issue on the board keeps its column")
	added, err = service.Add(context.Background(), &boards[0], 71)
	require.NoError(t, err)
	require.True(t, added)
	removed, err := service.Remove(context.Background(), boards[0], 72)
	require.NoError(t, err)
	require.False(t, removed)
	removed, err = service.Remove(context.Background(), boards[0], 70)
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, []string{
		"POST /api/v1/user/projects/3/columns/32/issues/71 ",
		"DELETE /api/v1/user/projects/3/columns/31/issues/70 ",
	}, *sent)
}

func TestProjectAddCreatesTheDefaultColumnOfAnEmptyBoard(t *testing.T) {
	client, sent := projectServer(t, map[string]string{
		"/api/v1/repos/alice/project/projects":           `[{"id":1,"title":"Roadmap"}]`,
		"/api/v1/repos/alice/project/projects/1/columns": `[]`,
	})
	service := Projects{Client: client, Repository: "alice/project"}
	boards, err := service.Boards(context.Background(), []string{"Roadmap"}, true)
	require.NoError(t, err)
	added, err := service.Add(context.Background(), &boards[0], 70)
	require.NoError(t, err)
	require.True(t, added)
	require.Equal(t, []string{
		`POST /api/v1/repos/alice/project/projects/1/columns {"title":"Uncategorized"}`,
		"POST /api/v1/repos/alice/project/projects/1/columns/30/default ",
		"POST /api/v1/repos/alice/project/projects/1/columns/30/issues/70 ",
	}, *sent)
}

func TestProjectBoardsSkipAnotherUsersProjects(t *testing.T) {
	client, _ := projectServer(t, map[string]string{
		"/api/v1/user":          `{"login":"bob"}`,
		"/api/v1/user/projects": `[{"id":3,"title":"Mine"}]`,
	})
	_, err := (Projects{Client: client, Repository: "alice/project"}).Boards(context.Background(), []string{"Mine"}, false)
	require.EqualError(t, err, `project "Mine" not found in alice/project`)
}
