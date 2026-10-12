// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func repositoryRecord(item services.Repository) terminal.Record {
	return terminal.Record{Heading: []terminal.Cell{{Text: item.FullName, Bold: true}}, Lines: []string{item.URL}}
}

func cloneURLForProtocol(cloneURL, sshURL, protocol string) (remote string, ssh bool, err error) {
	remote = cloneURL
	if protocol == "ssh" {
		remote = sshURL
	}
	if remote == "" {
		return "", false, errors.New("Gitea did not return a clone URL")
	}
	ssh = settings.RemoteIsSSH(remote)
	return remote, ssh, nil
}

// apiSSHHost trusts the SSH authority of the API's ssh_url when the instance has no ssh_host,
// so servers whose SSH host or port differ from the web host clone without extra configuration.
func apiSSHHost(host settings.Host, sshURL string) settings.Host {
	if host.SSHHost != "" || !settings.RemoteIsSSH(sshURL) {
		return host
	}
	if u, err := settings.ParseRemote(sshURL); err == nil {
		if _, err := settings.SSHEndpoint(u.Host); err == nil {
			host.SSHHost = u.Host
		}
	}
	return host
}

// mirrorSourceURL returns a mirror's credential-free network source URL, or "" when there is none to add.
func mirrorSourceURL(item services.Repository) string {
	if !item.Mirror || item.OriginalURL == "" {
		return ""
	}
	u, err := url.Parse(item.OriginalURL)
	if err != nil || u.Host == "" || strings.ContainsAny(item.OriginalURL, " \t\r\n\\") {
		return ""
	}
	switch u.Scheme {
	case "https", "http", "git":
		if u.User != nil {
			return ""
		}
	case "ssh":
		if _, password := u.User.Password(); password {
			return ""
		}
	default:
		return ""
	}
	return u.String()
}

// ensureHeadPublished prompts to push the head branch when the selected remote tip is missing or differs.
// Cross-repo heads (owner:branch), non-work-trees, and repositories without a matching remote are skipped.
func (a *application) ensureHeadPublished(cmd *cobra.Command, t target, snapshot settings.Snapshot, head string) error {
	if head == "" || strings.Contains(head, ":") {
		return nil
	}
	ctx := cmd.Context()
	probe := a.gitProbe()
	inside, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return nil
	}
	local, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+head+"^{commit}")
	if err != nil {
		return nil
	}
	remote, err := a.checkoutRemote(ctx, t, snapshot)
	if err != nil {
		if a.options.remote == "" && snapshot.Preferences.Remote == "" && errors.Is(err, errNoMatchingRemote) {
			return nil
		}
		return clierr.Wrap(clierr.Usage, err)
	}
	remoteTip, tipErr := probe.Output(ctx, a.options.cwd, false, "for-each-ref", "--format=%(objectname)", "refs/remotes/"+remote+"/"+head)
	if tipErr == nil && remoteTip == local {
		return nil
	}
	if err := a.confirm(cmd, fmt.Sprintf("Push branch %q to remote %q before creating the pull request?", head, remote)); err != nil {
		return err
	}
	credentials, err := a.remoteCredentials(ctx, t, remote)
	if err != nil {
		return err
	}
	args := append(append([]string{}, credentials...), "push", "--", remote, "refs/heads/"+head)
	_, err = a.git(ctx, args...)
	return err
}

