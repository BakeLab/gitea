// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) prCommand() *cobra.Command {
	group := &cobra.Command{Use: "pr", Short: "Manage pull requests", Long: `Work with Gitea pull requests: list, view, create, review, check out and merge them.

Commands that act on one pull request take a number, a URL or a branch name, and without
one select the pull request of the current branch. pr attachment takes a number or a URL.`}
	group.Annotations = pullArgumentsAnnotation(nil)
	group.Example = `$ gt pr list --author @me
$ gt pr create --fill
$ gt pr merge --squash --delete-branch`
	group.AddCommand(a.pullChecksCommand(), a.pullCheckoutCommand(), a.pullBackportCommand(), a.pullCleanCommand(), a.statusCommand(true))
	group.AddCommand(a.pullListCommand(), a.pullViewCommand())
	a.addPullMutations(group)
	group.AddCommand(a.pullEditCommand(), a.pullReadyCommand())
	group.AddCommand(a.issueAttachmentCommand(pullRef), a.commentCommand(pullRef), a.timelineCommand(pullRef))
	return jsonOutput(group, jsonFields[services.Pull]())
}

func (a *application) pullListCommand() *cobra.Command {
	var limit, page int
	var state, milestone string
	var options services.PullListOptions
	var labels []string
	var web bool
	cmd := &cobra.Command{Use: "list", Short: "List pull requests", Long: `List the pull requests of a repository, newest first.

The reviewers, reviewer_teams, reviews and review_decision JSON fields come from the reviews of each
pull request, and merged_by from reading each pull request again. gt only makes these requests when
--json names the fields, or selects every field and a --jq expression names them (see gt help
formatting). --limit bounds how many requests it makes.

review_decision is changes_requested, approved, review_required or empty. When the base branch's
protection requires approvals, the decision counts official approvals as Gitea does for merging:
fewer than required give review_required.`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if id, err := strconv.ParseInt(milestone, 10, 64); err == nil && id < 1 {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a milestone title or a positive ID"))
		}
		if options.NoAssignee && options.Assignee != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--assignee cannot be combined with --no-assignee"))
		}
		if web {
			if err := requireWebFlags(cmd, "state"); err != nil {
				return err
			}
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if web {
			link := repositoryURL(t) + "/pulls"
			switch state {
			case "closed", "merged":
				link += "?state=closed"
			case "all":
				link += "?state=all"
			}
			return a.openBrowser(cmd, link)
		}
		options.Author, options.Assignee = t.expandMe(options.Author), t.expandMe(options.Assignee)
		options.State, options.Limit, options.Page = state, limit, page
		if options.Labels, err = (services.Labels{Client: t.client, Repository: t.Repo}).ResolveIssueNames(cmd.Context(), labels); err != nil {
			return err
		}
		if milestone != "" {
			if options.Milestone, err = milestoneID(cmd, t, milestone); err != nil {
				return err
			}
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		window, err := service.ListPage(cmd.Context(), options)
		if err != nil {
			return err
		}
		items := window.Items
		view, reviews := a.jsonSelects("merged_by"), a.jsonSelects(pullReviewFields...)
		if view || reviews {
			if items, err = service.Detail(cmd.Context(), items, view, reviews); err != nil {
				return err
			}
		}
		noun := map[string]string{"open": "open pull requests", "closed": "closed pull requests", "merged": "merged pull requests", "all": "pull requests"}[state]
		what := noun + " in " + t.Repo
		empty := "no " + what
		if localFlagsChanged(cmd, "state", "limit", "page", "sort") {
			empty = "no " + noun + " match your filters in " + t.Repo
		}
		return writeTable(a, cmd, items, listSpec[services.Pull]{
			Columns: []terminal.Column{{Header: "ID", Prefix: "#"}, {Header: "Title", Flex: true}, {Header: "Branch", Flex: true, Min: 12}, {Header: "State", PipedOnly: state == "open"}, {Header: "Author"}, {Header: "Created"}},
			Row: func(item services.Pull) []terminal.Cell {
				state := pullState(item)
				return []terminal.Cell{
					{Text: strconv.FormatInt(item.Number, 10), Link: item.URL, Color: stateColors[state]},
					{Text: item.Title},
					{Text: pullHead(item, t.Repo)},
					{Text: state, Color: stateColors[state]},
					{Text: item.Author},
					{Time: terminal.ParseTime(item.CreatedAt)},
				}
			},
			Empty: empty, Context: what,
		}, servicePage(window, page))
	}}
	enumFlag(cmd, &state, "state", "s", "open", "Filter by state", "open", "closed", "merged", "all")
	pageFlags(cmd, &limit, &page)
	cmd.Flags().StringVarP(&options.Author, "author", "A", "", "Filter by author; @me for yourself")
	cmd.Flags().StringVarP(&options.Assignee, "assignee", "a", "", "Filter by assignee; @me for yourself")
	cmd.Flags().BoolVar(&options.NoAssignee, "no-assignee", false, "Only pull requests without an assignee")
	cmd.Flags().StringVarP(&options.Base, "base", "B", "", "Filter by base branch")
	cmd.Flags().StringVarP(&options.Head, "head", "H", "", "Filter by head branch, or OWNER:BRANCH for a fork")
	cmd.Flags().BoolVarP(&options.Draft, "draft", "d", false, "Show only drafts")
	cmd.Flags().StringVarP(&options.Search, "search", "S", "", "Filter by text in the title or body")
	cmd.Flags().StringSliceVarP(&labels, "label", "l", nil, "Filter by labels; repo: or org: picks between same-named labels")
	cmd.Flags().StringVarP(&milestone, "milestone", "m", "", "Filter by milestone title or ID")
	enumFlag(cmd, &options.Sort, "sort", "", "", "Order", "oldest", "recentupdate", "recentclose", "leastupdate", "mostcomment", "leastcomment", "priority")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the pull request list in the browser")
	_ = cmd.RegisterFlagCompletionFunc("author", a.completeUsers())
	_ = cmd.RegisterFlagCompletionFunc("assignee", a.completeUsers())
	_ = cmd.RegisterFlagCompletionFunc("base", a.completeBranches())
	_ = cmd.RegisterFlagCompletionFunc("head", a.completeBranches())
	_ = cmd.RegisterFlagCompletionFunc("label", a.completeLabels())
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	cmd.Example = `$ gt pr list
$ gt pr list --author @me --state all
$ gt pr list --json number,title,head --jq '.[] | select(.head == "feature/login")'`
	return cmd
}

