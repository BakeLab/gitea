// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	// completionLimit caps how many values an instance completer fetches and offers.
	completionLimit = 50
	// completionTimeout bounds the Git and API calls behind one completion.
	completionTimeout = 2 * time.Second
	// enumAnnotation holds the values of an enum flag, which validation and completion share.
	enumAnnotation = "gt.enum"
)

// enumFlag registers a string flag that accepts one of values, case-insensitively, completes them,
// and lists them in its usage, as in "Filter by state {open|closed|all}".
func enumFlag(cmd *cobra.Command, p *string, name, shorthand, value, usage string, values ...string) {
	cmd.Flags().StringVarP(p, name, shorthand, value, usage+" {"+strings.Join(values, "|")+"}")
	enumValues(cmd, name, values...)
}

// enumValues restricts the values of the existing flag name, so that validation and completion share
// one list. A slice flag accepts and completes comma-separated values.
func enumValues(cmd *cobra.Command, name string, values ...string) {
	_ = cmd.Flags().SetAnnotation(name, enumAnnotation, values)
	complete := completeValues(values...)
	if _, ok := cmd.Flags().Lookup(name).Value.(pflag.SliceValue); ok {
		complete = commaSeparated(complete)
	}
	_ = cmd.RegisterFlagCompletionFunc(name, complete)
}

// validateEnums rejects enum flag values outside their lists and stores the listed spelling of each
// value, so that values match case-insensitively.
func validateEnums(cmd *cobra.Command) error {
	var err error
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		allowed := flag.Annotations[enumAnnotation]
		if err != nil || allowed == nil || !flag.Changed {
			return
		}
		slice, isSlice := flag.Value.(pflag.SliceValue)
		given := []string{flag.Value.String()}
		if isSlice {
			given = slice.GetSlice()
		}
		for i, value := range given {
			at := slices.IndexFunc(allowed, func(v string) bool { return strings.EqualFold(v, strings.TrimSpace(value)) })
			if at < 0 {
				err = oneOf(flag.Name, value, allowed...)
				return
			}
			given[i] = allowed[at]
		}
		if isSlice {
			err = slice.Replace(given)
		} else {
			err = flag.Value.Set(given[0])
		}
	})
	return err
}

// completeValues completes the given values.
func completeValues(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return matching(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// matching keeps the candidates whose value starts with prefix, at most completionLimit of them.
func matching(candidates []cobra.Completion, prefix string) []cobra.Completion {
	result := []cobra.Completion{}
	for _, candidate := range candidates {
		if value, _, _ := strings.Cut(candidate, "\t"); strings.HasPrefix(value, prefix) {
			result = append(result, candidate)
			if len(result) == completionLimit {
				break
			}
		}
	}
	return result
}

// commaSeparated completes the last entry of a comma-separated value with complete, without offering
// entries that are already given, and keeps the cursor after it so that another comma can follow.
func commaSeparated(complete cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		done := toComplete[:strings.LastIndexByte(toComplete, ',')+1]
		given := strings.Split(done, ",")
		values, directive := complete(cmd, args, toComplete[len(done):])
		result := make([]cobra.Completion, 0, len(values))
		for _, value := range values {
			if name, _, _ := strings.Cut(value, "\t"); !slices.Contains(given, name) {
				result = append(result, done+value)
			}
		}
		return result, directive | cobra.ShellCompDirectiveNoSpace
	}
}

// mergeCompletions offers the candidates of every completer in order, each value once.
func mergeCompletions(completers ...cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		result, seen := []cobra.Completion{}, map[string]bool{}
		for _, complete := range completers {
			values, _ := complete(cmd, args, toComplete)
			for _, value := range values {
				if name, _, _ := strings.Cut(value, "\t"); !seen[name] {
					seen[name] = true
					result = append(result, value)
				}
			}
		}
		return matching(result, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeJSONFields completes the comma-separated fields that --json and --csv can select for the command.
var completeJSONFields = commaSeparated(func(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if cmd.Annotations["gt.text"] != "" || cmd.Annotations["gt.group"] != "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return matching(commandOutputFields(cmd), toComplete), cobra.ShellCompDirectiveNoFileComp
})

// completeJSONValue completes the word after a bare --json or --csv as its fields. Without this, cobra
// completes that word as a positional argument, because the value of these flags is optional.
func completeJSONValue(args []string) []string {
	if n := len(args); n >= 2 && (args[n-2] == "--json" || args[n-2] == "--csv") && !strings.HasPrefix(args[n-1], "-") {
		return slices.Concat(args[:n-2], []string{args[n-2] + "=" + args[n-1]})
	}
	return args
}

// describe turns items into completion candidates, each a value with a one-line description. Server
// text cannot drive the terminal: values with control characters are skipped, descriptions sanitized.
func describe[T any](items []T, candidate func(T) (value, description string)) []cobra.Completion {
	result := make([]cobra.Completion, 0, len(items))
	for _, item := range items {
		value, description := candidate(item)
		if value == "" || terminal.SafeLine(value) != value {
			continue
		}
		if description = strings.Join(strings.Fields(terminal.SafeLine(description)), " "); description != "" {
			value = cobra.CompletionWithDesc(value, description)
		}
		result = append(result, value)
	}
	return result
}

// completionContext bounds the work behind one completion.
func completionContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), completionTimeout)
}

