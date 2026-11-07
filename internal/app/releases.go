// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

func (a *application) releaseCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "release",
		Short: "Manage releases",
		Long: `Work with the releases of a repository: list and view them with their notes and assets, create,
edit, publish and delete them, and upload or download their assets.`,
	}
	group.AddCommand(a.releaseListCommand(), a.releaseViewCommand(), a.releaseCreateCommand(), a.releaseEditCommand(),
		a.releasePublishCommand(), a.releaseDeleteCommand(), a.releaseAssetCommand(), a.releaseUploadCommand(), a.releaseDownloadCommand())
	return jsonOutput[services.Release](group)
}

// releaseKind is the type of a release for people: Draft, Pre-release, or Latest when it is the
// repository's latest release.
func releaseKind(item services.Release, latest int64) terminal.Cell {
	switch {
	case item.Draft:
		return terminal.Cell{Text: "Draft", Muted: true}
	case item.Prerelease:
		return terminal.Cell{Text: "Pre-release", Color: "yellow"}
	case item.ID == latest:
		return terminal.Cell{Text: "Latest", Color: "green"}
	}
	return terminal.Cell{}
}

func (a *application) releaseListCommand() *cobra.Command {
	var limit, page int
	var selecting, excludeDrafts, excludePrereleases bool
	var order string
	command := &cobra.Command{
		Use:   "list",
		Short: "List releases",
		Long: `List the releases of a repository, in the order of Gitea, which goes by the date of each release's
tag. --exclude-drafts and --exclude-pre-releases leave those out.

--order asc lists the oldest first and --order desc the newest first. Both read every release and sort
them in gt, by when each was published, or created for a draft, before printing the page. Without
--order the releases stay in Gitea's order.`,
		Example: `$ gt release list
$ gt release list -R alice/project --json tag,title,draft
$ gt release list --exclude-drafts --exclude-pre-releases --limit 5
$ gt release list --order asc --limit 1 --json tag --jq '.[0].tag'`,
		Args: argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.checkSelect(cmd, selecting); err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			sorted := order != ""
			result, err := (services.Releases{Client: target.client, Repository: target.Repo}).ListPage(cmd.Context(), limit, page, services.ReleaseListOptions{ExcludeDrafts: excludeDrafts, ExcludePrereleases: excludePrereleases, Order: order})
			if err != nil {
				return err
			}
			if selecting && len(result.Items) > 0 {
				return runSelection(a, cmd, result.Items, releaseSelection(target.Repo), target.Host, servicePage(result, page))
			}
			// Releases come newest first, so the first published one of the first page is the latest.
			var latest int64
			published := slices.IndexFunc(result.Items, func(item services.Release) bool { return !item.Draft && !item.Prerelease })
			switch {
			case published < 0 || a.options.json != "":
			case page == 1 && !sorted:
				latest = result.Items[published].ID
			default:
				item, err := (services.Releases{Client: target.client, Repository: target.Repo}).Latest(cmd.Context())
				if err != nil && !services.IsNotFound(err) {
					return err
				}
				latest = item.ID
			}
			spec := listSpec[services.Release]{
				Columns: []terminal.Column{{Header: "Title", Flex: true}, {Header: "Type"}, {Header: "Tag"}, {Header: "Published"}, {Header: "URL", PipedOnly: true}},
				Row: func(item services.Release) []terminal.Cell {
					return []terminal.Cell{{Text: cmp.Or(item.Title, item.Tag), Link: item.URL}, releaseKind(item, latest), {Text: item.Tag}, {Time: terminal.ParseTime(item.When())}, {Text: item.URL}}
				},
				Context: "releases in " + target.Repo, Empty: "no releases in " + target.Repo,
			}
			return writeTable(a, cmd, result.Items, spec, servicePage(result, page))
		},
	}
	pageFlags(command, &limit, &page)
	command.Flags().BoolVar(&excludeDrafts, "exclude-drafts", false, "Leave out draft releases")
	command.Flags().BoolVar(&excludePrereleases, "exclude-pre-releases", false, "Leave out pre-releases")
	enumFlag(command, &order, "order", "", "", "Read every release and sort by when each was published, oldest (asc) or newest (desc) first (default: the order of Gitea)", "asc", "desc")
	selectFlag(command, &selecting)
	return command
}

