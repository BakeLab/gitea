// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"uuid"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/globallock"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"xorm.io/builder"
)

type bbExecutorConfig struct {
	Endpoint          string                        `json:"endpoint"`
	Project           string                        `json:"project"`
	TokenFile         string                        `json:"token_file"`
	TokenURL          string                        `json:"token_url"`
	ClientID          string                        `json:"client_id"`
	ClientSecretFile  string                        `json:"client_secret_file"`
	Resource          string                        `json:"resource"`
	Scopes            []string                      `json:"scopes"`
	ProfileAuth       *bbExecutorProfileAuth        `json:"profile_auth,omitempty"`
	MaxConcurrent     int                           `json:"max_concurrent"`
	AutoConcurrency   bool                          `json:"auto_concurrency,omitempty"`
	GitHubTokenFile   string                        `json:"github_token_file,omitempty"`
	MaxDurationS      int                           `json:"max_duration_s"`
	Profiles          []bbExecutorProfile           `json:"profiles"`
	UnsupportedLabels []bbExecutorUnsupportedLabels `json:"unsupported_labels,omitempty"`
	tokens            oauth2.TokenSource
	client            *http.Client
	githubDeduper     *githubStatusDeduper
	reconcileState    *bbExecutorReconcileState
}

type bbExecutorReconcileState struct {
	mu                   sync.Mutex
	lastUnmatchedSummary string
}

func (state *bbExecutorReconcileState) updateUnmatchedSummary(summary string) (changed, previouslyUnmatched bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lastUnmatchedSummary == summary {
		return false, state.lastUnmatchedSummary != ""
	}
	previouslyUnmatched = state.lastUnmatchedSummary != ""
	state.lastUnmatchedSummary = summary
	return true, previouslyUnmatched
}

type bbExecutorProfileAuth struct {
	Issuer    string   `json:"issuer"`
	ClientID  string   `json:"client_id"`
	Resource  string   `json:"resource"`
	Scopes    []string `json:"scopes"`
	TokenFile string   `json:"token_file"`
}

type bbExecutorProfile struct {
	Platform string `json:"platform"`
	ExecUser string `json:"exec_user"`
	Name     string `json:"name"`
	Extends  string `json:"extends,omitempty"`
	// Repositories optionally limits this profile to exact owner/repository names.
	Repositories []string       `json:"repositories,omitempty"`
	Labels       []string       `json:"labels"`
	RunnerURL    string         `json:"runner_url"`
	RunnerSHA256 string         `json:"runner_sha256"`
	Sandbox      map[string]any `json:"sandbox"`
	PackageSets  []string       `json:"package_sets"`
}

type bbCapacityResponse struct {
	Schedulable int   `json:"schedulable"`
	MaxCount    int   `json:"max_count"`
	AsOfMS      int64 `json:"as_of_ms"`
}

type bbTaskResult struct {
	ID    string `json:"task_id"`
	State string `json:"state"`
}

type bbRequestError struct {
	method, path string
	status       int
}

func (err *bbRequestError) Error() string {
	return fmt.Sprintf("BB executor request %s %s returned HTTP %d", err.method, err.path, err.status)
}

func (task bbTaskResult) terminal() bool {
	return slices.Contains([]string{"done", "failed", "cancelled"}, task.State)
}

func bbHTTPS(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("BB executor URLs must use HTTPS without credentials, query or fragment")
	}
	return u, nil
}

