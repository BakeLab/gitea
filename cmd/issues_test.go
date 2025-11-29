// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const (
	testOwner = "testOwner"
	testRepo  = "testRepo"
)

func createTestIssue(comments int, isClosed bool) gitea.Issue {
	var issue = gitea.Issue{
		ID:      42,
		Index:   1,
		Title:   "Test issue",
		State:   gitea.StateOpen,
		Body:    "This is a test",
		Created: time.Date(2025, 31, 10, 23, 59, 59, 999999999, time.UTC),
		Updated: time.Date(2025, 1, 11, 0, 0, 0, 0, time.UTC),
		Labels: []*gitea.Label{
			{
				Name:        "example/Label1",
				Color:       "very red",
				Description: "This is an example label",
			},
			{
				Name:        "example/Label2",
				Color:       "hardly red",
				Description: "This is another example label",
			},
		},
		Comments: comments,
		Poster: &gitea.User{
			UserName: "testUser",
		},
		Assignees: []*gitea.User{
			{UserName: "testUser"},
			{UserName: "testUser3"},
		},
		HTMLURL: "<space holder>",
		Closed:  nil, //2025-11-10T21:20:19Z
	}

	if isClosed {
		var closed = time.Date(2025, 11, 10, 21, 20, 19, 0, time.UTC)
		issue.Closed = &closed
	}

	if isClosed {
		issue.State = gitea.StateClosed
	} else {
		issue.State = gitea.StateOpen
	}

	return issue

}

func createTestIssueComments(comments int) []gitea.Comment {
	baseID := 900
	var result []gitea.Comment

	for commentID := 0; commentID < comments; commentID++ {
		result = append(result, gitea.Comment{
			ID: int64(baseID + commentID),
			Poster: &gitea.User{
				UserName: "Freddy",
			},
			Body: fmt.Sprintf("This is a test comment #%v", commentID),
			Created: time.Date(2025, 11, 3, 12, 0, 0, 0, time.UTC).
				Add(time.Duration(commentID) * time.Hour),
		})
	}

	return result

}

func TestRunIssueDetailAsJSON(t *testing.T) {
	type TestCase struct {
		name         string
		issue        gitea.Issue
		comments     []gitea.Comment
		flagComments bool
		flagOutput   string
		flagOut      string
		closed       bool
	}

	cmd := cli.Command{
		Name: "t",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "comments",
				Value: false,
			},
			&cli.StringFlag{
				Name:  "output",
				Value: "json",
			},
		},
	}

	testContext := context.TeaContext{
		Owner: testOwner,
		Repo:  testRepo,
		Login: &config.Login{
			Name: "testLogin",
			URL:  "http://127.0.0.1:8081",
		},
		Command: &cmd,
	}

	testCases := []TestCase{
		{
			name:         "Simple issue with no comments, no comments requested",
			issue:        createTestIssue(0, true),
			comments:     []gitea.Comment{},
			flagComments: false,
		},
		{
			name:         "Simple issue with no comments, comments requested",
			issue:        createTestIssue(0, true),
			comments:     []gitea.Comment{},
			flagComments: true,
		},
		{
			name:         "Simple issue with comments, no comments requested",
			issue:        createTestIssue(2, true),
			comments:     createTestIssueComments(2),
			flagComments: false,
		},
		{
			name:         "Simple issue with comments, comments requested",
			issue:        createTestIssue(2, true),
			comments:     createTestIssueComments(2),
			flagComments: true,
		},
		{
			name:         "Simple issue with comments, comments requested, not closed",
			issue:        createTestIssue(2, false),
			comments:     createTestIssueComments(2),
			flagComments: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				if path == fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments", testOwner, testRepo, testCase.issue.Index) {
					jsonComments, err := json.Marshal(testCase.comments)
					if err != nil {
						require.NoError(t, err, "Testing setup failed: failed to marshal comments")
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, err = w.Write(jsonComments)
					require.NoError(t, err, "Testing setup failed: failed to write out comments")
				} else {
					http.NotFound(w, r)
				}
			})

			server := httptest.NewServer(handler)

			testContext.Login.URL = server.URL
			testCase.issue.HTMLURL = fmt.Sprintf("%s/%s/%s/issues/%d/", testContext.Login.URL, testOwner, testRepo, testCase.issue.Index)

			var outBuffer bytes.Buffer
			testContext.Writer = &outBuffer
			var errBuffer bytes.Buffer
			testContext.ErrWriter = &errBuffer

			if testCase.flagComments {
				_ = testContext.Command.Set("comments", "true")
			} else {
				_ = testContext.Command.Set("comments", "false")
			}

			err := runIssueDetailAsJSON(&testContext, &testCase.issue)

			server.Close()

			require.NoError(t, err, "Failed to run issue detail as JSON")

			out := outBuffer.String()

			require.NotEmpty(t, out, "Unexpected empty output from runIssueDetailAsJSON")

			//setting expectations

			var expectedLabels []labelData
			expectedLabels = []labelData{}
			for _, l := range testCase.issue.Labels {
				expectedLabels = append(expectedLabels, labelData{
					Name:        l.Name,
					Color:       l.Color,
					Description: l.Description,
				})
			}

			var expectedAssignees []string
			expectedAssignees = []string{}
			for _, a := range testCase.issue.Assignees {
				expectedAssignees = append(expectedAssignees, a.UserName)
			}

			var expectedClosedAt *time.Time
			if testCase.issue.Closed != nil {
				expectedClosedAt = testCase.issue.Closed
			}

			var expectedComments []commentData
			expectedComments = []commentData{}
			if testCase.flagComments {
				for _, c := range testCase.comments {
					expectedComments = append(expectedComments, commentData{
						ID:      c.ID,
						Author:  c.Poster.UserName,
						Body:    c.Body,
						Created: c.Created,
					})
				}
			}

			expected := issueData{
				ID:        testCase.issue.ID,
				Index:     testCase.issue.Index,
				Title:     testCase.issue.Title,
				State:     testCase.issue.State,
				Created:   testCase.issue.Created,
				User:      testCase.issue.Poster.UserName,
				Body:      testCase.issue.Body,
				URL:       testCase.issue.HTMLURL,
				ClosedAt:  expectedClosedAt,
				Labels:    expectedLabels,
				Assignees: expectedAssignees,
				Comments:  expectedComments,
			}

			// validating reality
			var actual issueData
			dec := json.NewDecoder(bytes.NewReader(outBuffer.Bytes()))
			dec.DisallowUnknownFields()
			err = dec.Decode(&actual)
			require.NoError(t, err, "Failed to unmarshal output into struct")

			assert.Equal(t, expected, actual, "Expected structs differ from expected one")
		})
	}

}