// viewRelease returns the release tagged tag, or the latest release without a tag.
func viewRelease(cmd *cobra.Command, service services.Releases, tag string) (services.Release, error) {
	if tag == "" {
		return service.Latest(cmd.Context())
	}
	return service.View(cmd.Context(), tag)
}

func (a *application) releaseViewCommand() *cobra.Command {
	var web bool
	command := &cobra.Command{
		Use:   "view [<tag>]",
		Short: "View a release",
		Long:  "View a release with its notes, rendered on a terminal, and its assets. Without a tag, view the latest release.",
		Example: `# View the latest release
$ gt release view

# View a release in the browser
$ gt release view v1.0.0 --web`,
		Args: nonBlankArgs(cobra.MaximumNArgs(1), "tag"),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			tag := ""
			if len(args) > 0 {
				tag = args[0]
			}
			item, err := viewRelease(cmd, service, tag)
			if err != nil {
				return err
			}
			if web {
				return a.openBrowser(cmd, item.URL)
			}
			latest := item.ID
			if tag != "" && a.options.json == "" && !item.Draft && !item.Prerelease {
				newest, _ := service.Latest(cmd.Context())
				latest = newest.ID
			}
			return writeView(a, cmd, item, func(item services.Release) terminal.View { return a.releaseView(item, latest) })
		},
	}
	command.Flags().BoolVarP(&web, "web", "w", false, "Open the release in the browser")
	command.ValidArgsFunction = a.completeReleases()
	return command
}

func (a *application) releaseView(item services.Release, latest int64) terminal.View {
	view := terminal.View{Title: cmp.Or(item.Title, item.Tag), Body: item.Body, Kind: "release", URL: item.URL}
	if item.Title != "" && item.Title != item.Tag {
		view.ID = item.Tag
	}
	if kind := releaseKind(item, latest); kind.Text != "" {
		view.Badges = []terminal.Cell{kind}
	}
	verb := "released"
	if item.Draft {
		verb = "drafted"
	}
	var meta []string
	if when := terminal.ParseTime(item.When()); !when.IsZero() {
		meta = append(meta, strings.TrimSpace(item.Author+" "+verb+" this "+terminal.RelativeTime(a.deps.Now(), when)))
	}
	if item.Target != "" {
		meta = append(meta, "target "+item.Target)
	}
	view.Meta = strings.Join(meta, " · ")
	assets := make([]string, len(item.Assets))
	for i, asset := range item.Assets {
		assets[i] = fmt.Sprintf("%s (%s)", asset.Name, terminal.Bytes(asset.Size))
	}
	// Scripts get the state that the badge shows on a terminal.
	view.Fields = []terminal.Field{
		{Label: "Tag", Value: item.Tag, PipedOnly: true},
		{Label: "Draft", Value: strconv.FormatBool(item.Draft), PipedOnly: true},
		{Label: "Prerelease", Value: strconv.FormatBool(item.Prerelease), PipedOnly: true},
		{Label: "Assets", Value: strings.Join(assets, ", ")},
	}
	return view
}

// releaseFields are the flags of release create and release edit.
type releaseFields struct {
	tag, title, notes, notesFile, target string
	draft, prerelease, editor            bool
}

