// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Branch configuration keys that record what a backport branch backports, so that a backport
// stopped at a conflict can be finished later.
const (
	backportNumberKey = "gitea-cli-backport"
	backportTargetKey = "gitea-cli-backport-target"
	backportOntoKey   = "gitea-cli-backport-onto"
	backportFromKey   = "gitea-cli-backport-from"
	backportPickedKey = "gitea-cli-backport-picked"
)

// BackportOptions selects a merged pull request, the branch to backport it to and the new local
// branch that receives the cherry-picks.
type BackportOptions struct {
	// CredentialArgs contains per-operation Git configuration for the fetches only.
	CredentialArgs []string
	Directory      string
	Remote         string
	Prompt         bool
	Number         int64
	// Base is the branch the pull request was merged into, MergeCommit the commit the merge left
	// there and MergeBase the commit of Base the pull request started from.
	Base, MergeCommit, MergeBase string
	Target, Branch               string
	// Keep leaves a conflicting cherry-pick in progress to resolve instead of undoing the backport.
	Keep bool
	// Hold, when set, returns a runner for one cherry-pick that holds back Git's diagnostics and a
	// function that shows them, so that only those of a cherry-pick left to the user are shown.
	Hold func() (Runner, func())
}

// BackportResult describes a backport branch: the commits picked from the base branch and the
// commits they became on the backport branch.
type BackportResult struct {
	Target  string   `json:"target"`
	Branch  string   `json:"branch"`
	Picked  []string `json:"picked"`
	Commits []string `json:"commits"`
	Number  int64    `json:"-"`
}

// BackportConflict reports a cherry-pick that stopped at conflicts. Kept tells whether the
// cherry-pick is still in progress on the backport branch or was undone.
type BackportConflict struct {
	Target, Branch string
	Files          []string
	Kept           bool
}

func (e *BackportConflict) Error() string {
	files := ""
	if len(e.Files) > 0 {
		files = " in " + strings.Join(e.Files, ", ")
	}
	if e.Kept {
		return fmt.Sprintf("cherry-picking to %s conflicts%s; resolve the conflicts on branch %s, git add them and run git cherry-pick --continue, then gt pr backport --continue", e.Target, files, e.Branch)
	}
	return fmt.Sprintf("cherry-picking to %s conflicts%s; nothing was changed. Run again with --keep to resolve the conflicts yourself", e.Target, files)
}

// BackportSkipped reports a target that cannot take the backport, such as one that already has
// it; the clone is left as it was, so that other targets can still be tried.
type BackportSkipped struct{ Err error }

func (e *BackportSkipped) Error() string { return e.Err.Error() }
func (e *BackportSkipped) Unwrap() error { return e.Err }