func initBBExecutor(ctx context.Context) error {
	if setting.Actions.BBExecutorConfig == "" {
		return nil
	}
	f, err := os.Open(setting.Actions.BBExecutorConfig)
	if err != nil {
		return err
	}
	defer f.Close()
	var config bbExecutorConfig
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&config); err != nil {
		return err
	}
	config.reconcileState = &bbExecutorReconcileState{}
	if _, err := bbHTTPS(config.Endpoint); err != nil {
		return err
	}
	origin, err := bbHTTPS(setting.AppURL)
	if err != nil {
		return err
	}
	if config.Project == "" || config.MaxConcurrent < 1 || config.MaxConcurrent > 100 || config.MaxDurationS < 60 || config.MaxDurationS > 604800 || len(config.Profiles) == 0 {
		return errors.New("BB executor requires project, profiles, max_concurrent (1-100) and max_duration_s (60-604800)")
	}
	if config.Profiles, err = resolveBBProfileExtends(config.Profiles); err != nil {
		return err
	}
	if err := config.validateUnsupportedLabels(); err != nil {
		return err
	}
	var githubToken string
	if config.GitHubTokenFile != "" {
		data, err := readBBSecretFile(config.GitHubTokenFile)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			return errors.New("BB executor GitHub token file is unavailable or empty")
		}
		githubToken = strings.TrimSpace(string(data))
	}
	config.githubDeduper = newGitHubStatusDeduper(githubToken)
	if config.ProfileAuth != nil {
		profile := config.ProfileAuth
		if config.TokenFile != "" || config.TokenURL != "" || config.ClientID != "" || config.ClientSecretFile != "" || config.Resource != "" || len(config.Scopes) != 0 {
			return errors.New("BB executor profile_auth cannot be combined with token_file or OAuth credentials")
		}
		if profile.ClientID == "" || profile.Resource == "" || !filepath.IsAbs(profile.TokenFile) {
			return errors.New("BB executor profile_auth requires client_id, resource and token_file")
		}
		store := &bbProfileTokenStore{path: profile.TokenFile}
		if _, err := store.Load(ctx); err != nil {
			return errors.New("BB executor profile token file is unavailable or invalid")
		}
		source, err := newBBProfileTokenSource(graceful.GetManager().ShutdownContext(), *profile, store)
		if err != nil {
			return errors.New("BB executor profile authentication initialization failed")
		}
		config.client, err = newBBProfileHTTPClient(&http.Client{Timeout: 20 * time.Second}, config.Endpoint, source)
		if err != nil {
			return err
		}
	} else if config.TokenFile == "" {
		if _, err := bbHTTPS(config.TokenURL); err != nil {
			return err
		}
		if config.ClientID == "" || config.ClientSecretFile == "" || config.Resource == "" {
			return errors.New("BB executor OAuth requires client_id, client_secret_file and resource")
		}
		clientSecret, err := readBBSecretFile(config.ClientSecretFile)
		if err != nil || strings.TrimSpace(string(clientSecret)) == "" {
			return errors.New("BB executor client secret file is unavailable or invalid")
		}
		auth := clientcredentials.Config{ClientID: config.ClientID, ClientSecret: strings.TrimSpace(string(clientSecret)), TokenURL: config.TokenURL, Scopes: config.Scopes, EndpointParams: url.Values{"resource": {config.Resource}}, AuthStyle: oauth2.AuthStyleInHeader}
		authCtx := context.WithValue(graceful.GetManager().ShutdownContext(), oauth2.HTTPClient, &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
		config.tokens = auth.TokenSource(authCtx)
	} else if config.TokenURL != "" || config.ClientID != "" || config.ClientSecretFile != "" {
		return errors.New("BB executor config must select token_file or OAuth credentials")
	}
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	for _, profile := range config.Profiles {
		if profile.Name == "" || len(profile.Labels) == 0 || profile.Sandbox == nil {
			return errors.New("BB executor profiles require name, labels and sandbox")
		}
		if err := validateBBProfileRepositories(profile.Repositories); err != nil {
			return fmt.Errorf("BB executor profile %q: %w", profile.Name, err)
		}
		if profile.Platform != "" && profile.Platform != "linux" && profile.Platform != "darwin" {
			return errors.New("BB executor platform must be linux or darwin")
		}
		if profile.Platform == "darwin" {
			if node, _ := profile.Sandbox["node_name"].(string); node == "" {
				return errors.New("BB executor darwin profiles require an explicit macOS node_name")
			}
			for _, label := range profile.Labels {
				if !strings.HasSuffix(label, ":host") {
					return errors.New("BB executor darwin profiles require host runner labels")
				}
			}
			if len(profile.PackageSets) != 0 {
				return errors.New("BB executor darwin profiles use guest image tools, not Linux package sets")
			}
		} else if !slices.Contains(profile.PackageSets, "git-runner-docker") {
			return errors.New("BB executor profiles must prepare git-runner-docker")
		}
		if _, err := bbHTTPS(profile.RunnerURL); err != nil {
			return err
		}
		if len(profile.RunnerSHA256) != 64 || strings.ContainsFunc(profile.RunnerSHA256, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) {
			return errors.New("BB executor requires a lowercase runner_sha256")
		}
		for _, label := range profile.Labels {
			if strings.TrimSpace(strings.SplitN(label, ":", 2)[0]) == "" {
				return errors.New("BB executor labels must have non-empty names")
			}
		}
		if profile.Sandbox["runtime"] != "sandbox-vm" || profile.Sandbox["network"] != "gateway" {
			return errors.New("BB executor requires sandbox-vm gateway profiles")
		}
		if !bbArrayContains(profile.Sandbox["required_internal_services"], origin.Hostname()+":"+port) || !bbArrayContains(profile.Sandbox["allowed_hosts"], origin.Hostname()) {
			return errors.New("BB executor sandbox must require the exact Gitea internal service and allow its hostname")
		}
	}
	active, err := actions_model.ListActiveBBTasks(ctx)
	if err != nil {
		return err
	}
	for _, row := range active {
		if row.Endpoint != strings.TrimRight(config.Endpoint, "/") || row.Project != config.Project {
			return errors.New("drain BB executor tasks before changing endpoint or project")
		}
	}
	go graceful.GetManager().RunWithShutdownContext(func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			_, err := globallock.TryLockAndDo(ctx, "actions_bb_executor", config.reconcile)
			if err != nil && ctx.Err() == nil {
				log.Error("BB executor reconciliation: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return nil
}

// resolveBBProfileExtends flattens "extends" inheritance. Labels are never
// inherited so two profiles cannot claim the same labels by accident.
func resolveBBProfileExtends(profiles []bbExecutorProfile) ([]bbExecutorProfile, error) {
	byName := map[string][]int{}
	for i, profile := range profiles {
		byName[profile.Name] = append(byName[profile.Name], i)
	}
	resolved := make([]bool, len(profiles))
	visiting := make([]bool, len(profiles))
	var resolve func(i int) error
	resolve = func(i int) error {
		if resolved[i] {
			return nil
		}
		child := profiles[i]
		if child.Extends == "" {
			resolved[i] = true
			return nil
		}
		if child.Name == "" || len(byName[child.Name]) != 1 {
			return fmt.Errorf("BB executor profile %q with extends requires a unique name", child.Name)
		}
		if len(child.Labels) == 0 {
			return fmt.Errorf("BB executor profile %q extends %q but must define its own labels", child.Name, child.Extends)
		}
		parents := byName[child.Extends]
		if len(parents) != 1 {
			return fmt.Errorf("BB executor profile %q extends unknown or ambiguous profile %q", child.Name, child.Extends)
		}
		if visiting[i] {
			return fmt.Errorf("BB executor profile %q has a cyclic extends chain", child.Name)
		}
		visiting[i] = true
		defer func() { visiting[i] = false }()
		if err := resolve(parents[0]); err != nil {
			return err
		}
		merged := profiles[parents[0]]
		merged.Name, merged.Extends, merged.Labels = child.Name, child.Extends, child.Labels
		if child.Repositories != nil {
			merged.Repositories = slices.Clone(child.Repositories)
		} else {
			merged.Repositories = slices.Clone(merged.Repositories)
		}
		if child.RunnerURL != "" {
			merged.RunnerURL = child.RunnerURL
		}
		if child.RunnerSHA256 != "" {
			merged.RunnerSHA256 = child.RunnerSHA256
		}
		if child.Platform != "" {
			merged.Platform = child.Platform
		}
		if child.ExecUser != "" {
			merged.ExecUser = child.ExecUser
		}
		if child.PackageSets != nil {
			merged.PackageSets = slices.Clone(child.PackageSets)
		} else {
			merged.PackageSets = slices.Clone(merged.PackageSets)
		}
		merged.Sandbox = bbMergeSandbox(merged.Sandbox, child.Sandbox)
		profiles[i] = merged
		resolved[i] = true
		return nil
	}
	profiles = slices.Clone(profiles)
	for i := range profiles {
		if err := resolve(i); err != nil {
			return nil, err
		}
	}
	return profiles, nil
}

func (config bbExecutorConfig) repositoryNamesForJobs(ctx context.Context, jobs []*actions_model.ActionRunJob) (map[int64]string, error) {
	for _, profile := range config.Profiles {
		if len(profile.Repositories) > 0 {
			ids := make([]int64, 0, len(jobs))
			seen := make(map[int64]bool, len(jobs))
			for _, job := range jobs {
				if !seen[job.RepoID] {
					ids = append(ids, job.RepoID)
					seen[job.RepoID] = true
				}
			}
			repos, err := repo_model.GetRepositoriesMapByIDs(ctx, ids)
			if err != nil {
				return nil, err
			}
			names := make(map[int64]string, len(repos))
			for id, repo := range repos {
				names[id] = repo.FullName()
			}
			return names, nil
		}
	}
	return map[int64]string{}, nil
}

// bbMergeSandbox deep-merges maps by key; every other value, including lists,
// is replaced by the override. The result never aliases its inputs.
func bbMergeSandbox(base, override map[string]any) map[string]any {
	if base == nil && override == nil {
		return nil
	}
	merged := make(map[string]any, len(base)+len(override))
	for key, value := range base {
		merged[key] = bbCloneSandboxValue(value)
	}
	for key, value := range override {
		if overrideMap, ok := value.(map[string]any); ok {
			if baseMap, ok := merged[key].(map[string]any); ok {
				merged[key] = bbMergeSandbox(baseMap, overrideMap)
				continue
			}
		}
		merged[key] = bbCloneSandboxValue(value)
	}
	return merged
}

func bbCloneSandboxValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return bbMergeSandbox(v, nil)
	case []any:
		cloned := make([]any, len(v))
		for i, item := range v {
			cloned[i] = bbCloneSandboxValue(item)
		}
		return cloned
	}
	return value
}

func bbArrayContains(value any, wanted string) bool {
	items, _ := value.([]any)
	return slices.ContainsFunc(items, func(item any) bool { return item == wanted })
}

func (config bbExecutorConfig) request(ctx context.Context, method, path, key string, body, target any) error {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(config.Endpoint, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	if config.ProfileAuth == nil {
		var bearer string
		if config.tokens != nil {
			token, err := config.tokens.Token()
			if err != nil {
				return errors.New("BB executor OAuth token acquisition failed")
			}
			bearer = token.AccessToken
		} else {
			token, err := readBBSecretFile(config.TokenFile)
			if err != nil {
				return err
			}
			bearer = strings.TrimSpace(string(token))
		}
		if bearer == "" {
			return errors.New("BB executor credential is empty")
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("X-Breadboard-Project", config.Project)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := config.client
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &bbRequestError{method: method, path: path, status: res.StatusCode}
	}
	if target != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(target)
	}
	return nil
}

func (config bbExecutorConfig) reconcile(ctx context.Context) error {
	// These workflows target GitHub-only services. Exclude them in Gitea so
	// their GitHub Actions runs remain unchanged.
	if err := config.skipExcludedWorkflows(ctx); err != nil {
		return fmt.Errorf("skip Gitea-excluded workflows: %w", err)
	}
	// Explicitly unsupported label sets must be drained even when BB has no
	// capacity, the global concurrency limit is full, or a capacity query fails.
	// Unknown labels remain queued so a missing profile is observable and safe.
	if err := config.skipConfiguredUnsupportedJobs(ctx); err != nil {
		return fmt.Errorf("skip configured unsupported BB jobs: %w", err)
	}
	rows, err := actions_model.ListActiveBBTasks(ctx)
	if err != nil {
		return err
	}
	var failures []error
	var failureMu sync.Mutex
	var workers sync.WaitGroup
	limit := make(chan struct{}, 8)
	for _, row := range rows {
		limit <- struct{}{}
		workers.Go(func() {
			defer func() { <-limit }()
			if err := config.advance(ctx, row); err != nil {
				failureMu.Lock()
				failures = append(failures, fmt.Errorf("BB task %d: %w", row.ID, err))
				failureMu.Unlock()
			}
		})
	}
	workers.Wait()
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	rows, err = actions_model.ListActiveBBTasks(ctx)
	if err != nil {
		return err
	}
	remaining := config.MaxConcurrent - len(rows)
	unmatchedProfiles := make(map[string]int)
	fullyScanned := true
	profileCapacity := make(map[int]int, len(config.Profiles))
	if config.AutoConcurrency && remaining > 0 {
		// Read capacity for every profile before admitting any new work. If any
		// query fails, fail closed for this reconciliation while leaving active
		// tasks alone (they were advanced above).
		for profileIndex, profile := range config.Profiles {
			capacity, err := config.schedulableCapacity(ctx, profile, config.MaxConcurrent)
			if err != nil {
				return fmt.Errorf("query BB schedulable capacity for profile %q: %w", profile.Name, err)
			}
			profileCapacity[profileIndex] = capacity
		}
	}
	admission := make(map[githubDedupGroupKey]githubDedupDecision)
	groupsSeen := make(map[githubDedupGroupKey]bool)
	runs := make(map[int64]*actions_model.ActionRun)
	repos := make(map[int64]*repo_model.Repository)
	for page := 1; ; page++ {
		jobs, total, err := actions_model.FindJobQueueJobs(ctx, actions_model.JobQueueOptions{Status: actions_model.StatusWaiting}, page, 100)
		if err != nil {
			return err
		}
		repoNames, err := config.repositoryNamesForJobs(ctx, jobs)
		if err != nil {
			return fmt.Errorf("load repositories for BB profile matching: %w", err)
		}
		for _, job := range jobs {
			exists, err := db.GetEngine(ctx).Where("job_id = ?", job.ID).Exist(new(actions_model.ActionBBTask))
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			matchingProfiles := make([]int, 0, len(config.Profiles))
			profileLabels := make(map[int][]string, len(config.Profiles))
			for _, profileIndex := range config.matchingProfiles(job, repoNames[job.RepoID]) {
				profile := config.Profiles[profileIndex]
				labels := bbProfileRunnerLabels(profile)
				matchingProfiles = append(matchingProfiles, profileIndex)
				profileLabels[profileIndex] = labels
			}
			if len(matchingProfiles) == 0 {
				labels, _ := json.Marshal(job.RunsOn)
				unmatchedProfiles[string(labels)]++
				continue
			}
			deferred := false
			for _, profileIndex := range matchingProfiles {
				profile := config.Profiles[profileIndex]
				if remaining <= 0 {
					continue
				}
				if config.AutoConcurrency && profileCapacity[profileIndex] <= 0 {
					continue
				}
				labels := profileLabels[profileIndex]
				if config.githubDeduper != nil {
					groupKey := githubDedupKey(job)
					if groupsSeen[groupKey] {
						deferred = true
						break
					}
					decision, decided := admission[groupKey]
					if !decided {
						run := runs[job.RunID]
						if run == nil {
							run, err = actions_model.GetRunByRepoAndID(ctx, job.RepoID, job.RunID)
							if err != nil {
								log.Warn("BB executor cannot load workflow run for GitHub dedup job %d; allowing Gitea dispatch: %v", job.ID, err)
								decision = githubDedupRun
							} else {
								runs[job.RunID] = run
							}
						}
						repo := repos[job.RepoID]
						if run != nil && repo == nil {
							repo, err = repo_model.GetRepositoryByID(ctx, job.RepoID)
							if err != nil {
								log.Warn("BB executor cannot load repository for GitHub dedup job %d; allowing Gitea dispatch: %v", job.ID, err)
								decision = githubDedupRun
							} else {
								repos[job.RepoID] = repo
							}
						}
						if run != nil && repo != nil {
							var siblings []*actions_model.ActionRunJob
							decision, siblings, _, err = config.githubDeduper.decide(ctx, repo, run, job)
							decision = githubDedupDecisionForLookup(decision, err)
							if err != nil {
								config.githubDeduper.clearPending(groupKey)
								log.Warn("BB executor GitHub status lookup failed for job %d; allowing Gitea dispatch: %v", job.ID, err)
							}
							if decision == githubDedupSkip {
								if err := markGitHubDuplicateJobsSkipped(ctx, siblings); err != nil {
									return err
								}
							}
						}
						admission[groupKey] = decision
					}
					if decision == githubDedupWait || decision == githubDedupSkip {
						groupsSeen[groupKey] = true
						deferred = true
						break
					}
				}
				row, err := config.prepare(ctx, job, profile, labels)
				if err != nil {
					return err
				}
				if err := config.advance(ctx, row); err != nil {
					return err
				}
				remaining--
				if config.AutoConcurrency {
					profileCapacity[profileIndex]--
				}
				break
			}
			if deferred {
				continue
			}
			if remaining == 0 {
				break
			}
		}
		if int64(page*100) >= total {
			break
		}
		if remaining <= 0 {
			fullyScanned = false
			break
		}
	}
	config.reportUnmatchedProfiles(unmatchedProfiles, fullyScanned)
	return nil
}

func (config bbExecutorConfig) reportUnmatchedProfiles(unmatchedProfiles map[string]int, fullyScanned bool) {
	if config.reconcileState == nil {
		return
	}
	labels := make([]string, 0, len(unmatchedProfiles))
	count := 0
	for labelSet, jobs := range unmatchedProfiles {
		labels = append(labels, fmt.Sprintf("%d jobs for %s", jobs, labelSet))
		count += jobs
	}
	slices.Sort(labels)
	summary := strings.Join(labels, "; ")
	if summary == "" && !fullyScanned {
		return
	}
	changed, previouslyUnmatched := config.reconcileState.updateUnmatchedSummary(summary)
	if !changed {
		return
	}
	if summary == "" {
		if previouslyUnmatched {
			log.Info("BB executor can now match all observed waiting jobs to configured profiles")
		}
		return
	}
	log.Warn("BB executor cannot schedule at least %d observed waiting jobs because no configured profile matches their runs-on labels: %s", count, summary)
}

func (config bbExecutorConfig) schedulableCapacity(ctx context.Context, profile bbExecutorProfile, maxCount int) (int, error) {
	if maxCount < 1 || maxCount > 100 {
		return 0, errors.New("BB capacity max_count must be between 1 and 100")
	}
	var response bbCapacityResponse
	request := map[string]any{"spec": profile.Sandbox, "max_count": maxCount}
	if err := config.request(ctx, http.MethodPost, "/v1/scheduler/capacity", "", request, &response); err != nil {
		return 0, err
	}
	if response.Schedulable < 0 || response.MaxCount < 1 || response.MaxCount > 100 || response.Schedulable > response.MaxCount || response.AsOfMS <= 0 {
		return 0, errors.New("BB capacity response is invalid")
	}
	if response.MaxCount < maxCount {
		maxCount = response.MaxCount
	}
	if response.Schedulable > maxCount {
		return 0, errors.New("BB capacity response exceeds requested max_count")
	}
	return response.Schedulable, nil
}

func (config bbExecutorConfig) prepare(ctx context.Context, job *actions_model.ActionRunJob, profile bbExecutorProfile, labels []string) (*actions_model.ActionBBTask, error) {
	row := &actions_model.ActionBBTask{JobID: job.ID, Endpoint: strings.TrimRight(config.Endpoint, "/"), Project: config.Project}
	err := db.WithTx(ctx, func(ctx context.Context) error {
		runner := &actions_model.ActionRunner{UUID: uuid.New().String(), Name: fmt.Sprintf("bb-job-%d", job.ID), RepoID: job.RepoID, AgentLabels: labels, Ephemeral: true, HasCancellingSupport: true}
		runner.GenerateAndFillToken()
		if err := actions_model.CreateRunner(ctx, runner); err != nil {
			return err
		}
		row.RunnerID = runner.ID
		registration, err := json.Marshal(map[string]any{"id": runner.ID, "uuid": runner.UUID, "name": runner.Name, "token": runner.Token, "address": setting.AppURL, "labels": profile.Labels, "ephemeral": true})
		if err != nil {
			return err
		}
		row.Registration, err = secret.EncryptSecret(setting.SecretKey, string(registration))
		if err != nil {
			return err
		}
		if err := db.Insert(ctx, row); err != nil {
			return err
		}
		namespace := sha256.Sum256([]byte(setting.AppURL))
		row.Key = fmt.Sprintf("%x_%d", namespace[:8], row.ID)
		request, err := config.taskRequest(row, profile)
		if err != nil {
			return err
		}
		row.RequestJSON = string(request)
		_, err = db.GetEngine(ctx).ID(row.ID).Cols("request_json", "key").Update(row)
		return err
	})
	return row, err
}

func bbSecretName(row *actions_model.ActionBBTask) string {
	return "GITEA_BB_RUNNER_" + row.Key
}

func bbShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func bbRunnerDownloadScript(runnerURL string) string {
	return "download_attempt=1\ndownload_delay=1\nwhile ! curl --fail --silent --show-error --location --connect-timeout 15 --max-time 300 " + bbShellQuote(runnerURL) + " -o runner.tmp; do\n" +
		"  rm -f runner.tmp\n" +
		"  if [ \"$download_attempt\" -ge 6 ]; then\n" +
		"    echo 'runner download failed after 6 attempts' >&2\n" +
		"    exit 1\n" +
		"  fi\n" +
		"  echo \"runner download failed (attempt $download_attempt/6); retrying in ${download_delay}s\" >&2\n" +
		"  sleep \"$download_delay\"\n" +
		"  download_attempt=$((download_attempt + 1))\n" +
		"  download_delay=$((download_delay * 2))\n" +
		"done\n" +
		"mv runner.tmp runner\n"
}

func (config bbExecutorConfig) taskRequest(row *actions_model.ActionBBTask, profile bbExecutorProfile) ([]byte, error) {
	workdir, user, platform := "/opt/gitea-bb", "root", "Linux"
	checksum := "sha256sum --check --status"
	createDirectory := "mkdir -p "
	if profile.Platform == "darwin" {
		workdir, user, platform = "/tmp/gitea-bb-"+row.Key, "breadboard", "Darwin"
		checksum = "shasum -a 256 --check"
		createDirectory = "mkdir -m 700 "
	}
	if profile.ExecUser != "" {
		user = profile.ExecUser
	}
	runnerConfig, err := json.Marshal(map[string]any{"runner": map[string]any{"file": workdir + "/.runner", "capacity": 1, "timeout": fmt.Sprintf("%ds", config.MaxDurationS)}, "container": map[string]any{"network": "host", "bind_workdir": true}, "host": map[string]any{"workdir_parent": workdir + "/work"}})
	if err != nil {
		return nil, err
	}
	script := "set -eu\numask 077\ntest \"$(uname -s)\" = " + bbShellQuote(platform) + "\n" + createDirectory + bbShellQuote(workdir) + "\n"
	if profile.Platform != "darwin" {
		script += "export HOME=" + bbShellQuote(workdir) + "\n"
	}
	script += "cd " + bbShellQuote(workdir) + "\nprintf '%s' \"$" + bbSecretName(row) + "\" > .runner\nunset " + bbSecretName(row) + "\nprintf '%s' " + bbShellQuote(string(runnerConfig)) + " > config.json\n" + bbRunnerDownloadScript(profile.RunnerURL) + "printf '%s  runner\\n' " + bbShellQuote(profile.RunnerSHA256) + " | " + checksum + "\nchmod 700 runner\nexec ./runner -c config.json daemon\n"
	return json.Marshal(map[string]any{"sandbox": profile.Sandbox, "package_sets": profile.PackageSets, "max_duration_s": config.MaxDurationS, "exec": map[string]any{"cmd": []string{"sh", "-c", script}, "user": user, "timeout_s": config.MaxDurationS, "secrets": []string{bbSecretName(row)}}})
}

func (config bbExecutorConfig) advance(ctx context.Context, row *actions_model.ActionBBTask) error {
	registration, err := secret.DecryptSecret(setting.SecretKey, row.Registration)
	if err != nil {
		return err
	}
	if err := config.request(ctx, http.MethodPost, "/v1/secrets", "", map[string]string{"name": bbSecretName(row), "value": registration}, nil); err != nil {
		return err
	}
	var task bbTaskResult
	if row.TaskID == "" {
		var request json.Value
		if err := json.Unmarshal([]byte(row.RequestJSON), &request); err != nil {
			return fmt.Errorf("decode persisted BB task request: %w", err)
		}
		if err := config.request(ctx, http.MethodPost, "/v1/tasks", "gitea-bb-"+row.Key, request, &task); err != nil {
			var responseError *bbRequestError
			if !errors.As(err, &responseError) || responseError.status != http.StatusBadRequest {
				return err
			}
			log.Error("BB executor rejected task for job %d: %v", row.JobID, err)
			task.State = "failed"
		}
		if task.ID == "" && !task.terminal() {
			return errors.New("BB executor response has no task ID")
		}
		row.TaskID = task.ID
		if _, err := db.GetEngine(ctx).ID(row.ID).Cols("task_id").Update(row); err != nil {
			return err
		}
	} else if err := config.request(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(row.TaskID), "", nil, &task); err != nil {
		return err
	}
	job, has, err := db.GetByID[actions_model.ActionRunJob](ctx, row.JobID)
	if err != nil {
		return err
	}
	owned := true
	if has && job.TaskID != 0 {
		actionTask, err := actions_model.GetTaskByID(ctx, job.TaskID)
		if err != nil {
			return err
		}
		owned = actionTask.RunnerID == row.RunnerID
	}
	shouldCancel := !has || !owned
	if has && job.Status == actions_model.StatusCancelling {
		if row.CancellationRequested == 0 {
			row.CancellationRequested = timeutil.TimeStampNow()
			if _, err := db.GetEngine(ctx).ID(row.ID).Cols("cancellation_requested").Update(row); err != nil {
				return err
			}
		}
		shouldCancel = shouldCancel || row.CancellationRequested.AddDuration(2*time.Minute) < timeutil.TimeStampNow()
	}
	if has && job.Status.IsDone() {
		shouldCancel = shouldCancel || job.TaskID == 0 || job.Stopped.AddDuration(2*time.Minute) < timeutil.TimeStampNow()
	}
	if !task.terminal() && shouldCancel {
		if err := config.request(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(row.TaskID)+"/cancel", "", struct{}{}, &task); err != nil {
			return err
		}
	}
	if !task.terminal() {
		return nil
	}
	if has && owned && !job.Status.IsDone() {
		log.Error("BB task %s ended as %s before job %d reported a result", row.TaskID, task.State, row.JobID)
		status := actions_model.StatusFailure
		if job.Status == actions_model.StatusCancelling {
			status = actions_model.StatusCancelled
		}
		changed := false
		if err := db.WithTx(ctx, func(ctx context.Context) error {
			previousStatus := job.Status
			job.Status, job.Stopped = status, timeutil.TimeStampNow()
			affected, err := actions_model.UpdateRunJob(ctx, job, builder.Eq{"status": previousStatus, "task_id": job.TaskID}, "status", "stopped")
			if err != nil || affected != 1 {
				return err
			}
			changed = true
			if job.TaskID != 0 {
				return actions_model.StopTask(ctx, job.TaskID, status)
			}
			return nil
		}); err != nil {
			return err
		}
		if changed {
			notifyAndEmitFinishedJobs(ctx, []*actions_model.ActionRunJob{job})
		}
	}
	if err := config.request(ctx, http.MethodDelete, "/v1/secrets/"+bbSecretName(row), "", nil, nil); err != nil {
		return err
	}
	if _, err := db.DeleteByID[actions_model.ActionRunner](ctx, row.RunnerID); err != nil && !errors.Is(err, util.ErrNotExist) {
		return err
	}
	row.Completed = true
	row.Registration = ""
	_, err = db.GetEngine(ctx).ID(row.ID).Cols("completed", "registration").Update(row)
	return err
}