func (f *releaseFields) register(a *application, cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.title, "title", "t", "", "Release title")
	flags.StringVarP(&f.notes, "notes", "n", "", "Release notes")
	flags.StringVarP(&f.notesFile, "notes-file", "F", "", "Read release notes from a file, or - for standard input")
	flags.BoolVarP(&f.editor, "editor", "e", false, "Write release notes in an editor")
	flags.StringVar(&f.target, "target", "", "Branch or commit the tag points to, when it is new")
	flags.BoolVarP(&f.draft, "draft", "d", false, "Save the release as a draft")
	flags.BoolVarP(&f.prerelease, "prerelease", "p", false, "Mark the release as a prerelease")
	_ = cmd.RegisterFlagCompletionFunc("target", a.completeBranches())
}

func (f *releaseFields) options(a *application, cmd *cobra.Command) (services.ReleaseOptions, error) {
	opts := services.ReleaseOptions{}
	changed := cmd.Flags().Changed
	if changed("title") {
		if strings.TrimSpace(f.title) == "" {
			return opts, clierr.Wrap(clierr.Usage, errors.New("release title cannot be empty"))
		}
		opts.Title = &f.title
	}
	if changed("notes") || changed("notes-file") {
		notes, err := a.readTextSource(cmd, f.notes, f.notesFile, "notes", "notes-file")
		if err != nil {
			return opts, err
		}
		opts.Body = &notes
	}
	for _, field := range []struct {
		flag  string
		value *bool
		into  **bool
	}{{"draft", &f.draft, &opts.Draft}, {"prerelease", &f.prerelease, &opts.Prerelease}} {
		if changed(field.flag) {
			*field.into = field.value
		}
	}
	if changed("target") {
		opts.Target = &f.target
	}
	return opts, nil
}

