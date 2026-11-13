// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/log"
	base "gitea.dev/modules/migration"
	"gitea.dev/modules/structs"
	"gitea.dev/modules/util"

	gh "github.com/google/go-github/v93/github"
)

var githubRepositoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// SyncGitHubOrganizationPullMirrors discovers organization repositories and creates missing
// private pull mirrors. Existing non-mirror repositories are never modified or replaced.
func SyncGitHubOrganizationPullMirrors(ctx context.Context, githubOwner, giteaOwner, token string, mirrorInterval, actionsInterval time.Duration) error {
	if !githubRepositoryName.MatchString(githubOwner) || !githubRepositoryName.MatchString(giteaOwner) || token == "" {
		return errors.New("GitHub organization mirror sync is missing valid owners or a token")
	}
	client, err := gh.NewClient(gh.WithAuthToken(token), gh.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}))
	if err != nil {
		return fmt.Errorf("create GitHub client: %w", err)
	}

	var repositories []*gh.Repository
	for page := 1; ; page++ {
		batch, _, err := client.Repositories.ListByOrg(ctx, githubOwner, &gh.RepositoryListByOrgOptions{
			Type:        "all",
			ListOptions: gh.ListOptions{PerPage: 100, Page: page},
		})
		if err != nil {
			return fmt.Errorf("list GitHub repositories for organization %q: %w", githubOwner, err)
		}
		repositories = append(repositories, batch...)
		if len(batch) < 100 {
			break
		}
	}
	log.Info("GitHub organization sync: discovered %d repositories in %s", len(repositories), githubOwner)
	// Import smaller repositories first so an interrupted run can make steady progress.
	sort.Slice(repositories, func(i, j int) bool {
		if repositories[i] == nil || repositories[j] == nil {
			return repositories[i] == nil && repositories[j] != nil
		}
		return repositories[i].GetSize() < repositories[j].GetSize()
	})

	owner, err := user_model.GetUserByName(ctx, giteaOwner)
	if err != nil {
		return fmt.Errorf("get Gitea mirror owner %q: %w", giteaOwner, err)
	}
	if !owner.IsOrganization() {
		return fmt.Errorf("Gitea mirror owner %q is not an organization", giteaOwner)
	}
	doer, err := user_model.GetAdminUser(ctx)
	if err != nil {
		return fmt.Errorf("get administrator for GitHub organization mirror sync: %w", err)
	}

	var failures []error
	seen := make(map[string]bool, len(repositories))
	for _, source := range repositories {
		if source == nil {
			failures = append(failures, fmt.Errorf("GitHub returned an empty repository for organization %q", githubOwner))
			continue
		}
		name := source.GetName()
		if !githubRepositoryName.MatchString(name) {
			failures = append(failures, fmt.Errorf("GitHub returned an invalid repository name for organization %q", githubOwner))
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true

		interval := mirrorInterval
		repository, err := repo.GetRepositoryByName(ctx, owner.ID, name)
		if err == nil {
			if !repository.IsMirror || !repository.IsPrivate || !sameGitHubOrigin(repository.OriginalURL, githubOwner, name) {
				failures = append(failures, fmt.Errorf("%s/%s already exists and is not the expected private GitHub pull mirror", giteaOwner, name))
				continue
			}
			if repository.UnitEnabled(ctx, unit.TypeActions) {
				interval = actionsInterval
			}
			mirror, err := repo.GetMirrorByRepoID(ctx, repository.ID)
			if err != nil {
				failures = append(failures, fmt.Errorf("get pull mirror %s/%s: %w", giteaOwner, name, err))
				continue
			}
			remote, err := git.ParseRemoteAddressURL(ctx, repository, mirror.GetRemoteName())
			if err != nil {
				failures = append(failures, fmt.Errorf("read pull mirror remote for %s/%s: %s", giteaOwner, name, util.SanitizeCredentialURLs(err.Error())))
				continue
			}
			address, updateAuth, err := githubMirrorRemoteAddress(remote.URL, githubOwner, name, token)
			if err != nil {
				failures = append(failures, fmt.Errorf("check pull mirror remote for %s/%s: %w", giteaOwner, name, err))
				continue
			}
			if updateAuth {
				if err := git.ManagedRemoteSetURL(ctx, repository, mirror.GetRemoteName(), address); err != nil {
					failures = append(failures, fmt.Errorf("update pull mirror authentication for %s/%s: %s", giteaOwner, name, util.SanitizeCredentialURLs(err.Error())))
					continue
				}
			}
			if mirror.Interval != interval {
				mirror.Interval = interval
				mirror.ScheduleNextUpdate()
				if err := repo.UpdateMirror(ctx, mirror); err != nil {
					failures = append(failures, fmt.Errorf("update pull mirror interval for %s/%s: %w", giteaOwner, name, err))
					continue
				}
			}
			log.Info("GitHub organization sync: validated existing pull mirror %s/%s", giteaOwner, name)
			continue
		}
		if !repo.IsErrRepoNotExist(err) {
			failures = append(failures, fmt.Errorf("check Gitea repository %s/%s: %w", giteaOwner, name, err))
			continue
		}

		cloneURL := "https://github.com/" + githubOwner + "/" + name + ".git"
		log.Info("GitHub organization sync: creating private pull mirror %s/%s", giteaOwner, name)
		migrated, err := MigrateRepository(ctx, doer, giteaOwner, base.MigrateOptions{
			CloneAddr:      cloneURL,
			AuthUsername:   "x-access-token",
			AuthToken:      token,
			UID:            int(owner.ID),
			RepoName:       name,
			Mirror:         true,
			LFS:            true,
			Private:        true,
			GitServiceType: structs.GithubService,
			MirrorInterval: interval.String(),
		}, nil)
		if err != nil {
			failures = append(failures, fmt.Errorf("create pull mirror %s/%s: %w", giteaOwner, name, err))
			continue
		}
		if migrated == nil {
			failures = append(failures, fmt.Errorf("create pull mirror %s/%s returned no repository", giteaOwner, name))
			continue
		}
		if migrated.UnitEnabled(ctx, unit.TypeActions) {
			mirror, err := repo.GetMirrorByRepoID(ctx, migrated.ID)
			if err != nil {
				failures = append(failures, fmt.Errorf("get new pull mirror %s/%s: %w", giteaOwner, name, err))
				continue
			}
			mirror.Interval = actionsInterval
			mirror.ScheduleNextUpdate()
			if err := repo.UpdateMirror(ctx, mirror); err != nil {
				failures = append(failures, fmt.Errorf("set Actions pull mirror interval for %s/%s: %w", giteaOwner, name, err))
				continue
			}
		}
	}
	return errors.Join(failures...)
}

func githubMirrorRemoteAddress(remote *url.URL, owner, name, token string) (string, bool, error) {
	if remote == nil {
		return "", false, errors.New("missing pull mirror remote")
	}
	address := *remote
	address.User = nil
	if !sameGitHubOrigin(address.String(), owner, name) {
		return "", false, errors.New("pull mirror remote is not the expected GitHub repository")
	}
	if remote.User != nil {
		password, ok := remote.User.Password()
		if ok && remote.User.Username() == "x-access-token" && password == token {
			return "", false, nil
		}
	}
	address.User = url.UserPassword("x-access-token", token)
	return address.String(), true, nil
}

func sameGitHubOrigin(rawURL, owner, name string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || !strings.EqualFold(u.Hostname(), "github.com") {
		return false
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	return strings.EqualFold(path, owner+"/"+name)
}
