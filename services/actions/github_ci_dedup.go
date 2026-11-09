// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

type githubWorkflowRun struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	HeadSHA    string `json:"head_sha"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RunNumber  int64  `json:"run_number"`
	RunAttempt int64  `json:"run_attempt"`
}

type githubWorkflowRunsResponse struct {
	TotalCount   int                 `json:"total_count"`
	WorkflowRuns []githubWorkflowRun `json:"workflow_runs"`
}

type githubWorkflowJob struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type githubWorkflowJobsResponse struct {
	TotalCount int                 `json:"total_count"`
	Jobs       []githubWorkflowJob `json:"jobs"`
}

type githubDedupDecision uint8

const (
	githubDedupRun githubDedupDecision = iota
	githubDedupWait
	githubDedupSkip
)

var errGitHubDedupSiblingChanged = errors.New("a Gitea matrix sibling changed before duplicate filtering")

type githubStatusCacheEntry struct {
	jobs      []githubWorkflowJob
	runStatus string
	expiresAt time.Time
	err       error
}

type githubDedupGroupKey struct {
	repoID      int64
	runID       int64
	attemptID   int64
	parentJobID int64
	jobID       string
}

func githubDedupKey(job *actions_model.ActionRunJob) githubDedupGroupKey {
	return githubDedupGroupKey{repoID: job.RepoID, runID: job.RunID, attemptID: job.RunAttemptID, parentJobID: job.ParentJobID, jobID: job.JobID}
}

type githubStatusDeduper struct {
	mu           sync.Mutex
	client       *http.Client
	apiBaseURL   string
	token        string
	cache        map[string]githubStatusCacheEntry
	pendingSince map[githubDedupGroupKey]time.Time
}

const (
	githubStatusCacheTTL  = 30 * time.Second
	githubDedupPendingTTL = 2 * time.Minute
	githubAPIMaxPages     = 20
)

func newGitHubStatusDeduper(token string) *githubStatusDeduper {
	return &githubStatusDeduper{
		client:       &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		apiBaseURL:   "https://api.github.com",
		token:        token,
		cache:        make(map[string]githubStatusCacheEntry),
		pendingSince: make(map[githubDedupGroupKey]time.Time),
	}
}

func githubMirrorSlug(repo *repo_model.Repository) (string, string, bool) {
	if repo == nil || !repo.IsMirror || repo.OriginalServiceType != api.GithubService || repo.OriginalURL == "" {
		return "", "", false
	}
	u, err := url.Parse(repo.OriginalURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (d *githubStatusDeduper) request(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.apiBaseURL, "/")+target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Gitea-GitHub-CI-deduper")
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	res, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("GitHub Actions API returned HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8*1024*1024)).Decode(out); err != nil {
		return err
	}
	return nil
}

func githubPageURL(endpoint string, page int) string {
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%sper_page=100&page=%d", endpoint, separator, page)
}

func (d *githubStatusDeduper) loadStatus(ctx context.Context, owner, name, sha, workflow, event string) (githubStatusCacheEntry, error) {
	cacheKey := owner + "/" + name + "/" + sha + "/" + workflow + "/" + event
	d.mu.Lock()
	if entry, ok := d.cache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		d.mu.Unlock()
		return entry, entry.err
	}
	// The BB executor reconciliation is protected by a shared global lock, so a
	// single mutex around this bounded fetch also coalesces all jobs for a SHA.
	defer d.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	jobs, runStatus, err := d.fetchStatus(ctx, owner, name, sha, workflow, event)
	entry := githubStatusCacheEntry{jobs: jobs, runStatus: runStatus, err: err, expiresAt: time.Now().Add(githubStatusCacheTTL)}
	if len(d.cache) >= 512 {
		now := time.Now()
		for key, cached := range d.cache {
			if now.After(cached.expiresAt) {
				delete(d.cache, key)
			}
		}
		for len(d.cache) >= 512 {
			var oldestKey string
			var oldest time.Time
			for key, cached := range d.cache {
				if oldestKey == "" || cached.expiresAt.Before(oldest) {
					oldestKey, oldest = key, cached.expiresAt
				}
			}
			delete(d.cache, oldestKey)
		}
	}
	d.cache[cacheKey] = entry
	return entry, err
}

func (d *githubStatusDeduper) fetchStatus(ctx context.Context, owner, name, sha, workflow, event string) ([]githubWorkflowJob, string, error) {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	workflowName := path.Base(workflow)
	endpoint := base + "/actions/workflows/" + url.PathEscape(workflowName) + "/runs?head_sha=" + url.QueryEscape(sha)
	var latest *githubWorkflowRun
	for page := 1; page <= githubAPIMaxPages; page++ {
		var response githubWorkflowRunsResponse
		if err := d.request(ctx, githubPageURL(endpoint, page), &response); err != nil {
			return nil, "", err
		}
		for _, run := range response.WorkflowRuns {
			if run.HeadSHA != sha || run.Path != workflow || run.Event != event {
				continue
			}
			if latest == nil || run.RunNumber > latest.RunNumber || (run.RunNumber == latest.RunNumber && run.RunAttempt > latest.RunAttempt) {
				candidate := run
				latest = &candidate
			}
		}
		if page*100 >= response.TotalCount {
			break
		}
		if page == githubAPIMaxPages {
			return nil, "", errors.New("GitHub Actions API pagination exceeded safety limit")
		}
	}
	if latest == nil {
		return nil, "", nil
	}
	jobs := make([]githubWorkflowJob, 0)
	for page := 1; page <= githubAPIMaxPages; page++ {
		var response githubWorkflowJobsResponse
		if err := d.request(ctx, githubPageURL(fmt.Sprintf("%s/actions/runs/%d/jobs", base, latest.ID), page), &response); err != nil {
			return nil, "", err
		}
		jobs = append(jobs, response.Jobs...)
		if page*100 >= response.TotalCount {
			break
		}
		if page == githubAPIMaxPages {
			return nil, "", errors.New("GitHub Actions job pagination exceeded safety limit")
		}
	}
	return jobs, latest.Status, nil
}

func githubWorkflowPath(ctx context.Context, repo *repo_model.Repository, run *actions_model.ActionRun) (string, bool) {
	if run == nil || run.IsScopedRun || run.WorkflowRepoID != repo.ID || run.WorkflowCommitSHA == "" || run.WorkflowID == "" {
		return "", false
	}
	gitRepo, err := git.OpenRepository(ctx, repo)
	if err != nil {
		return "", false
	}
	defer gitRepo.Close()
	commit, err := gitRepo.GetCommit(ctx, run.WorkflowCommitSHA)
	if err != nil {
		return "", false
	}
	for _, dir := range setting.Actions.WorkflowDirs {
		_, err := commit.SubTree(ctx, gitRepo, dir)
		if err != nil {
			if git.IsErrNotExist(err) {
				continue
			}
			return "", false
		}
		if !isGitHubWorkflowDirectory(dir) {
			return "", false // Gitea-only workflow directory
		}
		workflowPath := path.Join(dir, run.WorkflowID)
		if _, err := commit.GetTreeEntryByPath(ctx, gitRepo, workflowPath); err != nil {
			return "", false
		}
		return workflowPath, true
	}
	return "", false
}

func isGitHubWorkflowDirectory(dir string) bool {
	return path.Clean(dir) == ".github/workflows"
}

func isSupportedGitHubDedupEvent(event string) bool {
	return strings.EqualFold(event, "push")
}

func githubCommitMatches(run *actions_model.ActionRun, job *actions_model.ActionRunJob) bool {
	return run != nil && job != nil && job.CommitSHA != "" && job.CommitSHA == run.CommitSHA && job.CommitSHA == run.WorkflowCommitSHA
}

func (d *githubStatusDeduper) decide(ctx context.Context, repo *repo_model.Repository, run *actions_model.ActionRun, job *actions_model.ActionRunJob) (githubDedupDecision, []*actions_model.ActionRunJob, githubDedupGroupKey, error) {
	groupKey := githubDedupKey(job)
	owner, name, ok := githubMirrorSlug(repo)
	if !ok || !githubCommitMatches(run, job) || job.IsReusableCaller || job.ParentJobID != 0 || (repo.IsPrivate && d.token == "") {
		return githubDedupRun, nil, groupKey, nil
	}
	workflow, ok := githubWorkflowPath(ctx, repo, run)
	if !ok || !isSupportedGitHubDedupEvent(run.TriggerEvent) {
		return githubDedupRun, nil, groupKey, nil
	}
	allJobs, err := actions_model.GetRunJobsByRunAndAttemptID(ctx, run.ID, job.RunAttemptID)
	if err != nil {
		return githubDedupRun, nil, groupKey, err
	}
	siblings := make([]*actions_model.ActionRunJob, 0, 4)
	seenNames := make(map[string]bool)
	for _, sibling := range allJobs {
		if sibling.JobID != job.JobID || sibling.ParentJobID != job.ParentJobID {
			continue
		}
		if sibling.Status != actions_model.StatusWaiting || sibling.TaskID != 0 || sibling.IsReusableCaller || seenNames[sibling.Name] {
			return githubDedupRun, nil, groupKey, nil
		}
		seenNames[sibling.Name] = true
		siblings = append(siblings, sibling)
	}
	if len(siblings) == 0 {
		return githubDedupRun, nil, groupKey, nil
	}
	statuses, err := d.loadStatus(ctx, owner, name, job.CommitSHA, workflow, run.TriggerEvent)
	if err != nil {
		return githubDedupRun, nil, groupKey, err
	}
	decision := classifyGitHubMatrix(siblings, statuses.jobs, statuses.runStatus)
	decision = d.applyPendingTTL(groupKey, decision)
	if decision == githubDedupSkip {
		return decision, siblings, groupKey, nil
	}
	return decision, nil, groupKey, nil
}

func githubDedupDecisionForLookup(decision githubDedupDecision, err error) githubDedupDecision {
	if err != nil {
		return githubDedupRun
	}
	return decision
}

func classifyGitHubMatrix(siblings []*actions_model.ActionRunJob, checks []githubWorkflowJob, runStatus string) githubDedupDecision {
	if len(siblings) == 0 {
		return githubDedupRun
	}
	pendingRun := runStatus == "queued" || runStatus == "in_progress" || runStatus == "waiting" || runStatus == "pending" || runStatus == "requested"
	seen := make(map[string]bool, len(siblings))
	pending := false
	for _, sibling := range siblings {
		if sibling.Name == "" || seen[sibling.Name] {
			return githubDedupRun
		}
		seen[sibling.Name] = true
		count := 0
		var matching githubWorkflowJob
		for _, check := range checks {
			if check.Name == sibling.Name {
				count++
				matching = check
			}
		}
		if count > 1 {
			return githubDedupRun
		}
		if count == 0 {
			if pendingRun {
				pending = true
				continue
			}
			return githubDedupRun
		}
		if matching.Status == "queued" || matching.Status == "in_progress" || matching.Status == "waiting" || matching.Status == "pending" || matching.Status == "requested" {
			pending = true
			continue
		}
		if matching.Status != "completed" || matching.Conclusion != "success" {
			return githubDedupRun
		}
	}
	if pending {
		return githubDedupWait
	}
	return githubDedupSkip
}

func (d *githubStatusDeduper) applyPendingTTL(key githubDedupGroupKey, decision githubDedupDecision) githubDedupDecision {
	d.mu.Lock()
	defer d.mu.Unlock()
	if decision != githubDedupWait {
		delete(d.pendingSince, key)
		return decision
	}
	firstSeen, exists := d.pendingSince[key]
	if !exists {
		d.pendingSince[key] = time.Now()
		return githubDedupWait
	}
	if time.Since(firstSeen) >= githubDedupPendingTTL {
		delete(d.pendingSince, key)
		return githubDedupRun
	}
	return githubDedupWait
}

func (d *githubStatusDeduper) clearPending(key githubDedupGroupKey) {
	d.mu.Lock()
	delete(d.pendingSince, key)
	d.mu.Unlock()
}

func markGitHubDuplicateJobsSkipped(ctx context.Context, jobs []*actions_model.ActionRunJob) error {
	now := timeutil.TimeStampNow()
	var changed []*actions_model.ActionRunJob
	err := db.WithTx(ctx, func(ctx context.Context) error {
		for _, job := range jobs {
			previous := job.Status
			job.Status, job.Stopped = actions_model.StatusSkipped, now
			count, err := actions_model.UpdateRunJob(ctx, job, builder.Eq{"status": previous, "task_id": 0}, "status", "stopped")
			if err != nil {
				return err
			}
			if count != 1 {
				return errGitHubDedupSiblingChanged
			}
			changed = append(changed, job)
		}
		return nil
	})
	if errors.Is(err, errGitHubDedupSiblingChanged) {
		return nil // a runner claimed a sibling; the next reconciliation will fail open for this group
	}
	if err != nil {
		return err
	}
	if len(changed) > 0 {
		notifyAndEmitFinishedJobs(ctx, changed)
	}
	return nil
}
