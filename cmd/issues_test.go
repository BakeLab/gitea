// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
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

type fakeIssueCommentClient struct {
	owner    string
	repo     string
	index    int64
	comments []*gitea.Comment
}

func (f *fakeIssueCommentClient) ListIssueComments(owner, repo string, index int64, _ gitea.ListIssueCommentOptions) ([]*gitea.Comment, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.index = index
	return f.comments, nil, nil
}

type fakeIssueDetailClient struct {
	owner     string
	repo      string
	index     int64
	issue     *gitea.Issue
	reactions []*gitea.Reaction
}

func (f *fakeIssueDetailClient) GetIssue(owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.index = index
	return f.issue, nil, nil
}

func (f *fakeIssueDetailClient) GetIssueReactions(owner, repo string, index int64) ([]*gitea.Reaction, *gitea.Response, error) {
	f.owner = owner
	f.repo = repo
	f.index = index
	return f.reactions, nil, nil
}

func toCommentPointers(comments []gitea.Comment) []*gitea.Comment {
	result := make([]*gitea.Comment, 0, len(comments))
	for i := range comments {
		comment := comments[i]
		result = append(result, &comment)
	}
	return result
}

func createTestIssue(comments int, isClosed bool) gitea.Issue {
	issue := gitea.Issue{
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
		Closed:  nil, // 2025-11-10T21:20:19Z
	}

	if isClosed {
		closed := time.Date(2025, 11, 10, 21, 20, 19, 0, time.UTC)
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
			client := &fakeIssueCommentClient{
				comments: toCommentPointers(testCase.comments),
			}

			testContext.Login.URL = "https://gitea.example.com"
			testCase.issue.HTMLURL = fmt.Sprintf("%s/%s/%s/issues/%d/", testContext.Login.URL, testOwner, testRepo, testCase.issue.Index)

			var outBuffer bytes.Buffer
			testContext.Writer = &outBuffer
			var errBuffer bytes.Buffer
			testContext.ErrWriter = &errBuffer

			if testCase.flagComments {
				require.NoError(t, testContext.Set("comments", "true"))
			} else {
				require.NoError(t, testContext.Set("comments", "false"))
			}

			err := runIssueDetailAsJSONWithClient(&testContext, &testCase.issue, client)

			require.NoError(t, err, "Failed to run issue detail as JSON")
			if testCase.flagComments {
				assert.Equal(t, testOwner, client.owner)
				assert.Equal(t, testRepo, client.repo)
				assert.Equal(t, testCase.issue.Index, client.index)
			}

			out := outBuffer.String()

			require.NotEmpty(t, out, "Unexpected empty output from runIssueDetailAsJSON")

			// setting expectations

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

func TestRunIssueDetailUsesOwnerFlag(t *testing.T) {
	issueIndex := int64(12)
	expectedOwner := "overrideOwner"
	expectedRepo := "overrideRepo"
	issue := &gitea.Issue{
		ID:      99,
		Index:   issueIndex,
		Title:   "Owner override test",
		State:   gitea.StateOpen,
		Created: time.Date(2025, 11, 1, 10, 0, 0, 0, time.UTC),
		Poster: &gitea.User{
			UserName: "tester",
		},
		HTMLURL: "https://example.test/issues/12",
	}

	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "testLogin",
			URL:     "https://gitea.example.com",
			Token:   "token",
			User:    "loginUser",
			Default: true,
		}},
	})

	cmd := cli.Command{
		Name: "issues",
		Flags: []cli.Flag{
			&flags.LoginFlag,
			&flags.RepoFlag,
			&flags.RemoteFlag,
			&flags.OutputFlag,
			&cli.StringFlag{Name: "owner"},
			&cli.BoolFlag{Name: "comments"},
		},
	}
	var outBuffer bytes.Buffer
	var errBuffer bytes.Buffer
	cmd.Writer = &outBuffer
	cmd.ErrWriter = &errBuffer
	require.NoError(t, cmd.Set("login", "testLogin"))
	require.NoError(t, cmd.Set("repo", expectedRepo))
	require.NoError(t, cmd.Set("owner", expectedOwner))
	require.NoError(t, cmd.Set("comments", "false"))

	teaCtx, idx, err := resolveIssueDetailContext(&cmd, fmt.Sprintf("%d", issueIndex))
	require.NoError(t, err)

	client := &fakeIssueDetailClient{
		issue:     issue,
		reactions: []*gitea.Reaction{},
	}

	err = runIssueDetailWithClient(teaCtx, idx, client)
	require.NoError(t, err, "Expected runIssueDetail to succeed")
	assert.Equal(t, expectedOwner, client.owner)
	assert.Equal(t, expectedRepo, client.repo)
	assert.Equal(t, issueIndex, client.index)
}
