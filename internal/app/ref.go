// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
)

// refKind is what a reference argument addresses, which decides the URLs it accepts.
type refKind int

const (
	// issueRef is an issue or pull request number; it accepts issue and pull request URLs.
	issueRef refKind = iota
	// pullRef is a pull request number; it accepts pull request and issue URLs.
	pullRef
	// runRef is an Actions run ID; it accepts run and job URLs.
	runRef
)

// ref is a parsed reference argument.
type ref struct {
	Number int64
	// Repo is the repository the reference names, as a repository argument (a URL,
	// [HOST/]OWNER/REPO or NAME); it is empty for N and #N.
	Repo string
}

// errNotRef marks an argument that is not a number, URL or OWNER/REPO#N, such as a branch name.
var errNotRef = errors.New("not a reference")

// parseRef parses N, #N, [HOST/]OWNER/REPO#N, or a URL of what kind addresses:
// https://HOST/OWNER/REPO/(issues|pulls)/N[#...] for issues and pull requests, and
// https://HOST/OWNER/REPO/actions/runs/N[/jobs/M] for runs.
func parseRef(raw string, kind refKind) (ref, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		return parseRefURL(raw, kind)
	}
	repo, value, hashed := strings.Cut(raw, "#")
	if !hashed {
		repo, value = "", raw
	}
	// Only a repository before # makes a reference, so a branch such as fix#12 stays a branch.
	if repo != "" && !repositoryLike(repo) {
		return ref{}, errNotRef
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil && !hashed {
		return ref{}, errNotRef
	}
	if err != nil || n < 1 || (repo != "" && kind == runRef) {
		return ref{}, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid %s %q: use %s", kind.noun(), raw, kind.forms()))
	}
	return ref{Number: n, Repo: repo}, nil
}

// repositoryLike reports whether raw has the form OWNER/REPO or HOST/OWNER/REPO.
func repositoryLike(raw string) bool {
	pieces := strings.Split(raw, "/")
	return len(pieces) >= 2 && settings.ValidSlug(strings.Join(pieces[len(pieces)-2:], "/"))
}

func parseRefURL(raw string, kind refKind) (ref, error) {
	invalid := clierr.Wrap(clierr.Usage, fmt.Errorf("%q is not %s URL; use %s", raw, kind.article(), kind.forms()))
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return ref{}, invalid
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	at := func(i int) string {
		if i < len(segments) {
			return segments[i]
		}
		return ""
	}
	positive := func(i int) int64 {
		n, err := strconv.ParseInt(at(i), 10, 64)
		if err != nil || n < 1 {
			return 0
		}
		return n
	}
	// The repository is the path before the marker, so instances below a base path work too.
	for i := 2; i < len(segments); i++ {
		result := ref{}
		switch {
		case kind == runRef && at(i) == "actions" && at(i+1) == "runs" && positive(i+2) > 0:
			// Since Gitea 1.26 the web URL holds the run ID, which the API takes too; a trailing
			// /jobs/M only selects a job on the web page.
			result.Number = positive(i + 2)
		case kind != runRef && (at(i) == "issues" || at(i) == "pulls") && positive(i+1) > 0:
			result.Number = positive(i + 1)
		default:
			continue
		}
		repository := url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/" + strings.Join(segments[:i], "/")}
		result.Repo = repository.String()
		return result, nil
	}
	return ref{}, invalid
}

func (k refKind) noun() string {
	switch k {
	case pullRef:
		return "pull request"
	case runRef:
		return "run"
	}
	return "issue"
}

// article returns noun with its indefinite article, such as "an issue".
func (k refKind) article() string {
	if k == issueRef {
		return "an issue"
	}
	return "a " + k.noun()
}

// forms lists the reference forms of kind for error messages.
func (k refKind) forms() string {
	if k == runRef {
		return "a run ID or URL"
	}
	return "a number, #number, OWNER/REPO#number or URL"
}

