// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

// ruleWho words who a rule lets do something: the users, then the teams, as "alice, team release".
func ruleWho(users, teams []string) string {
	names := append([]string{}, users...)
	for _, team := range teams {
		names = append(names, "team "+team)
	}
	return strings.Join(names, ", ")
}

// ruleAllowlist words who is on an allowlist of a rule, among whom deployKeys adds the deploy keys.
func ruleAllowlist(users, teams []string, deployKeys bool) string {
	who := ruleWho(users, teams)
	if deployKeys {
		who = strings.Trim(who+", deploy keys", ", ")
	}
	return cmp.Or(who, "nobody")
}

// rulePushers words who may push directly to the branches of a rule.
func rulePushers(rule services.BranchRule) string {
	switch {
	case !rule.PushEnabled:
		return "nobody"
	case !rule.PushRestricted:
		return "everyone with write access"
	}
	return ruleAllowlist(rule.PushUsers, rule.PushTeams, rule.PushDeployKeys)
}

// ruleForcePushers words who may force push to the branches of a rule that allows it.
func ruleForcePushers(rule services.BranchRule) string {
	if !rule.ForcePushRestricted {
		return "everyone who may push"
	}
	return ruleAllowlist(rule.ForcePushUsers, rule.ForcePushTeams, rule.ForcePushDeployKeys)
}

// ruleMergers words who may merge pull requests into the branches of a rule.
func ruleMergers(rule services.BranchRule) string {
	if !rule.MergeRestricted {
		return "everyone with write access"
	}
	return cmp.Or(ruleWho(rule.MergeUsers, rule.MergeTeams), "nobody")
}

// ruleChecks words the status checks that must pass before pull requests merge.
func ruleChecks(rule services.BranchRule) string {
	if rule.StatusCheckRequired && len(rule.StatusChecks) == 0 {
		return "all reported checks"
	}
	return strings.Join(rule.StatusChecks, ", ")
}

// ruleNotes lists the other protections of a rule that are on, briefly.
func ruleNotes(rule services.BranchRule) string {
	var notes []string
	for _, note := range []struct {
		on   bool
		text string
	}{
		{rule.PushEnabled && rule.ForcePush, "force push allowed"}, // Gitea requires being allowed to push as well
		{rule.BlockOnRejected, "blocks on rejected reviews"},
		{rule.BlockOnOfficialReviewRequests, "blocks on official review requests"},
		{rule.BlockOnOutdatedBranch, "blocks outdated branches"},
		{rule.DismissStaleApprovals, "dismisses stale approvals"},
		{rule.IgnoreStaleApprovals, "ignores stale approvals"},
		{rule.RequireSignedCommits, "signed commits"},
		{rule.BlockAdminMergeOverride, "no admin override"},
	} {
		if note.on {
			notes = append(notes, note.text)
		}
	}
	return strings.Join(notes, ", ")
}

// completeBranchRules completes the protection rules of the repository.
func (a *application) completeBranchRules() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		rules, err := (services.Branches{Client: t.client}).Protections(ctx, t.Repo)
		return describe(rules, func(rule services.BranchRule) (string, string) { return rule.Rule, "" }), err
	})
}

func (a *application) branchProtectionCommand() *cobra.Command {
	group := &cobra.Command{
		Use: "protection", Short: "List and view branch protection rules",
		Long: `Read the protection rules of a repository, which requires admin access to it. gt branch protect
adds and changes rules, and gt branch unprotect removes them.`,
	}
	group.AddCommand(a.branchProtectionListCommand(), a.branchProtectionViewCommand())
	return jsonOutput[services.BranchRule](group)
}

func (a *application) branchProtectionListCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List the branch protection rules", Args: argsExact(0),
		Long: `List the protection rules of a repository, in the order Gitea applies them: the branch or pattern each
protects, the approvals pull requests need, who may push directly and the status checks that must
pass. A branch is protected by the first rule that matches it. This requires admin access to the
repository. gt branch protection view shows one rule in full.`,
		Example: `$ gt branch protection list
$ gt branch protection list --json rule,required_approvals,status_check_required,status_checks`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			rules, err := (services.Branches{Client: target.client}).Protections(cmd.Context(), target.Repo)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, rules, listSpec[services.BranchRule]{
				Columns: []terminal.Column{{Header: "Rule"}, {Header: "Approvals"}, {Header: "Push"}, {Header: "Checks", Flex: true}, {Header: "Also"}},
				Row: func(rule services.BranchRule) []terminal.Cell {
					return []terminal.Cell{
						{Text: terminal.SafeLine(rule.Rule), Bold: true},
						{Text: strconv.FormatInt(rule.RequiredApprovals, 10)},
						{Text: terminal.SafeLine(rulePushers(rule))},
						{Text: terminal.SafeLine(ruleChecks(rule))},
						{Text: terminal.SafeLine(ruleNotes(rule)), Muted: true},
					}
				},
				Context: "protection rules of " + target.Repo, Empty: "no protection rules in " + target.Repo,
			}, listPage{Number: 1})
		},
	}
}

func (a *application) branchProtectionViewCommand() *cobra.Command {
	return &cobra.Command{
		Use: "view <rule>", Short: "View a branch protection rule", Args: nonBlankArgs(cobra.ExactArgs(1), "rule"),
		Long: `View a protection rule, which requires admin access to the repository. The rule is named as
gt branch protection list lists it: a branch, or the pattern that was protected, such as release/*.`,
		Example: `$ gt branch protection view main
$ gt branch protection view 'release/*' --json push_users,push_teams`,
		Annotations:       map[string]string{"gt.arguments": repositoryArguments},
		ValidArgsFunction: firstArg(a.completeBranchRules()),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			rule, err := (services.Branches{Client: target.client}).Protection(cmd.Context(), target.Repo, args[0])
			if err != nil {
				return err
			}
			d := a.display()
			return writeView(a, cmd, rule, func(rule services.BranchRule) terminal.View {
				fields := []terminal.Field{
					{Label: "Required approvals", Value: strconv.FormatInt(rule.RequiredApprovals, 10)},
					{Label: "May push", Value: rulePushers(rule)},
					{Label: "May merge", Value: ruleMergers(rule)},
					{Label: "Status checks", Value: ruleChecks(rule)},
				}
				if rule.ApprovalsRestricted {
					fields = append(fields, terminal.Field{Label: "Approvals count from", Value: cmp.Or(ruleWho(rule.ApprovalUsers, rule.ApprovalTeams), "nobody")})
				}
				if rule.PushEnabled && rule.ForcePush {
					fields = append(fields, terminal.Field{Label: "May force push", Value: ruleForcePushers(rule)})
				}
				fields = append(fields,
					terminal.Field{Label: "Also", Value: ruleNotes(rule)},
					terminal.Field{Label: "Protected files", Value: rule.ProtectedFilePatterns},
					terminal.Field{Label: "Unprotected files", Value: rule.UnprotectedFilePatterns},
					timeField("Created", rule.CreatedAt, d), timeField("Updated", rule.UpdatedAt, d))
				return terminal.View{Title: rule.Rule, Fields: fields, Bodyless: true, Kind: "protection rule", URL: repositoryURL(target) + "/settings/branches"}
			})
		},
	}
}