// jsonSelects reports whether the JSON output includes one of fields: --json names it, or selects
// every field and --jq, if given, names it too, so that a filter that never reads it costs no
// requests.
func (a *application) jsonSelects(fields ...string) bool {
	if a.options.json == "*" {
		return a.options.jq == "" || slices.ContainsFunc(fields, func(field string) bool { return strings.Contains(a.options.jq, field) })
	}
	for field := range strings.SplitSeq(a.options.json, ",") {
		if slices.Contains(fields, strings.TrimSpace(field)) {
			return true
		}
	}
	return false
}

// pullReviewFields are the JSON fields of a pull request that come from its reviews.
var pullReviewFields = []string{"reviewers", "reviewer_teams", "reviews", "review_decision"}

// pullResult writes item as JSON with --json, after reading its reviews when the output includes
// fields that come from them.
func (a *application) pullResult(cmd *cobra.Command, t target, item services.Pull) error {
	if a.options.json == "" {
		return nil
	}
	item, err := a.pullWithReviews(cmd, t, item)
	if err != nil {
		return err
	}
	return a.writeJSON(cmd, item)
}

func (a *application) pullWithReviews(cmd *cobra.Command, t target, item services.Pull) (services.Pull, error) {
	if !a.jsonSelects(pullReviewFields...) {
		return item, nil
	}
	reviews, err := (services.Reviews{Client: t.client, Repository: t.Repo}).All(cmd.Context(), item.Number)
	if err != nil {
		return item, err
	}
	return (services.Pulls{Client: t.client, Repository: t.Repo}).WithReviews(cmd.Context(), item, reviews), nil
}

func (a *application) pullViewCommand() *cobra.Command {
	var web, comments bool
	cmd := &cobra.Command{Use: "view [<number> | <url> | <branch>]", Short: "View a pull request", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if comments && a.options.json != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--comments cannot be combined with --json or --jq; use gt pr comment list <number> --json or gt pr review list <number> --json"))
		}
		t, n, err := a.pullSelectorAny(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		item, err := service.View(cmd.Context(), n)
		if err != nil {
			return err
		}
		if web {
			return a.openBrowser(cmd, item.URL)
		}
		var details pullDetails
		if details.reviews, err = (services.Reviews{Client: t.client, Repository: t.Repo}).All(cmd.Context(), n); err != nil {
			return err
		}
		item = service.WithReviews(cmd.Context(), item, details.reviews)
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		if item.State == "open" && item.HeadSHA != "" {
			if details.checks, err = service.CommitChecks(cmd.Context(), item.HeadSHA); err != nil {
				return err
			}
		}
		return a.writeDiscussion(cmd, t, pullView(item, t.Repo, details), comments)
	}}
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the pull request in the browser")
	cmd.Flags().BoolVarP(&comments, "comments", "c", false, "Show comments and reviews")
	cmd.ValidArgsFunction = firstArg(a.completePulls("all"))
	cmd.Example = `# View the pull request of the current branch
$ gt pr view

# View #13 with its comments and reviews
$ gt pr view 13 --comments

$ gt pr view feature/login --json number,state,review_decision`
	return cmd
}

// pullState reports merged pull requests as merged, and open drafts as draft.
func pullState(item services.Pull) string {
	switch {
	case item.Merged:
		return "merged"
	case item.Draft && item.State == "open":
		return "draft"
	}
	return item.State
}