func (a *application) releaseCreateCommand() *cobra.Command {
	var fields releaseFields
	var assets []string
	var verifyTag bool
	command := &cobra.Command{
		Use:     "create [<tag>] [<file>...]",
		Aliases: []string{"new"},
		Short:   "Create a release",
		Long: `Create a release for a tag, and upload the files that follow the tag as its assets. Gitea creates
the tag at --target (the default branch) when it does not exist yet. The title defaults to the tag.
Without a tag and flags on a terminal, a form asks for the tag, title, notes, target, and whether
the release is a draft or a prerelease.

gt never pushes: commits and tags that only exist in your clone are not on the server, so push them
first (git push <remote> <tag>). In a clone, a tag that only exists locally is refused unless
--target says where Gitea creates it, and a new tag without --target is refused unless HEAD is the
head of the default branch on the server. A --target that is no branch on the server, such as HEAD
or a short SHA, names the commit it resolves to in the clone. --verify-tag refuses any tag that is
not on the server yet.`,
		Example: `# Create a release in a form
$ gt release create

# Create a release with notes from a file and two assets
$ gt release create v1.0.0 dist/gt-linux dist/gt-darwin --notes-file CHANGELOG.md

# Create a draft prerelease of a new tag on a branch
$ gt release create v1.1.0-rc1 --target release/1.1 --draft --prerelease --title "1.1 RC 1"`,
		Args: nonBlankArgs(cobra.ArbitraryArgs, "tag", "file"),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var editorDraft terminal.Draft
			defer finishDraft(&editorDraft, &runErr)
			form := len(args) == 0 && !localFlagsChanged(cmd) && a.canPrompt() && a.deps.ReleaseForm != nil
			if len(args) > 0 {
				fields.tag, assets = args[0], append(args[1:], assets...)
			}
			if err := validateTextSources(cmd, "notes", "notes-file"); err != nil {
				return err
			}
			if fields.tag == "" && fields.notesFile == "-" {
				return clierr.Wrap(clierr.Usage, errors.New("--notes-file - requires a tag; stdin cannot also supply a prompt"))
			}
			if err := requireUniqueAssetNames(assets); err != nil {
				return err
			}
			files, err := openRegularFiles(assets)
			defer closeFiles(files)
			if err != nil {
				return err
			}
			opts, err := fields.options(a, cmd)
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true) // before the form, which a failure would discard
			if err != nil {
				return err
			}
			// Only the clone of the selected repository says anything about its tags and commits.
			remote, clone := a.cloneRemote(cmd.Context(), target)
			prompted := false
			if form {
				initial := terminal.ReleaseFormValues{Editor: a.editorCommand()}
				if clone {
					initial.Target = "HEAD" // the commit of the clone, not whatever the default branch is at
				}
				values, err := a.deps.ReleaseForm.EditRelease(cmd.Context(), "", initial)
				if err != nil {
					return err
				}
				fields.tag, fields.title, fields.draft, fields.prerelease = values.Tag, values.Title, values.Draft, values.Prerelease
				if values.Notes != "" {
					opts.Body = &values.Notes
				}
				if fields.target = values.Target; fields.target != "" {
					opts.Target = &fields.target
				}
			} else if prompted, err = a.requireInput(cmd, &fields.tag, "tag", "Tag", nil); err != nil {
				return err
			}
			title := cmp.Or(fields.title, fields.tag)
			opts.Tag, opts.Title, opts.Draft, opts.Prerelease = &fields.tag, &title, &fields.draft, &fields.prerelease
			service := services.Releases{Client: target.client, Repository: target.Repo}
			if err := a.resolveReleaseTarget(cmd.Context(), target, clone, &opts); err != nil {
				return err
			}
			if err := a.checkReleaseTag(cmd.Context(), target, service, clone, remote, fields.tag, opts.Target != nil, verifyTag); err != nil {
				return err
			}
			if fields.editor {
				if editorDraft, err = a.editDraft(cmd, ""); err != nil {
					return err
				}
				opts.Body = &editorDraft.Body
			}
			if prompted {
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Create release " + fields.tag + " in " + target.Repo + "?", DefaultYes: true}); err != nil {
					return err
				}
			}
			item, err := service.Create(cmd.Context(), opts)
			if err = a.explainTarget(cmd.Context(), target, err, fields.target); err != nil {
				return err
			}
			kind := "release"
			if item.Draft {
				kind = "draft release"
			}
			a.done(cmd, "Created %s %s in %s", kind, item.Tag, target.Repo)
			failures := uploadAttachments(cmd.Context(), files, item.ID, service.UploadAsset, func(asset services.ReleaseAsset) {
				item.Assets = append(item.Assets, asset)
				a.done(cmd, "Uploaded %s (%s)", asset.Name, terminal.Bytes(asset.Size))
			})
			return errors.Join(followUpError("release "+item.Tag, "created", failures), a.created(cmd, item, item.URL))
		},
	}
	fields.register(a, command)
	command.Flags().StringSliceVar(&assets, "asset", nil, "File to upload as an asset (repeatable)")
	command.Flags().BoolVar(&verifyTag, "verify-tag", false, "Fail if the tag does not exist on the server yet")
	command.ValidArgsFunction = pathArgs(1, true, completeFiles, a.completeTags())
	return command
}