// completeHosts completes the configured instances with their accounts.
func (a *application) completeHosts(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	snapshot, err := a.settings()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	hosts := slices.Sorted(maps.Keys(snapshot.Hosts.Hosts))
	return matching(describe(hosts, func(host string) (string, string) {
		return host, strings.Join(slices.Sorted(maps.Keys(snapshot.Hosts.Hosts[host].Users)), ", ")
	}), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeAccounts completes the accounts of the selected instance, or of every instance when
// none is selected.
func (a *application) completeAccounts(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := completionContext(cmd)
	defer cancel()
	snapshot, err := a.settings()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	hosts := snapshot.Hosts.Hosts
	if t, err := a.instanceTarget(ctx); err == nil {
		hosts = map[string]settings.Host{t.Host: hosts[t.Host]}
	}
	accounts := []cobra.Completion{}
	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		accounts = append(accounts, describe(slices.Sorted(maps.Keys(hosts[host].Users)), func(user string) (string, string) {
			if hosts[host].User == user {
				return user, host + " (active)"
			}
			return user, host
		})...)
	}
	return matching(accounts, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// gitRemotes lists the remotes of the working directory with their URLs, in configuration order.
func (a *application) gitRemotes(ctx context.Context) [][2]string {
	out, err := a.gitProbe().Output(ctx, a.options.cwd, false, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return nil
	}
	var remotes [][2]string
	for line := range strings.Lines(out) {
		if key, url, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			remotes = append(remotes, [2]string{strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url"), url})
		}
	}
	return remotes
}

// completeRemotes completes the Git remotes of the working directory.
func (a *application) completeRemotes(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := completionContext(cmd)
	defer cancel()
	return matching(describe(a.gitRemotes(ctx), func(remote [2]string) (string, string) { return remote[0], settings.WithoutCredentials(remote[1]) }), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeLocalBranches completes the branches of the local checkout.
func (a *application) completeLocalBranches(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx, cancel := completionContext(cmd)
	defer cancel()
	out, err := a.gitProbe().Output(ctx, a.options.cwd, false, "for-each-ref", "--format=%(refname:lstrip=2)", "refs/heads")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return matching(describe(strings.Fields(out), func(branch string) (string, string) { return branch, "local branch" }), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeAliases completes the configured alias names with their expansions.
func (a *application) completeAliases(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	snapshot, err := a.settings()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	aliases := snapshot.Preferences.Aliases
	return matching(describe(slices.Sorted(maps.Keys(aliases)), func(name string) (string, string) { return name, aliases[name] }), toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeConfig completes a setting that config set changes, then its value: the fixed values, the configured instances
// for default_host, or the Git remotes for remote.
func (a *application) completeConfig(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch {
	case len(args) == 0:
		return matching(settableKeys, toComplete), cobra.ShellCompDirectiveNoFileComp
	case len(args) > 1:
		return nil, cobra.ShellCompDirectiveNoFileComp
	case args[0] == "default_host":
		return a.completeHosts(cmd, args, toComplete)
	case args[0] == "remote":
		return a.completeRemotes(cmd, args, toComplete)
	case args[0] == "ssh_key" || args[0] == "client_cert" || args[0] == "client_key":
		return completeFiles(cmd, args, toComplete)
	}
	return matching(configValues[args[0]], toComplete), cobra.ShellCompDirectiveNoFileComp
}

// apiCompletion completes the values that list fetches from the selected instance. It never
// prompts, refreshes or retries, and failures complete nothing rather than file names.
func (a *application) apiCompletion(requireRepo bool, list func(ctx context.Context, t target, toComplete string) ([]cobra.Completion, error)) cobra.CompletionFunc {
	return a.apiCompletionIn("", requireRepo, list)
}

// apiCompletionIn is apiCompletion for the repository that a command argument names, such as the
// one of a run URL; an empty repo selects the repository like any other command.
func (a *application) apiCompletionIn(repo string, requireRepo bool, list func(ctx context.Context, t target, toComplete string) ([]cobra.Completion, error)) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := completionContext(cmd)
		defer cancel()
		t, err := a.completionTarget(ctx, repo, requireRepo)
		var values []cobra.Completion
		if err == nil {
			values, err = list(ctx, t, toComplete)
		}
		if err != nil {
			cobra.CompDebugln("gt: "+err.Error(), false)
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return matching(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completionTarget resolves an API completion's target like resolve, but offline: it never prompts,
// refreshes credentials or retries.
func (a *application) completionTarget(ctx context.Context, repo string, requireRepo bool) (target, error) {
	t, err := a.resolveRequest(ctx, targetRequest{requireRepo: requireRepo, authenticate: true, offline: true, repo: repo, repoSource: "argument"})
	if err == nil {
		t.client.Retries = 0
	}
	return t, err
}

// completeIssues completes issue numbers in state (open, closed or all) with their titles.
func (a *application) completeIssues(state string) cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, _, err := services.Issues{Client: t.client, Repository: t.Repo}.List(ctx, services.ListOptions{State: state, Kind: "issues", Limit: completionLimit, Page: 1})
		return describe(items, func(item services.Issue) (string, string) { return strconv.FormatInt(item.Number, 10), item.Title }), err
	})
}

// completePulls completes pull request numbers in state (open, closed or all) with their titles.
func (a *application) completePulls(state string) cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, _, err := services.Pulls{Client: t.client, Repository: t.Repo}.List(ctx, services.PullListOptions{State: state, Limit: completionLimit, Page: 1})
		return describe(items, func(item services.Pull) (string, string) { return strconv.FormatInt(item.Number, 10), item.Title }), err
	})
}

// completeLabels completes comma-separated label names of the repository and, when its owner is an
// organization, of the organization.
func (a *application) completeLabels() cobra.CompletionFunc {
	return commaSeparated(a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		labels, err := services.Labels{Client: t.client, Repository: t.Repo}.List(ctx, completionLimit, 1)
		if err != nil {
			return nil, err
		}
		if orgLabels, err := (services.Labels{Client: t.client, Organization: t.owner()}).List(ctx, completionLimit, 1); err == nil {
			labels = append(labels, orgLabels...)
		}
		return describe(labels, func(label services.Label) (string, string) { return label.Name, label.Description }), nil
	}))
}

// completeMilestones completes the titles of open milestones. A numeric title would be read as
// another milestone's ID, so such a milestone is completed by its own ID instead.
func (a *application) completeMilestones() cobra.CompletionFunc {
	return a.completeMilestonesIn("open")
}

// completeMilestonesIn completes the titles of the milestones in state: open, closed or all.
func (a *application) completeMilestonesIn(state string) cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.Milestones{Client: t.client, Repository: t.Repo}.List(ctx, state, completionLimit, 1)
		return describe(items, func(item services.Milestone) (string, string) {
			return titleCandidate(item.ID, item.Title, item.Description)
		}), err
	})
}

// titleCandidate completes a resource addressed by title or ID by its title, or by its ID when the
// title reads as a number, which would be taken as an ID.
func titleCandidate(id int64, title, description string) (string, string) {
	if _, parse := strconv.ParseInt(strings.TrimSpace(title), 10, 64); parse == nil {
		return strconv.FormatInt(id, 10), strings.TrimSpace(title + " " + description)
	}
	return title, description
}

// completeUsers completes comma-separated usernames that can be assigned in the repository.
func (a *application) completeUsers() cobra.CompletionFunc {
	return commaSeparated(a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		users, err := services.Repositories{Client: t.client}.Assignees(ctx, t.Repo)
		return describe(users, func(item services.Assignee) (string, string) { return item.Username, item.FullName }), err
	}))
}

