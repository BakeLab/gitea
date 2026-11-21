// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

// activityPhrases say what an entry of a feed did, by the type that Gitea gives it.
var activityPhrases = map[string]string{
	"create_repo": "created the repository", "rename_repo": "renamed the repository", "star_repo": "starred", "watch_repo": "watched",
	"commit_repo": "pushed to", "create_issue": "opened issue", "create_pull_request": "opened pull request", "transfer_repo": "transferred",
	"push_tag": "pushed the tag", "comment_issue": "commented on issue", "comment_pull": "commented on pull request",
	"merge_pull_request": "merged pull request", "close_issue": "closed issue", "reopen_issue": "reopened issue",
	"close_pull_request": "closed pull request", "reopen_pull_request": "reopened pull request", "delete_tag": "deleted the tag",
	"delete_branch": "deleted the branch", "mirror_sync_push": "mirrored a push to", "mirror_sync_create": "mirrored a new ref to",
	"mirror_sync_delete": "mirrored the deletion of a ref in", "approve_pull_request": "approved pull request",
	"reject_pull_request": "requested changes on pull request", "publish_release": "published the release",
	"pull_review_dismissed": "dismissed a review of pull request", "pull_request_ready_for_review": "marked as ready for review pull request",
	"auto_merge_pull_request": "scheduled the merge of pull request",
}

// activityPhrase says what an entry did: "opened issue", or its type with spaces for one that is new.
func activityPhrase(item services.Activity) string {
	if phrase, ok := activityPhrases[item.Type]; ok {
		return phrase
	}
	return strings.ReplaceAll(item.Type, "_", " ")
}

// activityRef is the branch or tag of an entry, without its refs/heads/ or refs/tags/.
func activityRef(item services.Activity) string {
	ref := strings.TrimPrefix(item.Ref, "refs/heads/")
	return strings.TrimPrefix(ref, "refs/tags/")
}

// activityDetail is what an entry says besides what was done: "#12 Crash on start" for an issue or
// pull request, the comment for a comment, "2 commits to main" and the newest subject for a push.
func activityDetail(item services.Activity) string {
	switch item.Type {
	case "commit_repo", "mirror_sync_push":
		var push struct {
			Commits []struct{ Message string }
			Len     int
		}
		ref := activityRef(item)
		if json.Unmarshal([]byte(item.Content), &push) != nil {
			return ref
		}
		count := max(len(push.Commits), push.Len)
		if count == 0 {
			return ref
		}
		detail := fmt.Sprintf("%s to %s", terminal.Plural(count, "commit"), ref)
		if len(push.Commits) > 0 {
			if subject, _, _ := strings.Cut(push.Commits[0].Message, "\n"); subject != "" {
				detail += ": " + subject
			}
		}
		return detail
	case "push_tag", "delete_tag", "delete_branch", "mirror_sync_create", "mirror_sync_delete":
		return activityRef(item)
	case "publish_release":
		return strings.TrimSpace(item.Content + " (" + activityRef(item) + ")")
	}
	number, text, ok := strings.Cut(item.Content, "|")
	if !ok || strings.Trim(number, "0123456789") != "" {
		return item.Content
	}
	detail, separator := "#"+number, " "
	switch {
	case item.Type == "pull_review_dismissed":
		reviewer, reason, _ := strings.Cut(text, "|")
		if reviewer != "" {
			detail += " by " + reviewer
		}
		text, separator = reason, ": "
	case strings.HasPrefix(item.Type, "comment_"):
		separator = ": "
	}
	if text = strings.TrimSpace(text); text != "" {
		detail += separator + text
	}
	return detail
}

// writeActivity prints a page of a feed as a table, or as JSON.
func (a *application) writeActivity(cmd *cobra.Command, page services.Page[services.Activity], number int, target string) error {
	return writeTable(a, cmd, page.Items, listSpec[services.Activity]{
		Columns: []terminal.Column{{Header: "When"}, {Header: "Who"}, {Header: "What"}, {Header: "Repository"}, {Header: "Detail", Flex: true}},
		Row: func(item services.Activity) []terminal.Cell {
			return []terminal.Cell{
				{Time: terminal.ParseTime(item.CreatedAt)},
				{Text: terminal.SafeLine(item.Actor), Bold: true},
				{Text: activityPhrase(item)},
				{Text: terminal.SafeLine(item.Repository)},
				{Text: terminal.SafeLine(activityDetail(item)), Muted: true},
			}
		},
		Context: "activity of " + target, Empty: "no activity for " + target,
	}, servicePage(page, number))
}