// checkReleaseTag guards against a release whose tag Gitea would create from the default branch
// instead of from the local commit: a tag that only exists locally is refused unless --target
// names where to create it, a new tag without --target is refused unless HEAD is the head of the
// default branch, and --verify-tag refuses any tag the server lacks.
func (a *application) checkReleaseTag(ctx context.Context, t target, service services.Releases, clone bool, remote, tag string, hasTarget, verify bool) error {
	if !verify && (!clone || hasTarget) {
		return nil
	}
	exists, err := service.TagExists(ctx, tag)
	if err != nil || exists {
		return err
	}
	missing := services.NotFound("tag", tag, t.Repo)
	switch {
	case clone && a.localRefExists(ctx, "refs/tags/"+tag):
		hint := pushRefAdvice(t, remote, tag)
		if !verify {
			hint += ", or pass --target to create it on the server"
		}
		return explain(missing, "%s; %s", missing, hint)
	case verify:
		return explain(missing, "%s; create it first or drop --verify-tag", missing)
	}
	head := a.localCommit(ctx, "HEAD")
	if head == "" {
		return nil
	}
	repository, err := services.Repositories{Client: t.client}.View(ctx, t.Repo)
	if err != nil || repository.DefaultBranch == "" {
		return err
	}
	branch, err := services.Branches{Client: t.client}.View(ctx, t.Repo, repository.DefaultBranch)
	if services.IsNotFound(err) || err == nil && branch.SHA == head {
		return nil // Gitea itself refuses a release in an empty repository.
	}
	if err != nil {
		return err
	}
	advice := "pass --target HEAD to create it at HEAD"
	if _, err := (services.Repositories{Client: t.client}).Commit(ctx, t.Repo, head); services.IsNotFound(err) {
		advice = "push HEAD to " + t.Repo + " first, then pass --target HEAD to create it at HEAD"
		if command := a.pushCommitCommand(ctx, remote, "HEAD"); command != "" {
			advice = "push HEAD first (" + command + "), then pass --target HEAD to create it at HEAD"
		}
	} else if err != nil {
		return err
	}
	return explain(missing, "%s, and Gitea would create it at %s (%s), not at HEAD (%s); %s, or --target %s",
		missing, repository.DefaultBranch, shortSHA(branch.SHA), shortSHA(head), advice, repository.DefaultBranch)
}

// resolveReleaseTarget points opts.Target at what serverRef resolves it to.
func (a *application) resolveReleaseTarget(ctx context.Context, t target, clone bool, opts *services.ReleaseOptions) error {
	if opts.Target == nil {
		return nil
	}
	resolved, err := a.serverRef(ctx, t, clone, *opts.Target)
	if err == nil {
		opts.Target = &resolved
	}
	return err
}

// explainTarget adds a pushHint for ref to an error about a target the server lacks.
func (a *application) explainTarget(ctx context.Context, t target, err error, ref string) error {
	if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Kind == "target" {
		if hint := a.pushHint(ctx, t, ref); hint != "" {
			return explain(err, "%s; %s", err, hint)
		}
	}
	return err
}

// serverRef resolves a ref that Gitea would resolve on the server, such as HEAD or a
// short SHA, to the commit it names in the clone; a branch of the server stays as it is.
func (a *application) serverRef(ctx context.Context, t target, clone bool, ref string) (string, error) {
	if isFullSHA(ref) || !clone {
		return ref, nil
	}
	sha := a.localCommit(ctx, ref)
	if sha == "" {
		return ref, nil
	}
	if ref == "HEAD" { // no branch can be named HEAD
		return sha, nil
	}
	_, err := services.Branches{Client: t.client}.View(ctx, t.Repo, ref)
	switch {
	case err == nil:
		return ref, nil
	case services.IsNotFound(err):
		return sha, nil
	}
	return "", err
}

// pushHint tells how to publish a ref that only the local clone has, or returns "".
func (a *application) pushHint(ctx context.Context, t target, ref string) string {
	remote, clone := a.cloneRemote(ctx, t)
	if !clone {
		return ""
	}
	for _, prefix := range []string{"refs/tags/", "refs/heads/"} {
		if a.localRefExists(ctx, prefix+ref) {
			return pushRefAdvice(t, remote, ref)
		}
	}
	if ref == "" || !a.localRefExists(ctx, ref+"^{commit}") {
		return ""
	}
	if command := a.pushCommitCommand(ctx, remote, ref); command != "" {
		return "push a branch that has this commit first: " + command
	}
	return "push a branch that has this commit to " + t.Repo + " first"
}

// pushRefAdvice tells how to push the local branch or tag ref to t through remote, if known.
func pushRefAdvice(t target, remote, ref string) string {
	if remote == "" {
		return "push it to " + t.Repo + " first"
	}
	return "push it first: git push " + remote + " " + ref
}