// completeBranches completes the local branches, then the repository's branches.
func (a *application) completeBranches() cobra.CompletionFunc {
	return mergeCompletions(a.completeLocalBranches, a.apiCompletion(true, listBranches))
}

// completeArgumentBranches is completeBranches for a command with a positional [<repository>]:
// once that argument is given, it completes that repository's branches, not those of the clone here.
func (a *application) completeArgumentBranches() cobra.CompletionFunc {
	inClone := a.completeBranches()
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 || a.options.repo != "" {
			return inClone(cmd, args, toComplete)
		}
		return a.apiCompletionIn(args[0], true, listBranches)(cmd, args, toComplete)
	}
}

func listBranches(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
	items, err := services.Branches{Client: t.client}.ListPage(ctx, t.Repo, completionLimit, 1)
	return describe(items.Items, func(item services.Branch) (string, string) {
		if item.Protected {
			return item.Name, "protected"
		}
		return item.Name, ""
	}), err
}

// completeTags completes the repository's tags.
func (a *application) completeTags() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		tags, err := services.Repositories{Client: t.client}.Tags(ctx, t.Repo, completionLimit, 1)
		return describe(tags, func(name string) (string, string) { return name, "" }), err
	})
}

// completeReleases completes release tags with their titles.
func (a *application) completeReleases() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.Releases{Client: t.client, Repository: t.Repo}.ListPage(ctx, completionLimit, 1)
		return describe(items.Items, func(item services.Release) (string, string) { return item.Tag, item.Title }), err
	})
}