// Backport cherry-picks the commits a merged pull request left on its base branch onto a new branch
// started from the remote's target branch, with -x, and switches back to the branch it ran on. A
// merge commit contributes the commits it merged; a commit without merge parents contributes itself
// and, when it ends a run of commits by the pull request's authors at their author dates, that run,
// which is how a rebase or fast-forward merge lands; any other single commit is a squash. On a
// conflict it undoes everything unless Keep is set.
func Backport(ctx context.Context, runner Runner, opts BackportOptions) (BackportResult, error) {
	run := func(args ...string) (string, error) { return runner.Output(ctx, opts.Directory, opts.Prompt, args...) }
	if opts.Number < 1 || opts.Remote == "" || opts.Base == "" || opts.MergeCommit == "" || opts.Target == "" || opts.Branch == "" {
		return BackportResult{}, errors.New("backport requires a pull number, remote, base branch, merge commit, target and branch")
	}
	for _, name := range []string{opts.Target, opts.Branch, opts.Base} {
		if _, err := run("check-ref-format", "--branch", name); err != nil {
			return BackportResult{}, fmt.Errorf("invalid branch name %q", name)
		}
	}
	if err := requireWorkTree(run, "backport"); err != nil {
		return BackportResult{}, err
	}
	if err := refuseCherryPickInProgress(run); err != nil {
		return BackportResult{}, err
	}
	status, err := run("status", "--porcelain=v1", "--untracked-files=no")
	if err != nil {
		return BackportResult{}, err
	}
	if status != "" {
		return BackportResult{}, errors.New("working tree has changes; commit or stash them before a backport")
	}
	if existing, err := optional(run("rev-parse", "--verify", "--quiet", "refs/heads/"+opts.Branch)); err != nil {
		return BackportResult{}, err
	} else if existing != "" {
		return BackportResult{}, &BackportSkipped{existingBranch(run, opts.Branch)}
	}
	fetch := func(ref string) (string, error) {
		args := append(append([]string{}, opts.CredentialArgs...), "fetch", "--quiet", "--no-tags", "--", opts.Remote, ref)
		if _, err := run(args...); err != nil {
			return "", err
		}
		return run("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	}
	onto, err := fetch("refs/heads/" + opts.Target)
	if err != nil {
		return BackportResult{}, &BackportSkipped{fmt.Errorf("could not fetch the target branch %s from %s: %w", opts.Target, opts.Remote, err)}
	}
	base, err := fetch("refs/heads/" + opts.Base)
	if err != nil {
		return BackportResult{}, fmt.Errorf("could not fetch the base branch %s from %s: %w", opts.Base, opts.Remote, err)
	}
	if _, err := run("merge-base", "--is-ancestor", opts.MergeCommit, base); err != nil {
		return BackportResult{}, fmt.Errorf("the merge commit %s is not on the base branch %s of %s", shortCommit(opts.MergeCommit), opts.Base, opts.Remote)
	}
	head, err := fetch(fmt.Sprintf("refs/pull/%d/head", opts.Number))
	if err != nil {
		return BackportResult{}, fmt.Errorf("could not fetch pull request #%d from %s: %w", opts.Number, opts.Remote, err)
	}
	picks, err := landedCommits(run, opts.MergeCommit, opts.MergeBase, head)
	if err != nil {
		return BackportResult{}, err
	}
	if _, err := run("merge-base", "--is-ancestor", picks[len(picks)-1], onto); err == nil {
		return BackportResult{}, &BackportSkipped{fmt.Errorf("pull request #%d is already on %s", opts.Number, opts.Target)}
	}
	from, err := CurrentBranch(ctx, runner, opts.Directory)
	if err != nil {
		if from, err = run("rev-parse", "--verify", "HEAD^{commit}"); err != nil {
			return BackportResult{}, err
		}
	}
	if _, err := run("switch", "--quiet", "--no-track", "--create", opts.Branch, onto); err != nil {
		return BackportResult{}, fmt.Errorf("could not create branch %s: %w", opts.Branch, err)
	}
	undo := func() {
		if picking, _ := cherryPickInProgress(run); picking {
			_, _ = run("cherry-pick", "--abort")
		}
		_, _ = run(switchBack(from)...)
		_, _ = run("branch", "--quiet", "-D", opts.Branch)
	}
	for key, value := range map[string]string{
		backportNumberKey: strconv.FormatInt(opts.Number, 10), backportTargetKey: opts.Target,
		backportOntoKey: onto, backportFromKey: from, backportPickedKey: strings.Join(picks, " "), pullBaseRemoteKey: opts.Remote,
	} {
		if _, err := run("config", "branch."+opts.Branch+"."+key, value); err != nil {
			undo()
			return BackportResult{}, err
		}
	}
	show := func() {}
	pick := func(args ...string) error {
		picker := runner
		if opts.Hold != nil {
			picker, show = opts.Hold()
		}
		_, err := picker.Output(ctx, opts.Directory, opts.Prompt, args...)
		return err
	}
	err = pick(append([]string{"cherry-pick", "-x", "--allow-empty"}, picks...)...)
	for err != nil {
		if picking, _ := cherryPickInProgress(run); !picking {
			show()
			undo()
			return BackportResult{}, fmt.Errorf("could not cherry-pick to %s: %w", opts.Target, err)
		}
		// A commit whose changes the target already has stops the cherry-pick without conflicts.
		if emptyPick(run) {
			err = pick("cherry-pick", "--skip")
			continue
		}
		conflict := &BackportConflict{Target: opts.Target, Branch: opts.Branch, Kept: opts.Keep}
		if files, err := run("diff", "--name-only", "--diff-filter=U"); err == nil && files != "" {
			conflict.Files = strings.Split(files, "\n")
		}
		if opts.Keep {
			show()
		} else {
			undo()
		}
		return BackportResult{}, conflict
	}
	if added, err := run("rev-list", "--max-count=1", onto+"..refs/heads/"+opts.Branch); err == nil && added == "" {
		undo()
		return BackportResult{}, &BackportSkipped{fmt.Errorf("the changes of pull request #%d are already on %s", opts.Number, opts.Target)}
	}
	return finishBackport(run, opts.Branch, BackportResult{Target: opts.Target, Branch: opts.Branch, Picked: picks, Number: opts.Number}, onto, from)
}

// existingBranch explains why a backport cannot use an existing branch, which a stopped backport
// or the pull request opened from it may still need.
func existingBranch(run func(...string) (string, error), branch string) error {
	get := func(key string) string {
		value, _ := optional(run("config", "--get", "branch."+branch+"."+key))
		return value
	}
	if get(backportNumberKey) != "" {
		return fmt.Errorf("branch %q already exists; choose another with --branch, or finish its backport with gt pr backport --continue", branch)
	}
	if n, err := strconv.ParseInt(get(pullNumberKey), 10, 64); err == nil && n > 0 {
		return fmt.Errorf("branch %q already exists for pull request #%d; choose another with --branch, or delete it with gt pr clean %d once that pull request is merged or closed", branch, n, n)
	}
	return fmt.Errorf("branch %q already exists; choose another with --branch", branch)
}

// emptyPick reports whether the stopped cherry-pick has no conflicts and nothing to commit.
func emptyPick(run func(...string) (string, error)) bool {
	if files, err := run("diff", "--name-only", "--diff-filter=U"); err != nil || files != "" {
		return false
	}
	_, err := run("diff", "--cached", "--quiet", "HEAD")
	return err == nil
}

// ContinueBackport finishes the backport whose conflicts were resolved on the checked-out branch:
// it refuses while the cherry-pick is still in progress and switches back to the branch the
// backport started from.
func ContinueBackport(ctx context.Context, runner Runner, dir string) (BackportResult, error) {
	run := func(args ...string) (string, error) { return runner.Output(ctx, dir, false, args...) }
	branch, err := CurrentBranch(ctx, runner, dir)
	if err != nil {
		return BackportResult{}, errors.New("pr backport --continue must run on the backport branch that gt pr backport --keep left")
	}
	get := func(key string) string {
		value, _ := run("config", "--get", "branch."+branch+"."+key)
		return value
	}
	number, err := strconv.ParseInt(get(backportNumberKey), 10, 64)
	target, onto, from := get(backportTargetKey), get(backportOntoKey), get(backportFromKey)
	if err != nil || number < 1 || target == "" || onto == "" {
		return BackportResult{}, fmt.Errorf("branch %s is not a backport branch; pr backport --continue must run on the branch that gt pr backport --keep left", branch)
	}
	if err := refuseCherryPickInProgress(run); err != nil {
		return BackportResult{}, err
	}
	return finishBackport(run, branch, BackportResult{Target: target, Branch: branch, Picked: strings.Fields(get(backportPickedKey)), Number: number}, onto, from)
}

// RecordBackportPull records the pull request a backport branch opened, so that pr commands on the
// branch select it, and forgets the backport state.
func RecordBackportPull(ctx context.Context, runner Runner, dir, branch string, number int64) error {
	for _, key := range []string{backportNumberKey, backportTargetKey, backportOntoKey, backportFromKey, backportPickedKey} {
		_, _ = runner.Output(ctx, dir, false, "config", "--unset", "branch."+branch+"."+key)
	}
	_, err := runner.Output(ctx, dir, false, "config", "branch."+branch+"."+pullNumberKey, strconv.FormatInt(number, 10))
	return err
}

func finishBackport(run func(...string) (string, error), branch string, result BackportResult, onto, from string) (BackportResult, error) {
	commits, err := run("rev-list", "--reverse", onto+".."+"refs/heads/"+branch)
	if err != nil {
		return BackportResult{}, err
	}
	if commits == "" {
		return BackportResult{}, fmt.Errorf("branch %s has no commits beyond %s; nothing to backport", branch, result.Target)
	}
	result.Commits = strings.Split(commits, "\n")
	if from != "" {
		if _, err := run(switchBack(from)...); err != nil {
			return BackportResult{}, fmt.Errorf("backported to branch %s, but could not switch back to %s: %w", branch, from, err)
		}
	}
	return result, nil
}

// landedCommits returns, oldest first, the commits that merging a pull request with head head left
// on the base branch, ending at merge.
func landedCommits(run func(...string) (string, error), merge, mergeBase, head string) ([]string, error) {
	parents, err := run("rev-list", "--parents", "--max-count=1", merge)
	if err != nil {
		return nil, fmt.Errorf("could not read the merge commit %s: %w", shortCommit(merge), err)
	}
	if len(strings.Fields(parents)) > 2 {
		merged, err := run("rev-list", "--reverse", "--no-merges", merge+"^1.."+merge)
		if err != nil || merged == "" {
			return nil, fmt.Errorf("could not list the commits the merge commit %s merged", shortCommit(merge))
		}
		return strings.Split(merged, "\n"), nil
	}
	if mergeBase == "" {
		return []string{merge}, nil
	}
	pulled, err := authoredCommits(run, "--no-merges", mergeBase+".."+head)
	if err != nil || len(pulled) < 2 {
		return []string{merge}, nil
	}
	landed, err := authoredCommits(run, "--first-parent", fmt.Sprintf("--max-count=%d", len(pulled)), merge)
	if err != nil {
		return []string{merge}, nil
	}
	// A rebase drops the commits whose changes the base branch already has, so the run can lack
	// some of the pull request's commits.
	var commits []string
	next := len(pulled) - 1
	for i := len(landed) - 1; i >= 0; i-- {
		for next >= 0 && pulled[next][1] != landed[i][1] {
			next--
		}
		if next < 0 {
			break
		}
		commits = append(commits, landed[i][0])
		next--
	}
	if len(commits) == 0 {
		return []string{merge}, nil
	}
	slices.Reverse(commits)
	return commits, nil
}

// authoredCommits lists commits oldest first as pairs of the commit and its author, author date and
// subject, which a rebase keeps and a squash does not.
func authoredCommits(run func(...string) (string, error), args ...string) ([][2]string, error) {
	output, err := run(append([]string{"rev-list", "--reverse", "--format=%an <%ae> %at %s"}, args...)...)
	if err != nil {
		return nil, err
	}
	var commits [][2]string
	for line := range strings.SplitSeq(output, "\n") {
		if commit, ok := strings.CutPrefix(line, "commit "); ok {
			commits = append(commits, [2]string{commit, ""})
		} else if len(commits) > 0 {
			commits[len(commits)-1][1] = line
		}
	}
	return commits, nil
}

func refuseCherryPickInProgress(run func(...string) (string, error)) error {
	picking, err := cherryPickInProgress(run)
	if err != nil {
		return err
	}
	if picking {
		return errors.New("a cherry-pick is in progress; finish it with git cherry-pick --continue, or drop it with git cherry-pick --abort")
	}
	return nil
}

// cherryPickInProgress also sees a stopped multi-commit cherry-pick, which keeps its sequencer
// directory after its CHERRY_PICK_HEAD is gone.
func cherryPickInProgress(run func(...string) (string, error)) (bool, error) {
	if head, err := optional(run("rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")); err != nil || head != "" {
		return head != "", err
	}
	sequencer, err := run("rev-parse", "--path-format=absolute", "--git-path", "sequencer")
	if err != nil {
		return false, err
	}
	_, err = os.Stat(sequencer)
	return err == nil, nil
}

func switchBack(from string) []string {
	if isCommitID(from) {
		return []string{"switch", "--quiet", "--detach", from}
	}
	return []string{"switch", "--quiet", from}
}

func isCommitID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

func shortCommit(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}