// pushCommitCommand returns the Git command that pushes the commit ref to a branch of remote: the
// checked-out branch when it holds ref, else a new one. It is "" when no remote is known.
func (a *application) pushCommitCommand(ctx context.Context, remote, ref string) string {
	if remote == "" {
		return ""
	}
	if branch, err := a.currentBranch(ctx); err == nil && (ref == "HEAD" || a.localCommit(ctx, ref) == a.localCommit(ctx, "HEAD")) {
		return "git push -u " + remote + " " + branch
	}
	return "git push " + remote + " " + ref + ":refs/heads/BRANCH"
}

// localRefExists reports whether the working directory's checkout resolves ref.
func (a *application) localRefExists(ctx context.Context, ref string) bool {
	_, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// cloneRemote reports whether the working directory is a clone of the target repository, also
// when --repo or gt.repository selected it, and returns its Git remote that points at the
// repository, or "" when none does.
func (a *application) cloneRemote(ctx context.Context, t target) (string, bool) {
	if remote, ok := strings.CutPrefix(t.RepoSource, "remote "); ok {
		return remote, true
	}
	for _, remote := range a.gitRemotes(ctx) {
		if t.ownsRemote(remote[1]) {
			return remote[0], true
		}
	}
	return "", t.repoFromClone()
}

// isCloneOf reports whether the working directory is a clone of the target repository.
func (a *application) isCloneOf(ctx context.Context, t target) bool {
	_, clone := a.cloneRemote(ctx, t)
	return clone
}

// rejectSwitchValue refuses the old gt syntax "--draft false": a switch reads no value, so the
// word would be edited as one more release after the switch was applied as true.
func rejectSwitchValue(cmd *cobra.Command, args []string, switches ...string) error {
	for _, name := range switches {
		if !cmd.Flags().Changed(name) {
			continue
		}
		for _, arg := range args {
			if strings.EqualFold(arg, "true") || strings.EqualFold(arg, "false") {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s takes no value; use --%s or --%s=false", name, name, name))
			}
		}
	}
	return nil
}

// errNotesCleared refuses empty release notes on edit.
var errNotesCleared = clierr.Wrap(clierr.Usage, errors.New("release notes cannot be cleared because Gitea ignores empty notes; edit them to a placeholder, or delete and recreate the release"))

func (a *application) releaseEditCommand() *cobra.Command {
	var fields releaseFields
	command := &cobra.Command{
		Use:   "edit <tag>...",
		Short: "Edit releases",
		Long: `Edit the title, notes, tag, target, draft or prerelease state of releases. Without flags on a
terminal, a form starts from the current values of each release and only what it changes is sent.
In a clone, a target that is no branch on the server, such as HEAD or a short SHA, names the commit
it resolves to in the clone.`,
		Example: `# Edit a release in a form
$ gt release edit v1.0.0

$ gt release edit v1.0.0 --title "1.0.0" --notes-file CHANGELOG.md
$ gt release edit v1.0.0-rc1 v1.0.0-rc2 --prerelease=false`,
		Args: nonBlankArgs(cobra.MinimumNArgs(1), "tag"),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var editorDraft terminal.Draft
			defer finishDraft(&editorDraft, &runErr)
			if err := rejectSwitchValue(cmd, args, "draft", "prerelease"); err != nil {
				return err
			}
			if err := validateTextSources(cmd, "notes", "notes-file"); err != nil {
				return err
			}
			if fields.editor && len(args) != 1 {
				return clierr.Wrap(clierr.Usage, errors.New("--editor requires one release"))
			}
			opts, err := fields.options(a, cmd)
			if err != nil {
				return err
			}
			if opts.Body != nil && *opts.Body == "" {
				return errNotesCleared
			}
			if cmd.Flags().Changed("tag") {
				if len(args) != 1 || strings.TrimSpace(fields.tag) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("--tag requires one release and a nonempty new tag"))
				}
				opts.Tag = &fields.tag
			}
			form := !localFlagsChanged(cmd) && a.canPrompt() && a.deps.ReleaseForm != nil
			if !form && !fields.editor && opts == (services.ReleaseOptions{}) {
				return clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --title or --notes (see --help)"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			clone := a.isCloneOf(cmd.Context(), target)
			if form {
				return a.editReleasesInteractively(cmd, target, service, clone, args)
			}
			if err := a.resolveReleaseTarget(cmd.Context(), target, clone, &opts); err != nil {
				return err
			}
			if fields.editor {
				item, err := service.View(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if editorDraft, err = a.editDraft(cmd, item.Body); err != nil {
					return err
				}
				opts.Body = &editorDraft.Body
			}
			if editorDraft.Body == "" && fields.editor {
				return errNotesCleared
			}
			return batchDone(a, cmd, args, func(tag string) string { return "release " + tag }, func(tag string) (services.Release, error) {
				item, err := service.View(cmd.Context(), tag)
				if err != nil {
					return item, releaseError(err, target.Repo)
				}
				opts := opts
				if err := keepReleaseCommit(cmd.Context(), service, item, &opts); err != nil {
					return item, err
				}
				edited, err := service.Edit(cmd.Context(), item, opts)
				return edited, a.explainTarget(cmd.Context(), target, err, cmp.Or(fields.target, item.Target))
			}, func(item services.Release) string { return "Edited release " + item.Tag + " in " + target.Repo })
		},
	}
	fields.register(a, command)
	command.Flags().StringVar(&fields.tag, "tag", "", "Change the release's tag (one release only)")
	command.ValidArgsFunction = a.completeReleases()
	return command
}