// checkActivityDate refuses a --date that is not a day, before any request.
func checkActivityDate(date string) error {
	if date == "" {
		return nil
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--date requires a day as YYYY-MM-DD, got %q", date))
	}
	return nil
}

func (a *application) activityCommand() *cobra.Command {
	var limit, page int
	var date string
	var onlyPerformedBy bool
	cmd := &cobra.Command{
		Use: "activity [<owner>]", Short: "Show the recent activity of a user or organization", Args: nonBlankArgs(cobra.MaximumNArgs(1), "owner"),
		Long: `Show the activity feed of a user or organization, newest first: who opened or closed an issue,
commented, pushed, merged, published a release. The feed of a user holds what they did and what happened
in repositories they watch, and that of an organization what happened in its repositories. Without an
owner it is your own feed. --only-performed-by keeps what the user did, and cannot be combined with an
organization. --date keeps one day, in the server's time zone. For the activity of one repository, see
gt repo activity.`,
		Example: `$ gt activity
$ gt activity acme --date 2026-10-11
$ gt activity alice --only-performed-by --json type,repository,created_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.rejectRepoFlag(cmd, func(string) string { return "gt repo activity -R " + a.options.repo }); err != nil {
				return err
			}
			if err := checkActivityDate(date); err != nil {
				return err
			}
			scope := services.ActivityScope{Date: date, OnlyPerformedBy: onlyPerformedBy}
			if len(args) > 0 {
				scope.Owner = args[0]
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			feed, err := (services.Activities{Client: target.client}).ListPage(cmd.Context(), scope, limit, page)
			if errors.Is(err, services.ErrOrganizationFeedPerformedBy) {
				return clierr.Wrap(clierr.Usage, errors.New("--only-performed-by cannot be combined with an organization"))
			}
			if err != nil {
				return err
			}
			who := "your account"
			if scope.Owner != "" {
				who = terminal.SafeLine(scope.Owner)
			}
			return a.writeActivity(cmd, feed, page, who)
		},
	}
	pageFlags(cmd, &limit, &page)
	cmd.Flags().StringVar(&date, "date", "", "Only the activity of this `day` in the server's time zone, as YYYY-MM-DD")
	cmd.Flags().BoolVar(&onlyPerformedBy, "only-performed-by", false, "Only what the user did, not what happened in repositories they watch")
	cmd.ValidArgsFunction = a.completeOrgs()
	return jsonOutput[services.Activity](cmd)
}

func (a *application) repoActivityCommand() *cobra.Command {
	var limit, page int
	var date string
	cmd := &cobra.Command{
		Use: "activity [<repository>]", Short: "Show the recent activity of a repository", Args: nonBlankArgs(cobra.MaximumNArgs(1), "repository"),
		Long: `Show what happened lately in a repository, newest first: issues and pull requests opened, closed and
commented on, pushes, tags and releases. --date keeps one day, in the server's time zone.`,
		Example: `$ gt repo activity
$ gt repo activity acme/project --date 2026-10-11 --json type,actor,created_at`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			if err := checkActivityDate(date); err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			feed, err := (services.Activities{Client: target.client}).ListPage(cmd.Context(), services.ActivityScope{Repository: target.Repo, Date: date}, limit, page)
			if err != nil {
				return err
			}
			return a.writeActivity(cmd, feed, page, target.Repo)
		},
	}
	pageFlags(cmd, &limit, &page)
	cmd.Flags().StringVar(&date, "date", "", "Only the activity of this `day` in the server's time zone, as YYYY-MM-DD")
	cmd.ValidArgsFunction = a.completeRepos()
	return jsonOutput[services.Activity](cmd)
}