func (a *application) addRepositoryMutations(group *cobra.Command) {
	group.AddCommand(a.repoCreateCommand(), a.repoMigrateCommand())
	var forkOrg string
	fork := &cobra.Command{Use: "fork [repository]", Short: "Fork the selected repository", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Repositories{Client: t.client}).Fork(cmd.Context(), t.Repo, forkOrg)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, repositoryRecord)
	}}
	fork.Flags().StringVar(&forkOrg, "org", "", "Fork under an organization")
	group.AddCommand(fork)
	var description, repoName, website, defaultBranch string
	var private, template, repoArchived, actionsEnabled bool
	edit := &cobra.Command{Use: "edit [repository]", Short: "Edit repository settings", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		options := services.RepositoryEdit{}
		if cmd.Flags().Changed("name") {
			if strings.TrimSpace(repoName) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("repository name cannot be empty"))
			}
			options.Name = &repoName
		}
		if cmd.Flags().Changed("website") {
			options.Website = &website
		}
		if cmd.Flags().Changed("default-branch") {
			if strings.TrimSpace(defaultBranch) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("default branch cannot be empty"))
			}
			options.DefaultBranch = &defaultBranch
		}
		if cmd.Flags().Changed("archived") {
			options.Archived = &repoArchived
		}

		if cmd.Flags().Changed("description") {
			options.Description = &description
		}
		if cmd.Flags().Changed("private") {
			options.Private = &private
		}
		if cmd.Flags().Changed("as-template") {
			options.Template = &template
		}
		if cmd.Flags().Changed("actions") {
			options.ActionsEnabled = &actionsEnabled
		}
		if options == (services.RepositoryEdit{}) {
			return clierr.Wrap(clierr.Usage, errors.New("supply an edit flag; see gt repo edit --help"))
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if repoArchived {
			if err := a.confirm(cmd, "Archive repository "+t.Repo); err != nil {
				return err
			}
		}
		item, err := (services.Repositories{Client: t.client}).Edit(cmd.Context(), t.Repo, options)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, repositoryRecord)
	}}
	edit.Flags().StringVar(&repoName, "name", "", "Rename the repository")
	edit.Flags().StringVar(&website, "website", "", "Repository website; empty clears")
	edit.Flags().StringVar(&defaultBranch, "default-branch", "", "Default branch name")
	edit.Flags().BoolVar(&repoArchived, "archived", false, "Archive repository; =false restores it")
	edit.Flags().StringVar(&description, "description", "", "Repository description")
	edit.Flags().BoolVar(&private, "private", false, "Make repository private (use --private=false to make public)")
	edit.Flags().BoolVar(&template, "as-template", false, "Make repository a template (use =false to disable)")
	edit.Flags().BoolVar(&actionsEnabled, "actions", false, "Enable Actions (use --actions=false to disable)")
	group.AddCommand(edit)
	for _, verb := range []string{"archive", "delete"} {
		command := &cobra.Command{Use: verb + " [repository]", Short: titleCase(verb) + " the selected repository", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			if err := a.confirm(cmd, verb+" "+t.Host+"/"+t.Repo); err != nil {
				return err
			}
			service := services.Repositories{Client: t.client}
			if verb == "delete" {
				return service.Delete(cmd.Context(), t.Repo)
			}
			archived := true
			item, err := service.Edit(cmd.Context(), t.Repo, services.RepositoryEdit{Archived: &archived})
			if err != nil {
				return err
			}
			return writeRecord(a, cmd, item, repositoryRecord)
		}}
		if verb == "delete" {
			command.Annotations = textOutput("repository deletion does not return a resource")
		}
		group.AddCommand(command)
	}
	var depth int
	clone := &cobra.Command{Use: "clone <repository> [directory]", Short: "Clone a repository using the configured Git protocol", Args: usageArgs(cobra.RangeArgs(1, 2)), RunE: func(cmd *cobra.Command, args []string) error {
		if depth < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--depth must be nonnegative; zero fetches full history"))
		}
		if a.options.repo != "" {
			return clierr.Wrap(clierr.Usage, errors.New("clone repository argument cannot be combined with --repo"))
		}
		a.options.repo = args[0]
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo)
		if err != nil {
			return err
		}
		directory := ""
		if len(args) == 2 {
			directory = args[1]
		}
		if err := a.cloneRepository(cmd, t, item, directory, depth); err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		return nil
	}}
	clone.Flags().IntVarP(&depth, "depth", "d", 0, "Number of commits to fetch (0: full history)")
	group.AddCommand(clone)
}

