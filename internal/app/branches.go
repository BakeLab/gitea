// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// validateBranch rejects invalid Git refs before making requests.
func validateBranch(name string) error {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") || strings.ContainsAny(name, " ~^:?*[\\\t\r\n") {
		return clierr.Wrap(clierr.Usage, errors.New("invalid branch name"))
	}
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return clierr.Wrap(clierr.Usage, errors.New("invalid branch name"))
		}
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return clierr.Wrap(clierr.Usage, errors.New("invalid branch name"))
		}
	}
	return nil
}

// validateBranches validates every branch name.
func validateBranches(names []string) error {
	for _, name := range names {
		if err := validateBranch(name); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) branchCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "branch",
		Short: "Manage remote branches and protection rules",
		Long: `Work with the branches of a repository on the server: list and view them, create, rename and
delete them, and protect them from direct pushes. To work with local branches, use git.`,
	}
	group.AddCommand(a.branchListCommand(), a.branchViewCommand(), a.branchCreateCommand(), a.branchRenameCommand(), a.branchDeleteCommand())
	group.AddCommand(a.branchProtectCommand(), a.branchUnprotectCommand())
	return group
}

// branchURL links a branch's files on the web.
func branchURL(repository services.Repository, branch string) string {
	if repository.URL == "" {
		return ""
	}
	return repository.URL + "/src/branch/" + strings.ReplaceAll(url.PathEscape(branch), "%2F", "/")
}

func (a *application) branchListCommand() *cobra.Command {
	var limit, page int
	command := &cobra.Command{
		Use:   "list",
		Short: "List branches",
		Example: `$ gt branch list
$ gt branch list -R alice/project --limit 100 --json name,sha`,
		Args: argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			repository := services.Repository{}
			if a.options.json == "" {
				if repository, err = (services.Repositories{Client: target.client}).View(cmd.Context(), target.Repo); err != nil {
					return err
				}
			}
			result, err := (services.Branches{Client: target.client}).ListPage(cmd.Context(), target.Repo, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.Branch]{
				Columns: []terminal.Column{{Header: "Name"}, {Header: "Commit"}, {Header: "Protected"}, {Header: "Updated"}, {Header: "Message", Flex: true}},
				Row: func(item services.Branch) []terminal.Cell {
					name := terminal.Cell{Text: item.Name, Link: branchURL(repository, item.Name)}
					if item.Name == repository.DefaultBranch {
						name.Text += " (default)"
						name.Bold = true
					}
					protected := ""
					if item.Protected {
						protected = "protected"
					}
					message, _, _ := strings.Cut(item.Message, "\n")
					return []terminal.Cell{name, {Text: shortSHA(item.SHA), Muted: true}, {Text: protected, Color: "yellow"}, {Time: terminal.ParseTime(item.UpdatedAt)}, {Text: message}}
				},
				Context: "branches in " + target.Repo, Empty: "no branches in " + target.Repo,
			}
			return writeTable(a, cmd, result.Items, spec, servicePage(result, page))
		},
	}
	pageFlags(command, &limit, &page)
	return command
}

func (a *application) branchViewCommand() *cobra.Command {
	var web bool
	command := &cobra.Command{
		Use:     "view <branch>",
		Short:   "View a branch",
		Example: "$ gt branch view main\n$ gt branch view feature/login --web",
		Args:    argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBranch(args[0]); err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			repository, err := (services.Repositories{Client: target.client}).View(cmd.Context(), target.Repo)
			if err != nil {
				return err
			}
			if web {
				return a.openBrowser(cmd, branchURL(repository, args[0]))
			}
			item, err := (services.Branches{Client: target.client}).View(cmd.Context(), target.Repo, args[0])
			if err != nil {
				return err
			}
			return writeView(a, cmd, item, func(item services.Branch) terminal.View {
				var badges []terminal.Cell
				if item.Name == repository.DefaultBranch {
					badges = append(badges, terminal.Cell{Text: "default", Color: "green"})
				}
				protection := ""
				if item.Protected {
					badges = append(badges, terminal.Cell{Text: "protected", Color: "yellow"})
					protection = terminal.Plural(int(item.RequiredApprovals), "required approval")
				}
				meta := ""
				if updated := terminal.ParseTime(item.UpdatedAt); !updated.IsZero() {
					meta = "updated " + terminal.RelativeTime(a.deps.Now(), updated)
				}
				return terminal.View{
					Title: item.Name, Badges: badges, Meta: meta, Body: item.Message, Kind: "branch", URL: branchURL(repository, item.Name),
					Fields: []terminal.Field{{Label: "Commit", Value: item.SHA}, {Label: "Protection", Value: protection}, {Label: "You can push", Value: yesNo(item.CanPush)}},
				}
			})
		},
	}
	command.Flags().BoolVarP(&web, "web", "w", false, "Open the branch in the browser")
	command.ValidArgsFunction = a.completeBranches()
	return command
}

