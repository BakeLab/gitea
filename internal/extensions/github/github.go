// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package github reads gh-* extensions from github.com anonymously. It has no way to send
// credentials: gt's accounts belong to Gitea instances, and a GitHub token must never be guessed
// from the environment.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Host is the instance URL extensions from GitHub are recorded with.
const Host = "https://github.com"

// Client reads public repositories and releases from GitHub.
type Client struct {
	// API is the REST API root, https://api.github.com unless a test replaces it.
	API  string
	HTTP *http.Client
}

// New returns an anonymous client with a timeout.
func New() Client {
	return Client{API: "https://api.github.com", HTTP: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: checkRedirect}}
}

// checkRedirect follows only HTTPS redirects, such as to GitHub's asset storage, and never
// forwards an Authorization header, which gt does not set in the first place.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many redirects")
	}
	if req.URL.Scheme != "https" && req.URL.Scheme != via[0].URL.Scheme {
		return fmt.Errorf("refusing redirect to %s", req.URL.Redacted())
	}
	req.Header.Del("Authorization")
	return nil
}

// Repository is a GitHub repository.
type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Release is a GitHub release.
type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

// Asset is a GitHub release asset.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
	// Digest is "sha256:HEX" for assets GitHub recorded a digest for.
	Digest string `json:"digest"`
}

// SHA256 returns the asset's recorded SHA-256, or "".
func (a Asset) SHA256() string {
	digest, ok := strings.CutPrefix(a.Digest, "sha256:")
	if !ok {
		return ""
	}
	return strings.ToLower(digest)
}

// ErrNotFound reports a repository, release or file GitHub does not have.
var ErrNotFound = errors.New("not found on GitHub")

func (c Client) get(ctx context.Context, endpoint, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.API, "/")+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub answered %s for %s", resp.Status, endpoint)
	}
	return resp, nil
}

func (c Client) getJSON(ctx context.Context, endpoint string, out any) error {
	resp, err := c.get(ctx, endpoint, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
		return errors.New("GitHub returned an invalid JSON response")
	}
	return nil
}

func repoPath(owner, repo string) string {
	return "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// Repository returns the public repository owner/repo.
func (c Client) Repository(ctx context.Context, owner, repo string) (Repository, error) {
	var repository Repository
	return repository, c.getJSON(ctx, repoPath(owner, repo), &repository)
}

// Release returns the release tagged tag, or the latest release when tag is empty.
func (c Client) Release(ctx context.Context, owner, repo, tag string) (Release, error) {
	endpoint := repoPath(owner, repo) + "/releases/latest"
	if tag != "" {
		endpoint = repoPath(owner, repo) + "/releases/tags/" + url.PathEscape(tag)
	}
	var release Release
	return release, c.getJSON(ctx, endpoint, &release)
}

// Commit resolves ref to its commit SHA.
func (c Client) Commit(ctx context.Context, owner, repo, ref string) (string, error) {
	resp, err := c.get(ctx, repoPath(owner, repo)+"/commits/"+url.PathEscape(ref), "application/vnd.github.sha")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	sha, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	return strings.TrimSpace(string(sha)), err
}

// File returns the content of path at ref, and whether it exists.
func (c Client) File(ctx context.Context, owner, repo, ref, path string) ([]byte, bool, error) {
	resp, err := c.get(ctx, repoPath(owner, repo)+"/contents/"+strings.TrimLeft(path, "/")+"?ref="+url.QueryEscape(ref), "application/vnd.github.raw+json")
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return data, err == nil, err
}

// Download opens the content of an asset, which must be served over HTTPS.
func (c Client) Download(ctx context.Context, asset Asset) (io.ReadCloser, error) {
	u, err := url.Parse(asset.URL)
	if err != nil || (u.Scheme != "https" && !strings.HasPrefix(asset.URL, c.API)) {
		return nil, fmt.Errorf("refusing to download %s over an insecure connection", asset.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub answered %s for %s", resp.Status, asset.Name)
	}
	return resp.Body, nil
}