// completeOrgs completes the organizations of the account.
func (a *application) completeOrgs() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.Organizations{Client: t.client}.List(ctx, true, "", completionLimit, 1)
		return describe(items.Items, func(item services.Organization) (string, string) { return item.Name, item.FullName }), err
	})
}

// completeTeams completes teams as ORG/TEAM: first the account's organizations, then the teams of the
// organization before the slash.
func (a *application) completeTeams() cobra.CompletionFunc {
	orgs := a.completeOrgs()
	teams := a.apiCompletion(false, func(ctx context.Context, t target, toComplete string) ([]cobra.Completion, error) {
		org, _, _ := strings.Cut(toComplete, "/")
		items, err := services.Teams{Client: t.client}.List(ctx, org, completionLimit, 1)
		return describe(items.Items, func(item services.Team) (string, string) {
			return org + "/" + item.Name, strings.TrimSpace(item.Description + " (ID " + strconv.FormatInt(item.ID, 10) + ")")
		}), err
	})
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if strings.Contains(toComplete, "/") {
			return teams(cmd, args, toComplete)
		}
		values, directive := orgs(cmd, args, toComplete)
		for i, value := range values {
			name, description, _ := strings.Cut(value, "\t")
			values[i] = cobra.CompletionWithDesc(name+"/", description)
		}
		return values, directive | cobra.ShellCompDirectiveNoSpace
	}
}