// cloneRepository clones item with the configured Git protocol and adds fork and mirror remotes.
// An empty directory lets Git choose it from the repository name.
func (a *application) cloneRepository(cmd *cobra.Command, t target, item services.Repository, directory string, depth int) error {
	protocol := t.gitProtocol
	host := t.host
	if protocol == "ssh" {
		host = apiSSHHost(host, item.SSHURL)
	}
	hosts := map[string]settings.Host{t.Host: host}
	remote, remoteIsSSH, err := cloneURLForProtocol(item.CloneURL, item.SSHURL, protocol)
	if err != nil {
		return err
	}
	cloneHost, cloneRepo, err := settings.RepositoryURL(remote, hosts)
	if err != nil || cloneHost != t.Host || !strings.EqualFold(cloneRepo, t.Repo) || (protocol == "ssh") != remoteIsSSH {
		return errors.New("Gitea clone URL does not match the selected instance and repository; check the server root URL and configured ssh_host")
	}
	gitArgs := []string{"clone"}
	if depth > 0 {
		gitArgs = append(gitArgs, "--depth", fmt.Sprint(depth))
	}
	gitArgs = append(gitArgs, "--", remote)
	if directory != "" {
		gitArgs = append(gitArgs, directory)
	} else if directory = filepath.Base(strings.TrimSuffix(item.Name, ".git")); directory == "" || directory == "." || directory == "/" {
		directory = item.Name
	}
	credentialArgs, err := a.gitTransportArguments(t, remoteIsSSH)
	if err != nil {
		return err
	}
	gitArgs = append(credentialArgs, gitArgs...)
	if _, err := a.git(cmd.Context(), gitArgs...); err != nil {
		return err
	}
	clone := directory
	if !filepath.IsAbs(clone) {
		clone = filepath.Join(a.options.cwd, clone)
	}
	if item.Fork && item.Parent != nil {
		upstream, upstreamSSH, err := cloneURLForProtocol(item.Parent.CloneURL, item.Parent.SSHURL, protocol)
		if err != nil {
			return errors.New("cloned the fork, but Gitea did not return a parent clone URL")
		}
		upstreamHost, upstreamRepo, err := settings.RepositoryURL(upstream, hosts)
		if err != nil || upstreamHost != t.Host || !strings.EqualFold(upstreamRepo, item.Parent.FullName) || (protocol == "ssh") != upstreamSSH {
			return errors.New("cloned the fork, but its parent clone URL does not match the selected instance")
		}
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", "upstream", upstream); err != nil {
			return fmt.Errorf("cloned the fork, but could not add the upstream remote: %w", err)
		}
		if branch := item.Parent.DefaultBranch; branch != "" {
			if _, err := a.gitIn(cmd.Context(), clone, "config", "branch."+branch+".remote", "upstream"); err != nil {
				return fmt.Errorf("cloned the fork and added upstream, but could not configure branch tracking: %w", err)
			}
			if _, err := a.gitIn(cmd.Context(), clone, "config", "branch."+branch+".merge", "refs/heads/"+branch); err != nil {
				return fmt.Errorf("cloned the fork and added upstream, but could not configure branch tracking: %w", err)
			}
		}
	}
	// The remote is named source because repository discovery prefers a remote named upstream.
	if source := mirrorSourceURL(item); source != "" {
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", "source", source); err != nil {
			return fmt.Errorf("cloned the mirror, but could not add the source remote: %w", err)
		}
	}
	return nil
}

func pullRecord(item services.Pull) terminal.Record {
	return numberedRecord(item.Number, item.Title, item.URL)
}

