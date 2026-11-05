// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"cmp"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// dryRunStatuses lists the write endpoints of the Gitea API whose success is not the usual answer
// of a withheld write (201 for POST, 200 for PUT and PATCH, 204 for DELETE), as the swagger document
// of Gitea 29 gives it. The SDK refuses another status than the one an endpoint answers with, so a
// dry run has to answer each one right.
var dryRunStatuses = []struct {
	method, path string
	status       int
}{
	{http.MethodPost, "/admin/actions/runners/registration-token", 200},
	{http.MethodPost, "/admin/cron/{task}", 204},
	{http.MethodPost, "/admin/unadopted/{owner}/{repo}", 204},
	{http.MethodPost, "/admin/users/{username}/badges", 204},
	{http.MethodPost, "/admin/users/{username}/rename", 204},
	{http.MethodPost, "/markdown", 200},
	{http.MethodPost, "/markdown/raw", 200},
	{http.MethodPost, "/markup", 200},
	{http.MethodPut, "/notifications", 205},
	{http.MethodPatch, "/notifications/threads/{id}", 205},
	{http.MethodPost, "/orgs/{org}/actions/runners/registration-token", 200},
	{http.MethodPut, "/orgs/{org}/actions/secrets/{secretname}", 201},
	{http.MethodDelete, "/orgs/{org}/actions/variables/{variablename}", 200},
	{http.MethodPut, "/orgs/{org}/actions/variables/{variablename}", 201},
	{http.MethodPost, "/orgs/{org}/avatar", 204},
	{http.MethodPut, "/orgs/{org}/blocks/{username}", 204},
	{http.MethodPost, "/orgs/{org}/projects/{id}/columns/move", 204},
	{http.MethodPost, "/orgs/{org}/projects/{id}/columns/{column_id}/default", 204},
	{http.MethodPost, "/orgs/{org}/projects/{id}/issues/{issue_id}/move", 204},
	{http.MethodPut, "/orgs/{org}/public_members/{username}", 204},
	{http.MethodPost, "/orgs/{org}/rename", 204},
	{http.MethodDelete, "/orgs/{org}/repos", 202},
	{http.MethodPost, "/repos/{owner}/{repo}/actions/runners/registration-token", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/actions/runs/{run}/approve", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/actions/runs/{run}/cancel", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/actions/runs/{run}/force-cancel", 200},
	{http.MethodPut, "/repos/{owner}/{repo}/actions/secrets/{secretname}", 201},
	{http.MethodDelete, "/repos/{owner}/{repo}/actions/variables/{variablename}", 200},
	{http.MethodPut, "/repos/{owner}/{repo}/actions/variables/{variablename}", 201},
	{http.MethodPut, "/repos/{owner}/{repo}/actions/workflows/{workflow_id}/disable", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/actions/workflows/{workflow_id}/dispatches", 200},
	{http.MethodPut, "/repos/{owner}/{repo}/actions/workflows/{workflow_id}/enable", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/avatar", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/branch_protections/priority", 204},
	{http.MethodPatch, "/repos/{owner}/{repo}/branches/{branch}", 204},
	{http.MethodPut, "/repos/{owner}/{repo}/branches/{branch}", 204},
	{http.MethodPut, "/repos/{owner}/{repo}/collaborators/{collaborator}", 204},
	{http.MethodDelete, "/repos/{owner}/{repo}/contents/{filepath}", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/diffpatch", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/file-contents", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/forks", 202},
	{http.MethodPost, "/repos/{owner}/{repo}/hooks/{id}/tests", 204},
	{http.MethodPatch, "/repos/{owner}/{repo}/issues/comments/{id}/assets/{attachment_id}", 201},
	{http.MethodPost, "/repos/{owner}/{repo}/issues/comments/{id}/reactions", 200},
	{http.MethodPatch, "/repos/{owner}/{repo}/issues/{index}", 201},
	{http.MethodPatch, "/repos/{owner}/{repo}/issues/{index}/assets/{attachment_id}", 201},
	{http.MethodDelete, "/repos/{owner}/{repo}/issues/{index}/assignees", 200},
	{http.MethodDelete, "/repos/{owner}/{repo}/issues/{index}/blocks", 200},
	{http.MethodDelete, "/repos/{owner}/{repo}/issues/{index}/dependencies", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/issues/{index}/labels", 200},
	{http.MethodPut, "/repos/{owner}/{repo}/issues/{index}/lock", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/issues/{index}/pin", 204},
	{http.MethodPatch, "/repos/{owner}/{repo}/issues/{index}/pin/{position}", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/issues/{index}/reactions", 200},
	{http.MethodDelete, "/repos/{owner}/{repo}/issues/{index}/subscriptions/{user}", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/issues/{index}/times", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/merge-upstream", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/mirror-sync", 200},
	{http.MethodPut, "/repos/{owner}/{repo}/notifications", 205},
	{http.MethodPost, "/repos/{owner}/{repo}/projects/{id}/columns/move", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/projects/{id}/columns/{column_id}/default", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/projects/{id}/issues/{issue_id}/move", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/comments/{id}/resolve", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/comments/{id}/unresolve", 204},
	{http.MethodPatch, "/repos/{owner}/{repo}/pulls/{index}", 201},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/{index}/merge", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/{index}/reviews", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/{index}/reviews/{id}", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/{index}/reviews/{id}/dismissals", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/{index}/reviews/{id}/undismissals", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/pulls/{index}/update", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/push_mirrors", 200},
	{http.MethodPost, "/repos/{owner}/{repo}/push_mirrors-sync", 200},
	{http.MethodPatch, "/repos/{owner}/{repo}/releases/{id}/assets/{attachment_id}", 201},
	{http.MethodPost, "/repos/{owner}/{repo}/tags", 200},
	{http.MethodPut, "/repos/{owner}/{repo}/teams/{team}", 204},
	{http.MethodPut, "/repos/{owner}/{repo}/topics", 204},
	{http.MethodPut, "/repos/{owner}/{repo}/topics/{topic}", 204},
	{http.MethodPost, "/repos/{owner}/{repo}/transfer", 202},
	{http.MethodPost, "/repos/{owner}/{repo}/transfer/accept", 202},
	{http.MethodPost, "/repos/{owner}/{repo}/transfer/reject", 200},
	{http.MethodPut, "/teams/{id}/members/{username}", 204},
	{http.MethodPut, "/teams/{id}/repos/{org}/{repo}", 204},
	{http.MethodPost, "/user/actions/runners/registration-token", 200},
	{http.MethodPut, "/user/actions/secrets/{secretname}", 201},
	{http.MethodDelete, "/user/actions/variables/{variablename}", 201},
	{http.MethodPut, "/user/actions/variables/{variablename}", 201},
	{http.MethodPost, "/user/avatar", 204},
	{http.MethodPut, "/user/blocks/{username}", 204},
	{http.MethodPut, "/user/following/{username}", 204},
	{http.MethodPost, "/user/projects/{id}/columns/move", 204},
	{http.MethodPost, "/user/projects/{id}/columns/{column_id}/default", 204},
	{http.MethodPost, "/user/projects/{id}/issues/{issue_id}/move", 204},
	{http.MethodPut, "/user/starred/{owner}/{repo}", 204},
}

type dryRunStatus struct {
	method  string
	pattern *regexp.Regexp
	status  int
}

// dryRunPatterns turns the paths of dryRunStatuses into patterns, those with the most fixed segments
// first, so that a fixed segment wins over a parameter.
var dryRunPatterns = sync.OnceValue(func() []dryRunStatus {
	patterns := make([]dryRunStatus, 0, len(dryRunStatuses))
	for _, entry := range dryRunStatuses {
		segments := strings.Split(entry.path, "/")
		for i, segment := range segments {
			if strings.HasPrefix(segment, "{") {
				segments[i] = "[^/]+"
			} else {
				segments[i] = regexp.QuoteMeta(segment)
			}
		}
		patterns = append(patterns, dryRunStatus{entry.method, regexp.MustCompile("^" + strings.Join(segments, "/") + "$"), entry.status})
	}
	params := func(p dryRunStatus) int { return strings.Count(p.pattern.String(), "[^/]+") }
	slices.SortStableFunc(patterns, func(a, b dryRunStatus) int { return cmp.Compare(params(a), params(b)) })
	return patterns
})

// apiPath is path without what comes before the API's own paths: an instance can be served from a
// sub-path.
func apiPath(path string) string {
	if _, after, ok := strings.Cut(path, "/api/v1/"); ok {
		return "/" + after
	}
	return path
}

// successStatus is the status that Gitea answers a write with when it succeeds: the usual one of
// the method unless dryRunStatuses names the endpoint.
func successStatus(method, path string) int {
	path = apiPath(path)
	for _, pattern := range dryRunPatterns() {
		if pattern.method == method && pattern.pattern.MatchString(path) {
			return pattern.status
		}
	}
	switch method {
	case http.MethodPost:
		return http.StatusCreated
	case http.MethodDelete:
		return http.StatusNoContent
	}
	return http.StatusOK
}