func (a *application) editReleasesInteractively(cmd *cobra.Command, target target, service services.Releases, clone bool, tags []string) error {
	results, failures := applyBatch(cmd, tags, func(tag string) string { return "release " + tag }, func(tag string) (services.Release, error) {
		item, err := service.View(cmd.Context(), tag)
		if err != nil {
			return item, releaseError(err, target.Repo)
		}
		current := terminal.ReleaseFormValues{Tag: item.Tag, Title: item.Title, Notes: item.Body, Target: item.Target, Draft: item.Draft, Prerelease: item.Prerelease, Editor: a.editorCommand()}
		values, err := a.deps.ReleaseForm.EditRelease(cmd.Context(), "Editing release "+item.Tag+" in "+target.Repo, current)
		if err != nil {
			return item, abortBatch{err}
		}
		opts, err := changedReleaseFields(current, values)
		if err != nil {
			return item, err
		}
		if opts == (services.ReleaseOptions{}) {
			a.warn(cmd, "No changes to release %s in %s", item.Tag, target.Repo)
			return item, nil
		}
		if err := a.resolveReleaseTarget(cmd.Context(), target, clone, &opts); err != nil {
			return item, err
		}
		if err := keepReleaseCommit(cmd.Context(), service, item, &opts); err != nil {
			return item, err
		}
		if item, err = service.Edit(cmd.Context(), item, opts); err == nil {
			a.done(cmd, "Edited release %s in %s", item.Tag, target.Repo)
		}
		return item, a.explainTarget(cmd.Context(), target, err, values.Target)
	}, nil)
	return errors.Join(append(failures, writeBatchJSON(a, cmd, len(tags), results))...)
}

// keepReleaseCommit keeps a published release on its commit when opts renames its tag to one the
// server lacks, which Gitea would otherwise create at the head of the release's target branch.
func keepReleaseCommit(ctx context.Context, service services.Releases, item services.Release, opts *services.ReleaseOptions) error {
	if opts.Tag == nil || *opts.Tag == item.Tag || opts.Target != nil || item.Draft {
		return nil
	}
	exists, err := service.TagExists(ctx, *opts.Tag)
	if err != nil || exists {
		return err
	}
	sha, err := service.TagCommit(ctx, item.Tag)
	if services.IsNotFound(err) {
		return explain(err, "%s; pass --target to choose the commit of tag %s", err, *opts.Tag)
	}
	if err != nil {
		return err
	}
	opts.Target = &sha
	return nil
}

