// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"errors"
	"fmt"
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
	return len(pieces) == 2 && validPiece(pieces[0]) && validPiece(pieces[1])
}

// validPiece reports whether piece can be an owner or repository name.
func validPiece(piece string) bool {
	// ':' keeps scp-style remotes such as git@host:owner/name.git from passing as a slug.
	return piece != "" && piece != "." && piece != ".." && !strings.ContainsAny(piece, ":?#\\ \t\r\n")
}

// RepositoryRef is a parsed repository argument.
type RepositoryRef struct {
	// Host is the instance URL the argument names, or "" when it names none.
	Host string
	// Owner is "" for a bare NAME, which belongs to the active account.
	Owner, Name string
}

// Slug returns owner/name, or the bare name when the owner is not known yet.
func (r RepositoryRef) Slug() string {
	if r.Owner == "" {
		return r.Name
	}
	return r.Owner + "/" + r.Name
}

// ParseRepository reads a repository argument: NAME, OWNER/NAME, HOST/OWNER/NAME (with an
// optional .git suffix), or a clone or web URL. HOST may carry a port and a configured
// instance's base path; it takes the scheme of the configured instance it names, else https.
func ParseRepository(raw string, hosts map[string]Host) (RepositoryRef, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") || scpLike(raw) {
		host, slug, err := RepositoryURL(raw, hosts)
		if err != nil {
			return RepositoryRef{}, err
		}
		owner, name, _ := strings.Cut(slug, "/")
		return RepositoryRef{Host: host, Owner: owner, Name: name}, nil
	}
	invalid := fmt.Errorf("invalid repository %q: use OWNER/REPO, HOST/OWNER/REPO or a URL", raw)
	trimmed := strings.TrimSuffix(raw, ".git")
	pieces := strings.Split(trimmed, "/")
	switch {
	case len(pieces) == 1 && validPiece(pieces[0]):
		return RepositoryRef{Name: pieces[0]}, nil
	case len(pieces) == 2 && ValidSlug(trimmed):
		return RepositoryRef{Owner: pieces[0], Name: pieces[1]}, nil
	case len(pieces) < 3:
		return RepositoryRef{}, invalid
	}
	// The longest configured instance prefix wins, so example.org/gitea/owner/name selects the
	// instance installed below /gitea rather than example.org.
	best := ""
	for instance := range hosts {
		prefix := bareInstance(instance)
		if len(prefix) > len(bareInstance(best)) && strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(prefix)+"/") && ValidSlug(trimmed[len(prefix)+1:]) {
			best = instance
		}
	}
	if best != "" {
		owner, name, _ := strings.Cut(trimmed[len(bareInstance(best))+1:], "/")
		return RepositoryRef{Host: best, Owner: owner, Name: name}, nil
	}
	if len(pieces) != 3 || !ValidSlug(pieces[1]+"/"+pieces[2]) {
		return RepositoryRef{}, invalid
	}
	host, err := ResolveHost(pieces[0], hosts)
	if err != nil {
		return RepositoryRef{}, invalid
	}
	return RepositoryRef{Host: host, Owner: pieces[1], Name: pieces[2]}, nil
}

// ResolveHost normalizes an instance given as a URL or as a bare HOST[:PORT][/PATH]. A bare
// host takes the scheme of the configured instance it names, else https.
func ResolveHost(raw string, hosts map[string]Host) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		return NormalizeHost(raw)
	}
	bare := strings.TrimSuffix(raw, "/")
	var matches []string
	for instance := range hosts {
		if strings.EqualFold(bareInstance(instance), bare) {
			matches = append(matches, instance)
		}
	}
	switch len(matches) {
	case 0:
		instance, err := NormalizeHost("https://" + raw)
		if err != nil {
			return "", fmt.Errorf("invalid instance %q: use HOST[:PORT][/PATH] or an http(s) URL", raw)
		}
		return instance, nil
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("instance %q is configured for both http and https; include the scheme", raw)
}

// bareInstance returns an instance URL without its scheme, such as localhost:3001/gitea.
func bareInstance(instance string) string {
	_, bare, _ := strings.Cut(instance, "://")
	return bare
}
