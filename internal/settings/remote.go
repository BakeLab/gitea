// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package settings

import (
	"cmp"
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
		sshHost := cmp.Or(host.SSHHost, DefaultSSHHost(instance))
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
		return "", "", sshRemoteError(u, remoteSSH, hosts)
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

// RemoteRepository resolves a Git remote like RepositoryURL, ignoring the credentials of a remote
// such as https://user:token@host/owner/name.git, which repository arguments must not embed.
func RemoteRepository(raw string, hosts map[string]Host) (string, string, error) {
	return RepositoryURL(WithoutCredentials(raw), hosts)
}

// WithoutCredentials returns a URL without the secrets it can embed, for showing or matching it.
// An HTTP(S) URL loses its user information, an ssh:// URL its password, and both their query and
// fragment. An scp-like address such as git@host:owner/name keeps its user, which Git reads as a
// login name only. A URL that does not parse but holds an @ becomes "".
func WithoutCredentials(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "@") {
			return ""
		}
		return raw
	}
	if u.User != nil && u.Scheme == "ssh" {
		u.User = url.User(u.User.Username())
	} else {
		u.User = nil
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// DefaultSSHHost returns the SSH host of an instance without ssh_host: its web host on port 22.
func DefaultSSHHost(instance string) string {
	base, _ := url.Parse(instance)
	if host := base.Hostname(); strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return base.Hostname()
}

// SSHRemoteError is an SSH remote that matches no instance's SSH host.
type SSHRemoteError struct {
	// Endpoint is the remote's host:port; Instance is the only instance with the remote's host
	// name, or "".
	Endpoint, Instance string
	// Environment says that the instance is the GITEA_TOKEN instance without stored settings, whose
	// SSH host only GITEA_INSTANCE_SSH_HOST sets.
	Environment bool
}

func (e *SSHRemoteError) Error() string {
	if e.Endpoint == "" {
		return "SSH remote does not match a configured instance"
	}
	if e.Environment {
		return fmt.Sprintf("SSH remote does not match a configured instance; if %s is the SSH host of %s, %s", e.Endpoint, cmp.Or(e.Instance, "the GITEA_TOKEN instance"), e.Fix())
	}
	if e.Instance != "" {
		return fmt.Sprintf("SSH remote does not match a configured instance; if %s is the SSH host of %s, run %s", e.Endpoint, e.Instance, e.Fix())
	}
	return fmt.Sprintf("SSH remote does not match a configured instance; if %s is an instance's SSH host, run %s, or set GITEA_INSTANCE_SSH_HOST with GITEA_TOKEN", e.Endpoint, e.Fix())
}

// Fix returns the command that configures the remote's endpoint as an instance's SSH host.
func (e *SSHRemoteError) Fix() string {
	if e.Environment {
		return "set GITEA_INSTANCE_SSH_HOST=" + e.Endpoint
	}
	return "gt config set ssh_host " + e.Endpoint + " --host " + cmp.Or(e.Instance, "URL")
}

func sshRemoteError(u *url.URL, endpoint string, hosts map[string]Host) error {
	if endpoint == "" {
		return &SSHRemoteError{}
	}
	var named []string
	for instance, host := range hosts {
		configured, err := url.Parse("ssh://" + cmp.Or(host.SSHHost, DefaultSSHHost(instance)))
		if err == nil && strings.EqualFold(configured.Hostname(), u.Hostname()) {
			named = append(named, instance)
		}
	}
	e := &SSHRemoteError{Endpoint: endpoint}
	if len(named) == 1 {
		e.Instance = named[0]
	}
	return e
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
	best, tied := "", false
	for instance := range hosts {
		prefix := bareInstance(instance)
		if !strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(prefix)+"/") || !ValidSlug(trimmed[len(prefix)+1:]) {
			continue
		}
		switch longest := len(bareInstance(best)); {
		case len(prefix) > longest:
			best, tied = instance, false
		case len(prefix) == longest && best != "":
			tied = true // http and https of one host: the scheme decides, not map order.
		}
	}
	if tied {
		return RepositoryRef{}, fmt.Errorf("instance %q is configured for both http and https; include the scheme", bareInstance(best))
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
