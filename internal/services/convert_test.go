// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"encoding/json"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func decode[T any](t *testing.T, data string) *T {
	t.Helper()
	value := new(T)
	require.NoError(t, json.Unmarshal([]byte(data), value))
	return value
}

func TestIssueFromMapsServerFields(t *testing.T) {
	issue := decode[gitea.Issue](t, `{
		"id":4,"number":2,"title":"Bug","body":"Details","ref":"main","state":"open","comments":3,
		"html_url":"https://gitea.test/alice/project/issues/2","user":{"login":"alice"},
		"labels":[{"id":7,"name":"kind/bug","color":"ee0701","description":"Broken","exclusive":true,"is_archived":true}],
		"assignees":[{"login":"bob"},{"login":"carol"}],"milestone":{"id":5,"title":"v1"},
		"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T12:00:00+02:00","due_date":"2026-11-01T00:00:00Z"}`)
	deadline := "2026-11-01T00:00:00Z"
	require.Equal(t, Issue{
		Author: "alice", Labels: []Label{{ID: 7, Name: "kind/bug", Color: "ee0701", Description: "Broken", Exclusive: true, Archived: true}},
		Ref: "main", ID: 4, Number: 2, Title: "Bug", Body: "Details", URL: "https://gitea.test/alice/project/issues/2", State: "open",
		CreatedAt: "2026-10-01T12:00:00Z", UpdatedAt: "2026-10-02T12:00:00+02:00", Deadline: &deadline,
		Assignees: []string{"bob", "carol"}, Milestone: &IssueMilestone{ID: 5, Title: "v1"}, Comments: 3,
	}, issueFrom(issue))
}

func TestRepositoryFromMapsParents(t *testing.T) {
	repo := decode[gitea.Repository](t, `{
		"id":2,"name":"fork","full_name":"bob/fork","description":"Copy","html_url":"https://gitea.test/bob/fork",
		"private":true,"default_branch":"main","clone_url":"https://gitea.test/bob/fork.git","ssh_url":"git@gitea.test:bob/fork.git",
		"archived":true,"template":true,"fork":true,"mirror":true,"website":"https://example.test","has_actions":true,
		"original_url":"https://source.test/repo","parent":{"id":1,"name":"project","full_name":"alice/project"}}`)
	require.Equal(t, Repository{
		ActionsEnabled: true, Website: "https://example.test", ID: 2, Name: "fork", FullName: "bob/fork", Description: "Copy",
		URL: "https://gitea.test/bob/fork", Private: true, DefaultBranch: "main", CloneURL: "https://gitea.test/bob/fork.git",
		SSHURL: "git@gitea.test:bob/fork.git", Archived: true, Template: true, Fork: true, Mirror: true,
		Parent: &Repository{ID: 1, Name: "project", FullName: "alice/project"}, OriginalURL: "https://source.test/repo",
	}, repositoryFrom(repo))
}

func TestOrganizationAndMemberFrom(t *testing.T) {
	org := decode[gitea.Organization](t, `{"id":3,"username":"team","full_name":"Team","description":"About","website":"https://team.test","location":"Earth","visibility":"limited","repo_admin_change_team_access":true}`)
	require.Equal(t, Organization{ID: 3, Name: "team", FullName: "Team", Description: "About", Website: "https://team.test", Location: "Earth", Visibility: "limited", RepoAdminChangeTeamAccess: true}, organizationFrom(org))
	org.Name = "renamed"
	require.Equal(t, "renamed", organizationFrom(org).Name)
	member := decode[gitea.User](t, `{"id":9,"login":"alice","full_name":"Alice","email":"private@example.test"}`)
	require.Equal(t, OrganizationMember{ID: 9, Username: "alice", FullName: "Alice"}, memberFrom(member))
}

func TestAssetFromUsesBrowserDownloadURL(t *testing.T) {
	attachment := decode[gitea.Attachment](t, `{"id":5,"name":"file.bin","size":12,"download_count":2,"created_at":"2026-10-01T12:00:00Z","uuid":"abc","browser_download_url":"https://gitea.test/attachments/abc"}`)
	require.Equal(t, ReleaseAsset{ID: 5, Name: "file.bin", Size: 12, URL: "https://gitea.test/attachments/abc", CreatedAt: "2026-10-01T12:00:00Z", DownloadCount: 2}, assetFrom(attachment))
}

func TestConvertAllSkipsAbsentItems(t *testing.T) {
	require.Equal(t, []string{}, convertAll(nil, login))
	require.Equal(t, []string{"alice"}, convertAll([]*gitea.User{{UserName: "alice"}, nil}, login))
	require.Equal(t, []OrganizationMember{{ID: 1, Username: "alice"}}, convertAll([]*gitea.User{nil, {ID: 1, UserName: "alice"}}, memberFrom))
}