// completeRepos completes repositories as OWNER/NAME: those of the Git remotes on configured
// instances, then those of the owner before the slash, or else the account's repositories.
func (a *application) completeRepos() cobra.CompletionFunc {
	remotes := func(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		ctx, cancel := completionContext(cmd)
		defer cancel()
		snapshot, err := a.settings()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		values := []cobra.Completion{}
		for _, remote := range a.gitRemotes(ctx) {
			if host, repo, err := settings.RemoteRepository(remote[1], snapshot.Hosts.Hosts); err == nil && snapshot.Hosts.Hosts[host].Users != nil {
				values = append(values, cobra.CompletionWithDesc(repo, "remote "+remote[0]))
			}
		}
		return matching(values, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return mergeCompletions(remotes, a.apiCompletion(false, func(ctx context.Context, t target, toComplete string) ([]cobra.Completion, error) {
		owner, _, found := strings.Cut(toComplete, "/")
		if !found {
			owner = ""
		}
		items, _, err := services.Repositories{Client: t.client}.List(ctx, services.RepositoryListOptions{Owner: owner, Limit: completionLimit, Page: 1})
		return describe(items, func(item services.Repository) (string, string) { return item.FullName, item.Description }), err
	}))
}

// completeWorkflows completes workflow files with their names.
func (a *application) completeWorkflows() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.Actions{Client: t.client, Repository: t.Repo}.Workflows(ctx)
		return describe(items, func(item services.Workflow) (string, string) { return item.ID, item.Name }), err
	})
}

// completeRuns completes the IDs of recent workflow runs with their status and title.
func (a *application) completeRuns() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.Actions{Client: t.client, Repository: t.Repo}.Runs(ctx, services.RunListOptions{Limit: completionLimit, Page: 1})
		return describe(items, func(item services.Run) (string, string) {
			return strconv.FormatInt(item.ID, 10), strings.Join(slices.DeleteFunc([]string{item.Status, item.Conclusion, item.Title}, func(s string) bool { return s == "" }), " · ")
		}), err
	})
}

// completeSecrets completes Actions secret names in the selected scope.
func (a *application) completeSecrets(scope func() services.ActionScope) cobra.CompletionFunc {
	list := func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.ActionSecrets{Client: t.client, Scope: actionScope(scope, t)}.ListPage(ctx, completionLimit, 1)
		return describe(items.Items, func(item services.ActionSecret) (string, string) { return item.Name, item.Description }), err
	}
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return a.apiCompletion(inRepository(scope()), list)(cmd, args, toComplete)
	}
}

// completeVariables completes Actions variable names in the selected scope.
func (a *application) completeVariables(scope func() services.ActionScope) cobra.CompletionFunc {
	list := func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		items, err := services.ActionVariables{Client: t.client, Scope: actionScope(scope, t)}.ListPage(ctx, completionLimit, 1)
		return describe(items.Items, func(item services.ActionVariable) (string, string) { return item.Name, item.Description }), err
	}
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return a.apiCompletion(inRepository(scope()), list)(cmd, args, toComplete)
	}
}

// actionScope applies the target's repository to a scope without organization or account.
func actionScope(scope func() services.ActionScope, t target) services.ActionScope {
	selected := scope()
	if inRepository(selected) {
		selected.Repository = t.Repo
	}
	return selected
}

// inRepository reports whether the scope is the repository's, which discovery must then select.
func inRepository(scope services.ActionScope) bool {
	return scope.Organization == "" && !scope.Account
}

// pathFlags take local file paths, like every flag whose name ends in -file.
var pathFlags = []string{"asset", "attachment", "client-cert", "client-key", "file", "input", "output", "ssh-key"}

