// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	repo_model "gitea.dev/models/repo"
	api "gitea.dev/modules/structs"
)

func TestClassifyGitHubMatrix(t *testing.T) {
	siblings := []*actions_model.ActionRunJob{{Name: "test (linux)"}, {Name: "test (windows)"}}
	success := []githubWorkflowJob{
		{Name: "test (linux)", Status: "completed", Conclusion: "success"},
		{Name: "test (windows)", Status: "completed", Conclusion: "success"},
	}
	if got := classifyGitHubMatrix(siblings, success, "completed"); got != githubDedupSkip {
		t.Fatalf("all-success matrix decision = %v, want skip", got)
	}

	tests := []struct {
		name      string
		checks    []githubWorkflowJob
		runStatus string
		want      githubDedupDecision
	}{
		{name: "one matrix shard failed", checks: []githubWorkflowJob{success[0], {Name: "test (windows)", Status: "completed", Conclusion: "failure"}}, runStatus: "completed", want: githubDedupRun},
		{name: "missing completed check", checks: success[:1], runStatus: "completed", want: githubDedupRun},
		{name: "ambiguous job name", checks: []githubWorkflowJob{success[0], success[0], success[1]}, runStatus: "completed", want: githubDedupRun},
		{name: "pending job", checks: []githubWorkflowJob{{Name: "test (linux)", Status: "in_progress"}, success[1]}, runStatus: "in_progress", want: githubDedupWait},
		{name: "failure takes precedence over pending sibling", checks: []githubWorkflowJob{{Name: "test (linux)", Status: "in_progress"}, {Name: "test (windows)", Status: "completed", Conclusion: "failure"}}, runStatus: "in_progress", want: githubDedupRun},
		{name: "not-yet-created job in pending workflow", checks: success[:1], runStatus: "in_progress", want: githubDedupWait},
		{name: "missing skipped workflow", runStatus: "", want: githubDedupRun},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyGitHubMatrix(siblings, test.checks, test.runStatus); got != test.want {
				t.Fatalf("decision = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGitHubDedupPendingTTL(t *testing.T) {
	deduper := newGitHubStatusDeduper("")
	key := githubDedupGroupKey{repoID: 1, runID: 2, attemptID: 3, jobID: "build"}
	if got := deduper.applyPendingTTL(key, githubDedupWait); got != githubDedupWait {
		t.Fatalf("first pending decision = %v, want wait", got)
	}
	deduper.pendingSince[key] = time.Now().Add(-githubDedupPendingTTL - time.Second)
	if got := deduper.applyPendingTTL(key, githubDedupWait); got != githubDedupRun {
		t.Fatalf("expired pending decision = %v, want run", got)
	}
	if _, ok := deduper.pendingSince[key]; ok {
		t.Fatal("expired pending state was not cleared")
	}
}

func TestGitHubMirrorSourceRestrictions(t *testing.T) {
	for name, repo := range map[string]*repo_model.Repository{
		"github mirror": {IsMirror: true, OriginalServiceType: api.GithubService, OriginalURL: "https://github.com/acme/project.git"},
		"not a mirror":  {OriginalServiceType: api.GithubService, OriginalURL: "https://github.com/acme/project.git"},
		"gitea source":  {IsMirror: true, OriginalServiceType: api.GiteaService, OriginalURL: "https://git.example/acme/project.git"},
	} {
		t.Run(name, func(t *testing.T) {
			owner, repoName, ok := githubMirrorSlug(repo)
			if name == "github mirror" {
				if !ok || owner != "acme" || repoName != "project" {
					t.Fatalf("githubMirrorSlug() = %q/%q, %v", owner, repoName, ok)
				}
			} else if ok {
				t.Fatalf("githubMirrorSlug() unexpectedly enabled filtering for %s", name)
			}
		})
	}
	if isGitHubWorkflowDirectory(".gitea/workflows") || !isGitHubWorkflowDirectory(".github/workflows") {
		t.Fatal("workflow directory filtering does not distinguish GitHub from Gitea-only workflows")
	}
	if !isSupportedGitHubDedupEvent("push") || isSupportedGitHubDedupEvent("pull_request") {
		t.Fatal("only push events should be eligible for GitHub deduplication")
	}
}

func TestGitHubCommitMatchRequiresWorkflowAndJobSHA(t *testing.T) {
	job := &actions_model.ActionRunJob{CommitSHA: "same"}
	run := &actions_model.ActionRun{CommitSHA: "same", WorkflowCommitSHA: "same"}
	if !githubCommitMatches(run, job) {
		t.Fatal("matching job and workflow source commits were rejected")
	}
	run.WorkflowCommitSHA = "different-workflow-source"
	if githubCommitMatches(run, job) {
		t.Fatal("workflow source SHA mismatch must fail open")
	}
	run.WorkflowCommitSHA = "same"
	run.CommitSHA = "different-run-commit"
	if githubCommitMatches(run, job) {
		t.Fatal("run SHA mismatch must fail open")
	}
}

func TestGitHubStatusFetchScopesWorkflowAndPaginates(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/project/actions/workflows/ci.yml/runs" && r.URL.Query().Get("page") == "1":
			_, _ = fmt.Fprint(w, `{"total_count":101,"workflow_runs":[{"id":10,"path":".github/workflows/ci.yml","head_sha":"sha","event":"push","status":"completed","run_number":1,"run_attempt":1}]}`)
		case r.URL.Path == "/repos/acme/project/actions/workflows/ci.yml/runs" && r.URL.Query().Get("page") == "2":
			_, _ = fmt.Fprint(w, `{"total_count":101,"workflow_runs":[{"id":11,"path":".github/workflows/ci.yml","head_sha":"sha","event":"push","status":"completed","run_number":2,"run_attempt":1},{"id":99,"path":".github/workflows/other.yml","head_sha":"sha","event":"push","status":"completed","run_number":99,"run_attempt":1}]}`)
		case r.URL.Path == "/repos/acme/project/actions/runs/11/jobs" && r.URL.Query().Get("page") == "1":
			_, _ = fmt.Fprint(w, `{"total_count":1,"jobs":[{"name":"build","status":"completed","conclusion":"success"}]}`)
		default:
			t.Errorf("unexpected GitHub API request: %s?%s", r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	deduper := newGitHubStatusDeduper("")
	deduper.apiBaseURL = server.URL
	entry, err := deduper.loadStatus(context.Background(), "acme", "project", "sha", ".github/workflows/ci.yml", "push")
	if err != nil {
		t.Fatalf("loadStatus() error = %v", err)
	}
	if entry.runStatus != "completed" || len(entry.jobs) != 1 || entry.jobs[0].Conclusion != "success" {
		t.Fatalf("unexpected status snapshot: %#v", entry)
	}
	if len(pages) != 3 || !strings.Contains(pages[0], "page=1") || !strings.Contains(pages[1], "page=2") || !strings.Contains(pages[2], "/runs/11/jobs") {
		t.Fatalf("unexpected pagination/job requests: %v", pages)
	}
}

func TestGitHubStatusAPIErrorIsFailOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limit", http.StatusForbidden)
	}))
	defer server.Close()
	deduper := newGitHubStatusDeduper("")
	deduper.apiBaseURL = server.URL
	if _, err := deduper.loadStatus(context.Background(), "acme", "project", "sha", ".github/workflows/ci.yml", "push"); err == nil {
		t.Fatal("loadStatus() succeeded despite API rate limit")
	} else if got := githubDedupDecisionForLookup(githubDedupSkip, err); got != githubDedupRun {
		t.Fatalf("API error decision = %v, want fail-open run", got)
	}
}