func (a *application) addPullMutations(group *cobra.Command) {
	group.AddCommand(a.pullCreateCommand())
	for _, verb := range []string{"close", "reopen"} {
		group.AddCommand(&cobra.Command{Use: verb + " <number>", Short: titleCase(verb) + " a pull request", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
			n, err := number(args[0])
			if err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			state := "closed"
			if verb == "reopen" {
				state = "open"
			}
			item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Edit(cmd.Context(), n, services.PullEdit{State: &state})
			if err != nil {
				return err
			}
			return writeRecord(a, cmd, item, pullRecord)
		}})
	}
	group.AddCommand(&cobra.Command{Use: "diff <number>", Short: "Print a pull request diff", Annotations: textOutput("diff returns patch text"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		body, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Diff(cmd.Context(), n)
		if err != nil {
			return err
		}
		defer body.Close()
		_, err = io.Copy(cmd.OutOrStdout(), body)
		return err
	}})
	group.AddCommand(a.pullReviewCommand())
	var method, mergeTitle, mergeMessage, mergeMessageFile string
	var deleteBranch, mergeEditor bool
	merge := &cobra.Command{Use: "merge [number]", Short: "Merge a pull request", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		if method != "merge" && method != "rebase" && method != "rebase-merge" && method != "squash" && method != "fast-forward-only" {
			return clierr.Wrap(clierr.Usage, errors.New("unsupported merge method"))
		}
		if err := validateTextSources(cmd, "message", "message-file"); err != nil {
			return err
		}
		if !mergeEditor {
			var err error
			mergeMessage, err = a.readTextSource(cmd, mergeMessage, mergeMessageFile, "message", "message-file")
			if err != nil {
				return err
			}
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		policy := a.policy(snapshot)
		if len(args) == 0 && (!policy.CanPrompt(a.deps.Streams) || a.deps.MergeForm == nil) {
			return clierr.Wrap(clierr.Usage, errors.New("supply a pull request number, or run interactively to choose one"))
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		var n int64
		if len(args) == 1 {
			n, err = number(args[0])
			if err != nil {
				return err
			}
		} else {
			n, err = a.selectPullToMerge(cmd, t)
			if err != nil {
				return err
			}
		}
		if mergeEditor {
			editorDraft, err = a.editDraft(cmd, "")
			if err != nil {
				return err
			}
			mergeMessage = editorDraft.Body
		}
		if err := a.confirm(cmd, fmt.Sprintf("Merge pull request %s#%d", t.Repo, n)); err != nil {
			return err
		}
		if !cmd.Flags().Changed("delete-branch") && !a.options.yes && policy.CanPrompt(a.deps.Streams) {
			item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
			if err != nil {
				return err
			}
			if !item.HeadRemoteDeleted {
				if deleteBranch, err = a.deps.Prompter.Confirm(cmd.Context(), fmt.Sprintf("Delete branch %s after merging?", item.Head)); err != nil {
					return err
				}
			}
		}
		return (services.Pulls{Client: t.client, Repository: t.Repo}).Merge(cmd.Context(), n, services.MergeOptions{
			Method: method, Title: mergeTitle, Message: mergeMessage, DeleteBranch: deleteBranch,
		})
	}}
	merge.Flags().StringVarP(&method, "method", "s", "merge", "Merge strategy")
	merge.Flags().StringVar(&method, "style", "merge", "Alias of --method")
	_ = merge.Flags().MarkHidden("style")
	merge.Flags().StringVar(&mergeTitle, "title", "", "Merge commit title")
	merge.Flags().StringVar(&mergeMessage, "message", "", "Merge commit message")
	merge.Flags().StringVar(&mergeMessageFile, "message-file", "", "Read merge commit message from file, or - for stdin")
	merge.Flags().BoolVar(&mergeEditor, "editor", false, "Compose the merge commit message in the configured editor")
	merge.Flags().BoolVar(&deleteBranch, "delete-branch", false, "Delete head branch after merging")
	group.AddCommand(merge)
}

func (a *application) selectPullToMerge(cmd *cobra.Command, t target) (int64, error) {
	branch, err := a.currentBranch(cmd.Context())
	if err != nil {
		return 0, clierr.Wrap(clierr.Usage, errors.New("supply a pull number outside a branch checkout"))
	}
	items, err := (services.Pulls{Client: t.client, Repository: t.Repo}).ListOpen(cmd.Context())
	if err != nil {
		return 0, err
	}
	var headMatches, baseMatches []terminal.MergePullChoice
	for _, item := range items {
		label := item.Head
		if owner, _, ok := strings.Cut(item.HeadRepo, "/"); ok && !strings.EqualFold(item.HeadRepo, t.Repo) {
			label = owner + ":" + item.Head // fork heads can share a branch name with the local one
		}
		choice := terminal.MergePullChoice{Number: item.Number, Title: item.Title, Head: label}
		switch branch {
		case item.Head:
			headMatches = append(headMatches, choice)
		case item.Base:
			baseMatches = append(baseMatches, choice)
		}
	}
	return a.deps.MergeForm.SelectPull(cmd.Context(), append(headMatches, baseMatches...))
}