// completeDefaults completes paths only where a command takes them: path flags and flags marked
// with MarkFlagFilename. Everything else without a completer completes nothing, by the root's
// default directive, rather than file names.
func completeDefaults(cmd *cobra.Command) {
	if cmd.ValidArgsFunction != nil {
		cmd.ValidArgsFunction = withoutGiven(cmd.ValidArgsFunction)
	}
	for _, flags := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags()} {
		flags.VisitAll(func(flag *pflag.Flag) {
			if _, registered := cmd.GetFlagCompletionFunc(flag.Name); registered {
				return
			}
			switch {
			case flag.Name == "dir":
				_ = cmd.RegisterFlagCompletionFunc(flag.Name, completeDirectories)
			case strings.HasSuffix(flag.Name, "-file") || slices.Contains(pathFlags, flag.Name) || flag.Annotations[cobra.BashCompFilenameExt] != nil:
				_ = cmd.RegisterFlagCompletionFunc(flag.Name, completeFiles)
			}
		})
	}
	for _, child := range cmd.Commands() {
		completeDefaults(child)
	}
}

// withoutGiven drops the positional completions that the arguments already name. Flag values are not
// filtered: Cobra passes the positional arguments to their completers as well, and a job ID may equal
// its run ID.
func withoutGiven(complete cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		values, directive := complete(cmd, args, toComplete)
		return slices.DeleteFunc(values, func(value cobra.Completion) bool {
			name, _, _ := strings.Cut(value, "\t")
			return slices.Contains(args, name)
		}), directive
	}
}

// completeFiles leaves the completion of file paths to the shell.
func completeFiles(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveDefault
}

// completeDirectories leaves the completion of directory paths to the shell.
func completeDirectories(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveFilterDirs
}

// firstArg completes only the first argument with complete.
func firstArg(complete cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return complete(cmd, args, toComplete)
	}
}

// pathArgs completes the arguments before position n with complete, and paths with path from
// position n on: every later argument when variadic, else only the one at n.
func pathArgs(n int, variadic bool, path, complete cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		switch {
		case len(args) < n:
			return complete(cmd, args, toComplete)
		case len(args) == n || variadic:
			return path(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// completionInstallHelp replaces cobra's help for the completion scripts, whose Markdown headings
// and tabs do not read like gt's; bash 3.2, macOS's default, cannot source <(gt completion bash).
func completionInstallHelp(root *cobra.Command) {
	completion, _, err := root.Find([]string{"completion"})
	if err != nil || completion == root {
		return
	}
	completion.Short = "Generate shell completion scripts"
	completion.Long = "Generate the completion script of gt for bash, zsh, fish or powershell.\nRun gt completion <shell> --help to see how to load it."
	help := map[string]struct{ long, example string }{
		"bash": {`Generate the completion script for bash. It needs the bash-completion package.

To load completions in every new shell, add the eval line below to ~/.bashrc (on macOS,
~/.bash_profile); bash 3.2, the default on macOS, cannot source <(gt completion bash).`, `$ eval "$(gt completion bash)"`},
		"zsh": {`Generate the completion script for zsh. It needs compinit; enable it
with the line autoload -U compinit; compinit in ~/.zshrc.

To load completions in every new shell, add the source line below to ~/.zshrc, or write
the script to a directory of $fpath as _gt.`, `$ source <(gt completion zsh)
$ gt completion zsh > "${fpath[1]}/_gt"`},
		"fish": {`Generate the completion script for fish.

To load completions in every new shell, write the script to ~/.config/fish/completions;
to load them in the current shell only, pipe it to source.`, `$ gt completion fish > ~/.config/fish/completions/gt.fish
$ gt completion fish | source`},
		"powershell": {`Generate the completion script for PowerShell.

To load completions in every new shell, add the line below to your PowerShell profile.`, `$ gt completion powershell | Out-String | Invoke-Expression`},
	}
	for _, shell := range completion.Commands() {
		shell.Long, shell.Example = help[shell.Name()].long, help[shell.Name()].example
		shell.Args, shell.DisableFlagsInUseLine = argsExact(0), false
		if shell.Annotations == nil {
			shell.Annotations = map[string]string{}
		}
		maps.Copy(shell.Annotations, textOutput("completion prints a shell script"))
		if flag := shell.Flags().Lookup("no-descriptions"); flag != nil {
			flag.Usage = "Disable completion descriptions"
		}
	}
}