// resolveRef resolves raw, an issue, pull request or run reference, and the repository it
// belongs to. A URL or OWNER/REPO#N selects that repository for this invocation and must
// agree with --repo; N and #N address the repository gt targets otherwise.
func (a *application) resolveRef(cmd *cobra.Command, raw string, kind refKind) (target, int64, error) {
	t, numbers, err := a.resolveRefs(cmd, []string{raw}, kind)
	if err != nil {
		return t, 0, err
	}
	return t, numbers[0], nil
}

// resolveRefs is resolveRef for several references, which must name the same repository and
// distinct numbers.
func (a *application) resolveRefs(cmd *cobra.Command, raws []string, kind refKind) (target, []int64, error) {
	numbers := make([]int64, len(raws))
	var selected ref
	source := ""
	for i, raw := range raws {
		parsed, err := parseRef(raw, kind)
		if errors.Is(err, errNotRef) {
			return target{}, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid %s %q: use %s", kind.noun(), raw, kind.forms()))
		}
		if err != nil {
			return target{}, nil, err
		}
		if slices.Contains(numbers[:i], parsed.Number) {
			return target{}, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("#%d is selected more than once; supply unique numbers", parsed.Number))
		}
		numbers[i] = parsed.Number
		if parsed.Repo == "" {
			continue
		}
		if selected.Repo != "" && !a.sameRepository(selected.Repo, parsed.Repo) {
			return target{}, nil, clierr.Wrap(clierr.Usage, fmt.Errorf("%s and %s are in different repositories; run one command per repository", source, raw))
		}
		selected, source = parsed, raw
	}
	t, err := a.resolveRequest(cmd.Context(), targetRequest{requireRepo: true, authenticate: true, repo: selected.Repo, repoSource: source})
	return t, numbers, err
}

// sameRepository reports whether two repository arguments name the same repository.
func (a *application) sameRepository(first, second string) bool {
	s, err := a.settings()
	if err != nil {
		return strings.EqualFold(first, second)
	}
	one, oneErr := settings.ParseRepository(first, s.Hosts.Hosts)
	two, twoErr := settings.ParseRepository(second, s.Hosts.Hosts)
	return oneErr == nil && twoErr == nil && strings.EqualFold(one.Slug(), two.Slug()) && (one.Host == "" || two.Host == "" || one.Host == two.Host)
}

// pullSelector resolves the pull request a pr command addresses: N, #N, OWNER/REPO#N, a pull
// request URL, a head branch, OWNER:BRANCH, or, without an argument, the pull request of the
// checked-out branch.
func (a *application) pullSelector(cmd *cobra.Command, args []string) (target, int64, error) {
	if len(args) == 0 || args[0] == "" {
		// The checked-out branch says nothing about a repository chosen with --repo.
		if a.options.repo != "" {
			return target{}, 0, clierr.Wrap(clierr.Usage, errors.New("pass a pull request number, URL or branch with --repo"))
		}
		return a.currentPull(cmd.Context())
	}
	raw := args[0]
	if _, err := parseRef(raw, pullRef); !errors.Is(err, errNotRef) {
		return a.resolveRef(cmd, raw, pullRef)
	}
	t, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return t, 0, err
	}
	owner, branch, qualified := strings.Cut(raw, ":")
	if !qualified {
		owner, branch = "", raw
	}
	pulls, err := (services.Pulls{Client: t.client, Repository: t.Repo}).ListOpen(cmd.Context())
	if err != nil {
		return t, 0, err
	}
	n, err := pullForBranch(pulls, owner, branch, t.Repo)
	if err != nil {
		return t, 0, err
	}
	if n == 0 {
		return t, 0, fmt.Errorf("no open pull request for branch %q in %s; pass a number, URL or branch", raw, t.Repo)
	}
	return t, n, nil
}

