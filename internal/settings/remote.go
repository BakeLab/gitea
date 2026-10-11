// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"errors"
	"net/url"
	"strings"
)

// RemoteIsSSH reports whether a Git remote uses SSH: an ssh:// URL or an scp-style user@host:path.
func RemoteIsSSH(remote string) bool {
	return strings.HasPrefix(remote, "ssh://") || scpLike(remote)
}

func scpLike(remote string) bool {
	return !strings.Contains(remote, "://") && strings.Contains(remote, "@")
}

// ParseRemote parses a Git remote URL, reading scp-style user@host:path as ssh://user@host/path
// without interpreting shell syntax.
func ParseRemote(raw string) (*url.URL, error) {
	if user, path, ok := strings.Cut(raw, ":"); ok && scpLike(raw) {
		raw = "ssh://" + user + "/" + path
	}
	return url.Parse(raw)
}

// RepositoryURL resolves a repository URL or Git remote to a configured instance and its owner/name.
// Unconfigured HTTP(S) URLs take the final two path segments as the repository.
func RepositoryURL(raw string, hosts map[string]Host) (string, string, error) {
	u, err := ParseRemote(raw)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh") {
		return "", "", errors.New("invalid repository URL")
	}
	password := false
	if u.User != nil {
		_, password = u.User.Password()
	}
	if u.User != nil && (u.Scheme != "ssh" || password) {
		return "", "", errors.New("repository URLs must not embed credentials")
	}
	slug, cloneURL := strings.CutSuffix(strings.Trim(u.Path, "/"), ".git")
	bestHost, bestRepo, bestLen := "", "", -1
	matchedHost := false
	remoteSSH, remoteErr := SSHEndpoint(u.Host)
	for instance, host := range hosts {
		base, _ := url.Parse(instance)
		sshHost := host.SSHHost
		if sshHost == "" {
			sshHost = base.Hostname()
			if strings.Contains(sshHost, ":") {
				sshHost = "[" + sshHost + "]"
			}
		}
		prefix := strings.Trim(base.Path, "/")
		configuredSSH, configuredErr := SSHEndpoint(sshHost)
		match := (u.Scheme == "ssh" && remoteErr == nil && configuredErr == nil && remoteSSH == configuredSSH) || (u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host))
		if !match {
			continue
		}
		matchedHost = true
		candidate := slug
		if u.Scheme != "ssh" && prefix != "" {
			if !strings.HasPrefix(slug, prefix+"/") {
				continue
			}
			candidate = strings.TrimPrefix(slug, prefix+"/")
		}
		if !ValidSlug(candidate) {
			continue
		}
		if bestHost != "" && u.Scheme == "ssh" && bestHost != instance {
			return "", "", errors.New("SSH remote matches multiple instances; select --host")
		}
		if len(prefix) == bestLen && bestHost != instance {
			return "", "", errors.New("repository URL matches multiple instances; select --host")
		}
		if len(prefix) > bestLen {
			bestHost, bestRepo, bestLen = instance, candidate, len(prefix)
		}
	}
	if bestHost != "" {
		return bestHost, bestRepo, nil
	}
	if u.Scheme == "ssh" {
		return "", "", errors.New("SSH remote does not match a configured instance")
	}
	if matchedHost {
		return "", "", errors.New("repository URL path is ambiguous; use owner/repository or a clone URL")
	}
	parts := strings.Split(slug, "/")
	if len(parts) < 2 {
		return "", "", errors.New("repository URL requires owner/repository")
	}
	repo := strings.Join(parts[len(parts)-2:], "/")
	// A .git suffix marks a clone URL, whose repository may share its name with a web route.
	if !ValidSlug(repo) || (!cloneURL && looksLikeWebResourcePath(parts)) {
		return "", "", errors.New("repository URL path is ambiguous; use owner/repository or a clone URL")
	}
	u.Path = "/" + strings.Join(parts[:len(parts)-2], "/")
	u.RawPath, u.RawQuery, u.Fragment, u.User = "", "", "", nil
	instance, err := NormalizeHost(u.String())
	return instance, repo, err
}

// looksLikeWebResourcePath detects Gitea HTML routes such as owner/repo/issues/7, which have a
// web route keyword in a segment after the possible owner/repo. Taking the final two segments
// from those paths would invent the wrong repository.
func looksLikeWebResourcePath(parts []string) bool {
	if len(parts) < 3 {
		return false
	}
	for _, part := range parts[2:] {
		switch strings.ToLower(part) {
		case "issues", "pulls", "pull", "commit", "commits", "src", "tree", "blob", "raw",
			"compare", "releases", "tags", "wiki", "actions", "settings", "projects",
			"packages", "labels", "milestones", "activities", "forks", "watchers", "stars":
			return true
		}
	}
	return false
}

// ValidSlug reports whether slug is exactly owner/name.
func ValidSlug(slug string) bool {
	pieces := strings.Split(slug, "/")
	if len(pieces) != 2 {
		return false
	}
	for _, piece := range pieces {
		// ':' keeps scp-style remotes such as git@host:owner/name.git from passing as a slug.
		if piece == "" || piece == "." || piece == ".." || strings.ContainsAny(piece, ":?#\\ \t\r\n") {
			return false
		}
	}
	return true
}
