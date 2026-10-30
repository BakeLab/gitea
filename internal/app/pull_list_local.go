// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// localFieldsAnnotation holds the JSON fields of a command with its --local flag.
const localFieldsAnnotation = "gt.fields.local"

// localBranchPull is a local branch with the pull request it belongs to, if any.
type localBranchPull struct {
	Branch  string `json:"branch"`
	Current bool   `json:"current"`
	// Commit is the branch's tip; HeadSHA is the pull request's head commit.
	Commit  string `json:"commit"`
	Number  *int64 `json:"number"`
	State   string `json:"state"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	HeadSHA string `json:"head_sha"`
	draft   bool
}

// clockSkew allows for a commit dated by a clock ahead of the server's.
const clockSkew = 24 * time.Hour

func (a *application) pullListLocal(cmd *cobra.Command) error {
	var rejected string
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if rejected == "" && flag.Changed && flag.Name != "local" {
			rejected = flag.Name
		}
	})
	if rejected == "" && a.options.repo != "" {
		rejected = "repo"
	}
	if rejected != "" {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--local cannot be combined with --%s", rejected))
	}
	ctx := cmd.Context()
	if !a.insideClone(ctx) {
		return clierr.Wrap(clierr.Usage, errors.New("--local requires a Git clone"))
	}
	branches, err := git.LocalBranches(ctx, a.gitProbe(), a.options.cwd)
	if err != nil {
		return err
	}
	t, err := a.resolve(ctx, true)
	if err != nil {
		return err
	}
	items, err := a.localPulls(ctx, t, branches)
	if err != nil {
		return err
	}
	tty := a.display().TTY
	return writeTable(a, cmd, items, listSpec[localBranchPull]{
		Columns: []terminal.Column{{}, {Header: "Branch", Flex: true, Min: 12}, {Header: "ID"}, {Header: "State"}, {Header: "Title", Flex: true}},
		Row: func(item localBranchPull) []terminal.Cell {
			marker, color, number, state := "", "", "-", "-"
			if item.Current {
				marker, color = "*", "green"
			}
			if item.Number != nil {
				number, state = strconv.FormatInt(*item.Number, 10), pullState(services.Pull{State: item.State, Merged: item.State == "merged", Draft: item.draft})
				if tty {
					number = "#" + number
				}
			}
			return []terminal.Cell{
				{Text: marker, Color: color},
				{Text: item.Branch, Color: color},
				{Text: number, Color: stateColors[state]},
				{Text: state, Color: stateColors[state]},
				{Text: item.Title},
			}
		},
		Empty: "no local branches", Context: "local branches and their pull requests in " + t.Repo,
	}, listPage{Number: 1})
}

// localPulls finds the pull request of each local branch with as few requests as it can: every
// open pull request, then, for branches without an open one, closed and merged pull requests
// back to the oldest such branch's last commit, and the pull requests that pr checkout recorded
// and neither list had.
func (a *application) localPulls(ctx context.Context, t target, branches []git.LocalBranch) ([]localBranchPull, error) {
	service := services.Pulls{Client: t.client, Repository: t.Repo}
	open, err := service.ListOpen(ctx)
	if err != nil {
		return nil, err
	}
	repos := map[string]string{}
	m := localPullMatcher{repo: t.Repo, remoteRepo: func(remote string) string {
		repo, ok := repos[remote]
		if !ok {
			repo = a.remoteRepository(ctx, t, remote)
			repos[remote] = repo
		}
		return repo
	}}
	found := make([]*services.Pull, len(branches))
	var since time.Time
	for i, branch := range branches {
		found[i] = m.best(branch, open, false)
		if found[i] == nil && m.number(branch) == 0 && (since.IsZero() || m.since(branch).Before(since)) {
			since = m.since(branch)
		}
	}
	var closed []services.Pull
	if !since.IsZero() {
		if closed, err = service.ClosedSince(ctx, since); err != nil {
			return nil, err
		}
	}
	viewed := map[int64]*services.Pull{}
	for i, branch := range branches {
		if found[i] != nil {
			continue
		}
		found[i] = m.best(branch, closed, true)
		n := m.number(branch)
		if n == 0 || (found[i] != nil && found[i].Number == n) {
			continue
		}
		if _, ok := viewed[n]; !ok {
			pull, err := service.View(ctx, n)
			if err != nil && !services.IsNotFound(err) {
				return nil, err
			}
			viewed[n] = nil
			if err == nil {
				viewed[n] = &pull
			}
		}
		if viewed[n] != nil {
			found[i] = viewed[n]
		}
	}
	items := make([]localBranchPull, len(branches))
	for i, branch := range branches {
		items[i] = localBranchPull{Branch: branch.Name, Current: branch.Current, Commit: branch.Commit}
		if pull := found[i]; pull != nil {
			items[i].Number, items[i].State, items[i].Title, items[i].URL, items[i].HeadSHA, items[i].draft = &pull.Number, pull.State, pull.Title, pull.URL, pull.HeadSHA, pull.Draft
		}
	}
	return items, nil
}

// localPullMatcher tells the pull request of a local branch like currentPull and pr clean do.
type localPullMatcher struct {
	repo       string
	remoteRepo func(remote string) string
}

// number is the pull request that pr checkout recorded for branch in this repository, else N
// of a pr/N branch that records none.
func (m localPullMatcher) number(branch git.LocalBranch) int64 {
	if branch.PullNumber > 0 {
		if branch.BaseRemote == "" || strings.EqualFold(m.remoteRepo(branch.BaseRemote), m.repo) {
			return branch.PullNumber
		}
		return 0
	}
	if n, err := strconv.ParseInt(strings.TrimPrefix(branch.Name, "pr/"), 10, 64); strings.HasPrefix(branch.Name, "pr/") && err == nil && n > 0 {
		return n
	}
	return 0
}

// since is when a pull request of branch was last updated at the earliest: not before the older
// of its last commit and its upstream's.
func (m localPullMatcher) since(branch git.LocalBranch) time.Time {
	since := branch.Committed
	if !branch.UpstreamCommitted.IsZero() && branch.UpstreamCommitted.Before(since) {
		since = branch.UpstreamCommitted
	}
	return since.Add(-clockSkew)
}

// strength ranks how pull belongs to branch, 0 for not at all: recorded by pr checkout; its head
// branch is the upstream; its head branch in this repository has the branch's name; or, for an
// AGit pull request or a fork's branch that pr checkout named, its head is the branch's commit.
// A closed pull request matches by name only when updated since the branch's commits, as the
// name may have been reused.
func (m localPullMatcher) strength(branch git.LocalBranch, pull services.Pull, closed bool) int {
	owner, _, _ := strings.Cut(pull.HeadRepo, "/")
	recent := !closed || !m.since(branch).After(pullUpdated(pull))
	switch {
	case m.number(branch) == pull.Number:
		return 4
	case recent && branch.Merge != "" && branch.Merge == pull.Head && pull.HeadRepo != "" && strings.EqualFold(m.remoteRepo(branch.Remote), pull.HeadRepo):
		return 3
	case recent && pull.Head != "" && pull.Head == branch.Name && strings.EqualFold(pull.HeadRepo, m.repo):
		return 2
	case pull.HeadSHA != "" && pull.HeadSHA == branch.Commit && (pull.Head == "" || branch.Name == owner+"/"+pull.Head):
		return 1
	}
	return 0
}

// best returns the pull request of pulls that belongs to branch most strongly, the first of equals.
func (m localPullMatcher) best(branch git.LocalBranch, pulls []services.Pull, closed bool) *services.Pull {
	var best *services.Pull
	strongest := 0
	for i := range pulls {
		if s := m.strength(branch, pulls[i], closed); s > strongest {
			best, strongest = &pulls[i], s
		}
	}
	return best
}

func pullUpdated(pull services.Pull) time.Time {
	updated, _ := time.Parse(time.RFC3339, pull.UpdatedAt)
	return updated
}