func (a *application) branchCreateCommand() *cobra.Command {
	var source string
	command := &cobra.Command{
		Use:   "create <branch>",
		Short: "Create a branch",
		Long:  "Create a branch on the server from another branch, a tag or a commit; the default branch unless --source names one.",
		Example: `# Branch off the default branch
$ gt branch create feature/login

# Branch off a tag or a commit
$ gt branch create hotfix/1.0.1 --source v1.0.0
$ gt branch create bisect --source 3f2a9c1`,
		Args: argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBranch(args[0]); err != nil {
				return err
			}
			if cmd.Flags().Changed("source") && strings.TrimSpace(source) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--source cannot be empty"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			item, err := (services.Branches{Client: target.client}).Create(cmd.Context(), target.Repo, args[0], source)
			if err != nil {
				return err
			}
			if source == "" {
				a.done(cmd, "Created branch %s in %s at %s", item.Name, target.Repo, shortSHA(item.SHA))
			} else {
				a.done(cmd, "Created branch %s from %s in %s at %s", item.Name, source, target.Repo, shortSHA(item.SHA))
			}
			return a.jsonResult(cmd, item)
		},
	}
	command.Flags().StringVarP(&source, "source", "s", "", "Branch, tag or commit to start from (default: the default branch)")
	_ = command.RegisterFlagCompletionFunc("source", mergeCompletions(a.completeBranches(), a.completeTags()))
	return command
}

func (a *application) branchRenameCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "rename <branch> <new-name>",
		Short:       "Rename a branch",
		Example:     "$ gt branch rename feature/logn feature/login",
		Annotations: textOutput("branch rename does not return a resource"),
		Args:        argsExact(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBranches(args); err != nil {
				return err
			}
			if args[0] == args[1] {
				return clierr.Wrap(clierr.Usage, errors.New("new branch name must differ from the current name"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			if err := (services.Branches{Client: target.client}).Rename(cmd.Context(), target.Repo, args[0], args[1]); err != nil {
				return err
			}
			a.done(cmd, "Renamed branch %s to %s in %s", args[0], args[1], target.Repo)
			return nil
		},
	}
	command.ValidArgsFunction = a.completeBranches()
	return command
}

func (a *application) branchDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete <branch>...",
		Short: "Delete branches",
		Example: `$ gt branch delete feature/login
$ gt branch delete old-a old-b --yes`,
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "branch deletion does not return a resource"},
		Args:        usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBranches(args); err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			subject := branchSubject(args)
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + " from " + target.Repo + "?"}); err != nil {
				return err
			}
			service := services.Branches{Client: target.client}
			return batchDone(a, cmd, args, func(name string) string { return "delete branch " + name }, func(name string) (string, error) {
				return name, service.Delete(cmd.Context(), target.Repo, name)
			}, func(name string) string { return "Deleted branch " + name + " from " + target.Repo })
		},
	}
	command.ValidArgsFunction = a.completeBranches()
	return command
}

// branchSubject names one or more branches in a prompt: "branch main" or "2 branches (a, b)".
func branchSubject(names []string) string {
	if len(names) == 1 {
		return "branch " + names[0]
	}
	return fmt.Sprintf("%d branches (%s)", len(names), strings.Join(names, ", "))
}