// changedReleaseFields sets only the fields that the edit form changed, so that an unchanged form
// writes nothing. An emptied title becomes the tag, as on create.
func changedReleaseFields(current, values terminal.ReleaseFormValues) (services.ReleaseOptions, error) {
	var opts services.ReleaseOptions
	if values.Notes != current.Notes {
		if strings.TrimSpace(values.Notes) == "" {
			return opts, errNotesCleared
		}
		opts.Body = &values.Notes
	}
	if values.Tag != current.Tag {
		opts.Tag = &values.Tag
	}
	if values.Title != current.Title {
		title := cmp.Or(values.Title, values.Tag)
		opts.Title = &title
	}
	if values.Target != current.Target {
		opts.Target = &values.Target
	}
	if values.Draft != current.Draft {
		opts.Draft = &values.Draft
	}
	if values.Prerelease != current.Prerelease {
		opts.Prerelease = &values.Prerelease
	}
	return opts, nil
}

// releaseError words the failure to find one release of a batch, which the batch prefixes with
// the release, as "release v9: not found in alice/x".
func releaseError(err error, repo string) error {
	if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Kind == "release" {
		return explain(err, "not found in %s", repo)
	}
	return err
}

func (a *application) releasePublishCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "publish <tag>...",
		Short:   "Publish draft releases",
		Example: "$ gt release publish v1.1.0",
		Args:    nonBlankArgs(cobra.MinimumNArgs(1), "tag"),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			results, failures := applyBatch(cmd, args, func(tag string) string { return "release " + tag }, func(tag string) (services.Release, error) {
				item, err := service.View(cmd.Context(), tag)
				if err != nil {
					return item, releaseError(err, target.Repo)
				}
				if !item.Draft {
					a.warn(cmd, "Release %s is already published", item.Tag)
					return item, nil
				}
				published := false
				if item, err = service.Edit(cmd.Context(), item, services.ReleaseOptions{Draft: &published}); err == nil {
					a.done(cmd, "Published release %s in %s", item.Tag, target.Repo)
				}
				return item, err
			}, nil)
			return errors.Join(append(failures, writeBatchJSON(a, cmd, len(args), results))...)
		},
	}
	command.ValidArgsFunction = a.completeReleases()
	return command
}

func (a *application) releaseDeleteCommand() *cobra.Command {
	var deleteTag bool
	command := &cobra.Command{
		Use:   "delete <tag>...",
		Short: "Delete releases",
		Long:  "Delete releases. Their tags stay unless --delete-tag removes them too.",
		Example: `$ gt release delete v1.0.0-rc1
$ gt release delete v1.0.0-rc1 v1.0.0-rc2 --delete-tag --yes`,
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "release deletion does not return a resource"},
		Args:        nonBlankArgs(cobra.MinimumNArgs(1), "tag"),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			subject := "release " + args[0]
			if len(args) > 1 {
				subject = fmt.Sprintf("%d releases (%s)", len(args), strings.Join(args, ", "))
			}
			if deleteTag {
				subject += " with their tags"
				if len(args) == 1 {
					subject = "release " + args[0] + " and its tag"
				}
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + " from " + target.Repo + "?"}); err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			return batchDone(a, cmd, args, func(tag string) string { return "release " + tag }, func(tag string) (services.Release, error) {
				item, err := service.View(cmd.Context(), tag)
				if err != nil {
					return item, releaseError(err, target.Repo)
				}
				return item, service.Delete(cmd.Context(), item, deleteTag)
			}, func(item services.Release) string {
				if deleteTag {
					return "Deleted release " + item.Tag + " and its tag from " + target.Repo
				}
				return "Deleted release " + item.Tag + " from " + target.Repo + " (tag kept; use --delete-tag)"
			})
		},
	}
	command.Flags().BoolVar(&deleteTag, "delete-tag", false, "Delete the release's Git tag too")
	command.ValidArgsFunction = a.completeReleases()
	return a.dryRunnable(command)
}