// currentPull finds the pull request of the checked-out branch: the one pr checkout recorded
// while it is open, else the open pull request whose head is the branch's upstream (and the
// upstream remote's owner), else one whose head has the local branch's name, else N from a pr/N
// branch, else the recorded pull request even though it is no longer open.
func (a *application) currentPull(ctx context.Context) (target, int64, error) {
	branch, err := git.CurrentBranchConfig(ctx, a.gitProbe(), a.options.cwd)
	if err != nil {
		return target{}, 0, clierr.Wrap(clierr.Usage, errors.New("not on a branch; pass a pull request number, URL or branch"))
	}
	t, err := a.resolve(ctx, true)
	if err != nil {
		return t, 0, err
	}
	service := services.Pulls{Client: t.client, Repository: t.Repo}
	// A recorded pull request that was merged or closed only counts when no open one has the branch.
	recorded := int64(0)
	if branch.PullNumber > 0 && (branch.BaseRemote == "" || a.remoteOwns(ctx, t, branch.BaseRemote)) {
		if pull, err := service.View(ctx, branch.PullNumber); err != nil || pull.State == "open" {
			return t, branch.PullNumber, nil
		}
		recorded = branch.PullNumber
	}
	pulls, err := service.ListOpen(ctx)
	if err != nil {
		return t, 0, err
	}
	if branch.Merge != "" {
		owner := ""
		if remoteURL, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", branch.Remote); err == nil {
			if _, repo, err := settings.RepositoryURL(remoteURL, t.hosts()); err == nil {
				owner, _, _ = strings.Cut(repo, "/")
			}
		}
		if n, err := pullForBranch(pulls, owner, branch.Merge, t.Repo); n > 0 || err != nil {
			return t, n, err
		}
	}
	if n, err := pullForBranch(pulls, "", branch.Name, t.Repo); n > 0 || err != nil {
		return t, n, err
	}
	if number, ok := strings.CutPrefix(branch.Name, "pr/"); ok {
		if n, err := strconv.ParseInt(number, 10, 64); err == nil && n > 0 {
			return t, n, nil
		}
	}
	if recorded > 0 {
		return t, recorded, nil
	}
	return t, 0, fmt.Errorf("no open pull request for branch %q in %s; pass a number, URL or branch", branch.Name, t.Repo)
}

// remoteOwns reports whether the Git remote named remote points at the target's repository.
func (a *application) remoteOwns(ctx context.Context, t target, remote string) bool {
	raw, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", remote)
	return err == nil && t.ownsRemote(raw)
}

// pullForBranch returns the number of the open pull request whose head is branch in a
// repository of owner (any owner when empty), 0 when none is, and an error when several are.
func pullForBranch(pulls []services.Pull, owner, branch, repo string) (int64, error) {
	var matches []string
	var number int64
	for _, pull := range pulls {
		headOwner, _, _ := strings.Cut(pull.HeadRepo, "/")
		if pull.Head != branch || (owner != "" && !strings.EqualFold(headOwner, owner)) {
			continue
		}
		matches = append(matches, "#"+strconv.FormatInt(pull.Number, 10))
		number = pull.Number
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("branch %q has several open pull requests in %s (%s); pass a number", branch, repo, strings.Join(matches, ", "))
	}
	return number, nil
}

// login returns the account gt acts as: the --sudo user, else the authenticated account.
func (t target) login() string {
	if t.client != nil && t.client.Sudo != "" {
		return t.client.Sudo
	}
	return t.User
}

// expandMe returns the login gt acts as for "@me" in a user-valued flag such as --assignee,
// and value unchanged otherwise.
func (t target) expandMe(value string) string {
	if strings.TrimSpace(value) == "@me" {
		return cmp.Or(t.login(), value)
	}
	return value
}

// expandMeAll applies expandMe to every entry of a user-list flag, in place.
func (t target) expandMeAll(values []string) {
	for i, value := range values {
		values[i] = t.expandMe(value)
	}
}