func (a *application) branchProtectCommand() *cobra.Command {
	var rule services.Protection
	var pattern string
	var pushers []string
	command := &cobra.Command{
		Use:   "protect <branch>...",
		Short: "Protect branches",
		Long: `Add a protection rule for each branch. By default nobody can push to a protected branch directly
and pull requests need one approving review to merge; --allow-push lets users or teams push, and
--required-approvals changes the review count.

The branches must exist. --pattern protects every branch that matches a glob instead, such as
release/*, including branches created later.`,
		Example: `# Require two approvals and passing CI on main
$ gt branch protect main --required-approvals 2 --status-check ci/build

# Let the release team push to every release branch
$ gt branch protect --pattern 'release/*' --allow-push acme/release`,
		Annotations: textOutput("branch protect does not return a resource"),
		Args: func(cmd *cobra.Command, args []string) error {
			if pattern != "" {
				return usageArgs(cobra.NoArgs)(cmd, args)
			}
			return usageArgs(cobra.MinimumNArgs(1))(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBranches(args); err != nil {
				return err
			}
			if rule.RequiredApprovals < 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--required-approvals cannot be negative"))
			}
			if err := requireNames("push", "", pushers, rule.StatusChecks); err != nil {
				return err
			}
			for _, pusher := range pushers {
				if _, team, ok := strings.Cut(pusher, "/"); ok {
					rule.PushTeams = append(rule.PushTeams, team)
				} else {
					rule.PushUsers = append(rule.PushUsers, pusher)
				}
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Branches{Client: target.client}
			rules := args
			if pattern != "" {
				rules = []string{pattern}
			}
			_, failures := applyBatch(cmd, rules, func(name string) string { return "protect " + name }, func(name string) (string, error) {
				if pattern == "" {
					if _, err := service.View(cmd.Context(), target.Repo, name); err != nil {
						return "", services.Classify(err, "branch", strconv.Quote(name), target.Repo)
					}
				}
				protection := rule
				protection.Rule = name
				created, err := service.Protect(cmd.Context(), target.Repo, protection)
				if err == nil && !created {
					a.warn(cmd, "%s is already protected in %s; change its rule on the web", name, target.Repo)
					return "", nil
				}
				return name, err
			}, func(name string) {
				if name != "" {
					a.done(cmd, "Protected %s in %s (%s)", name, target.Repo, protectionSummary(rule))
				}
			})
			return errors.Join(failures...)
		},
	}
	flags := command.Flags()
	flags.StringVar(&pattern, "pattern", "", "Protect the branches matching this glob, such as release/*")
	flags.Int64Var(&rule.RequiredApprovals, "required-approvals", 1, "Approving reviews that pull requests need")
	flags.StringSliceVar(&pushers, "allow-push", nil, "Users, or teams as ORG/TEAM, that may push directly")
	flags.StringSliceVar(&rule.StatusChecks, "status-check", nil, "Status checks that must pass (repeatable)")
	flags.BoolVar(&rule.BlockOnRejected, "block-on-rejected-reviews", false, "Block merging while changes are requested")
	flags.BoolVar(&rule.DismissStaleApprovals, "dismiss-stale-approvals", false, "Dismiss approvals when new commits are pushed")
	_ = command.RegisterFlagCompletionFunc("allow-push", a.completeUsers())
	command.ValidArgsFunction = a.completeBranches()
	return command
}

// protectionSummary describes a protection rule, as in "1 approval required, no direct pushes".
func protectionSummary(rule services.Protection) string {
	parts := []string{terminal.Plural(int(rule.RequiredApprovals), "approval") + " required"}
	if pushers := len(rule.PushUsers) + len(rule.PushTeams); pushers > 0 {
		parts = append(parts, "pushes by "+strings.Join(append(append([]string{}, rule.PushUsers...), rule.PushTeams...), ", "))
	} else {
		parts = append(parts, "no direct pushes")
	}
	if len(rule.StatusChecks) > 0 {
		parts = append(parts, "checks "+strings.Join(rule.StatusChecks, ", "))
	}
	return strings.Join(parts, ", ")
}

func (a *application) branchUnprotectCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "unprotect <branch>...",
		Short:       "Remove branch protection",
		Long:        "Remove the protection rules of branches, or of glob patterns such as release/*.",
		Example:     "$ gt branch unprotect main\n$ gt branch unprotect 'release/*' --yes",
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "branch unprotect does not return a resource"},
		Args:        usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			subject := "the protection of " + strings.Join(args, ", ")
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Remove " + subject + " in " + target.Repo + "?"}); err != nil {
				return err
			}
			service := services.Branches{Client: target.client}
			_, failures := applyBatch(cmd, args, func(name string) string { return "unprotect " + name }, func(name string) (string, error) {
				removed, err := service.Unprotect(cmd.Context(), target.Repo, name)
				if err == nil && !removed {
					a.warn(cmd, "%s is not protected in %s", name, target.Repo)
					return "", nil
				}
				return name, err
			}, func(name string) {
				if name != "" {
					a.done(cmd, "Unprotected %s in %s", name, target.Repo)
				}
			})
			return errors.Join(failures...)
		},
	}
	command.ValidArgsFunction = a.completeBranches()
	return command
}
