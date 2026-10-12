// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func (a *application) prCommand() *cobra.Command {
	group := &cobra.Command{Use: "pr", Short: "Manage pull requests", Long: `Work with Gitea pull requests: list, view, create, review, check out and merge them.

Commands that act on one pull request take a number, a URL or a branch name, and without
one select the pull request of the current branch.`}
	group.Annotations = pullArgumentsAnnotation(nil)
	group.Example = `$ gt pr list --author @me
$ gt pr create --fill
$ gt pr checkout 13
$ gt pr review --approve
$ gt pr merge --squash --delete-branch`
	group.AddCommand(a.pullChecksCommand(), a.pullCheckoutCommand(), a.pullCleanCommand(), a.statusCommand(true))
	group.AddCommand(a.pullListCommand(), a.pullViewCommand())
	a.addPullMutations(group)
	group.AddCommand(a.pullEditCommand(), a.pullReadyCommand())
	group.AddCommand(a.issueAttachmentCommand(), a.commentCommand())
	return group
}

func (a *application) pullListCommand() *cobra.Command {
	var limit, page int
	var state, milestone string
	var options services.PullListOptions
	var labels []string
	var web bool
	cmd := &cobra.Command{Use: "list", Short: "List pull requests", Long: `List the pull requests of a repository, newest first.

The reviewers, reviewer_teams and merged_by JSON fields come from separate requests for
each pull request, which gt only makes when --json names them.`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if id, err := strconv.ParseInt(milestone, 10, 64); err == nil && id < 1 {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a milestone title or a positive ID"))
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
			if options.Milestone, err = milestoneFilter(cmd.Context(), t, milestone); err != nil {
				return err
			}
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		window, err := service.ListPage(cmd.Context(), options)
		if err != nil {
			return err
		}
		items := window.Items
		if a.jsonSelects("reviewers", "reviewer_teams", "merged_by") {
			for i, item := range items {
				if items[i], err = service.View(cmd.Context(), item.Number); err != nil {
					return err
				}
			}
		}
		noun := map[string]string{"open": "open pull requests", "closed": "closed pull requests", "merged": "merged pull requests", "all": "pull requests"}[state]
		what := noun + " in " + t.Repo
		empty := "no " + what
		if filtered(cmd, "state", "limit", "page", "sort") {
			empty = "no " + noun + " match your filters in " + t.Repo
		}
		columns := []terminal.Column{{Header: "ID", Prefix: "#"}, {Header: "Title", Flex: true}, {Header: "Branch", Flex: true, Min: 12}, {Header: "State", PipedOnly: state == "open"}, {Header: "Author"}, {Header: "Created"}}
		return writeTable(a, cmd, items, listSpec[services.Pull]{
			Columns: columns,
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
		}, listPage{Number: page, More: window.More, Total: window.Total, Counted: window.Counted})
	}}
	enumFlag(cmd, &state, "state", "s", "open", "Filter by state", "open", "closed", "merged", "all")
	pageFlags(cmd, &limit, &page)
	cmd.Flags().StringVarP(&options.Author, "author", "A", "", "Filter by author; @me for yourself")
	cmd.Flags().StringVarP(&options.Assignee, "assignee", "a", "", "Filter by assignee; @me for yourself")
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
$ gt pr list -l bug -B main --draft
$ gt pr list --state merged -L 10
$ gt pr list --json number,title,head --jq '.[] | select(.head == "feature/login")'`
	return cmd
}

// changedFlags lists the names of the command's own flags that were given.
func changedFlags(cmd *cobra.Command) []string {
	var names []string
	cmd.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Changed {
			names = append(names, flag.Name)
		}
	})
	return names
}

// filtered reports whether a flag other than those named, which do not filter, was given.
func filtered(cmd *cobra.Command, unfiltered ...string) bool {
	return slices.ContainsFunc(changedFlags(cmd), func(name string) bool { return !slices.Contains(unfiltered, name) })
}

// jsonSelects reports whether --json names one of fields, rather than selecting every field.
func (a *application) jsonSelects(fields ...string) bool {
	for field := range strings.SplitSeq(a.options.json, ",") {
		if slices.Contains(fields, strings.TrimSpace(field)) {
			return true
		}
	}
	return false
}

// repositoryURL returns the web URL of the target's repository.
func repositoryURL(t target) string {
	return strings.TrimSuffix(t.Host, "/") + "/" + t.Repo
}

// milestoneFilter resolves a milestone given as an ID or a title (D10): a number is an ID first,
// then a title.
func milestoneFilter(ctx context.Context, t target, raw string) (int64, error) {
	service := services.Milestones{Client: t.client, Repository: t.Repo}
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
		_, err := service.View(ctx, id)
		if err == nil {
			return id, nil
		}
		if apiErr, ok := errors.AsType[*transport.APIError](err); !ok || apiErr.Status != http.StatusNotFound {
			return 0, err
		}
	}
	return service.ResolveName(ctx, raw)
}

func (a *application) pullViewCommand() *cobra.Command {
	var web, comments bool
	cmd := &cobra.Command{Use: "view [<number> | <url> | <branch>]", Short: "View a pull request", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelector(cmd, args)
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
		item.ReviewDecision = services.ReviewDecision(services.LatestReviews(details.reviews), len(item.Reviewers)+len(item.ReviewerTeams) > 0)
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
	cmd.ValidArgsFunction = a.completePulls("all")
	cmd.Example = `# View the pull request of the current branch
$ gt pr view

# View #13 with its comments and reviews
$ gt pr view 13 --comments

$ gt pr view https://gitea.com/owner/repo/pulls/13 --web
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
