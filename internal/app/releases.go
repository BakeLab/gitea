// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	return jsonOutput(group, jsonFields[services.Release]())
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

// releaseTime is when a release was published, or created for a draft.
func releaseTime(item services.Release) string {
	if item.PublishedAt != nil && !item.Draft {
		return *item.PublishedAt
	}
	return item.CreatedAt
}

func (a *application) releaseListCommand() *cobra.Command {
	var limit, page int
	command := &cobra.Command{
		Use:   "list",
		Short: "List releases",
		Example: `$ gt release list
$ gt release list -R alice/project --json tag,title,draft`,
		Args: argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			result, err := (services.Releases{Client: target.client, Repository: target.Repo}).ListPage(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			// Releases come newest first, so the first published one of the first page is the latest.
			var latest int64
			for _, item := range result.Items {
				if page == 1 && !item.Draft && !item.Prerelease {
					latest = item.ID
					break
				}
			}
			spec := listSpec[services.Release]{
				Columns: []terminal.Column{{Header: "Title", Flex: true}, {Header: "Type"}, {Header: "Tag"}, {Header: "Published"}, {Header: "URL", PipedOnly: true}},
				Row: func(item services.Release) []terminal.Cell {
					return []terminal.Cell{{Text: cmp.Or(item.Title, item.Tag), Link: item.URL}, releaseKind(item, latest), {Text: item.Tag}, {Time: terminal.ParseTime(releaseTime(item))}, {Text: item.URL}}
				},
				Context: "releases in " + target.Repo, Empty: "no releases in " + target.Repo,
			}
			return writeTable(a, cmd, result.Items, spec, servicePage(result, page))
		},
	}
	pageFlags(command, &limit, &page)
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
		Args: usageArgs(cobra.MaximumNArgs(1)),
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
	if when := terminal.ParseTime(releaseTime(item)); !when.IsZero() {
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
	view.Fields = []terminal.Field{{Label: "Tag", Value: item.Tag, PipedOnly: true}, {Label: "Assets", Value: strings.Join(assets, ", ")}}
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

gt never pushes: commits and tags that only exist in your clone are not on the server, so push them
first (git push <remote> <tag>), or pass --verify-tag to fail when the tag is not on the server yet.`,
		Example: `# Create a release with notes from a file and two assets
$ gt release create v1.0.0 dist/gt-linux dist/gt-darwin --notes-file CHANGELOG.md

# Create a draft prerelease of a new tag on a branch
$ gt release create v1.1.0-rc1 --target release/1.1 --draft --prerelease --title "1.1 RC 1"`,
		Args: usageArgs(cobra.ArbitraryArgs),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var editorDraft terminal.Draft
			defer finishDraft(&editorDraft, &runErr)
			if len(args) > 0 {
				fields.tag, assets = args[0], append(args[1:], assets...)
			}
			if err := validateTextSources(cmd, "notes", "notes-file"); err != nil {
				return err
			}
			if fields.tag == "" && fields.notesFile == "-" {
				return clierr.Wrap(clierr.Usage, errors.New("--notes-file - requires a tag; stdin cannot also supply a prompt"))
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
			prompted, err := a.requireInput(cmd, &fields.tag, "tag", "Tag", nil)
			if err != nil {
				return err
			}
			title := cmp.Or(fields.title, fields.tag)
			opts.Tag, opts.Title, opts.Draft, opts.Prerelease = &fields.tag, &title, &fields.draft, &fields.prerelease
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			if err := a.checkReleaseTag(cmd, target, service, fields.tag, opts.Target != nil, verifyTag); err != nil {
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
			if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Kind == "target" {
				if hint := a.pushHint(cmd.Context(), target, *opts.Target); hint != "" {
					err = explain(err, "%s; %s", err, hint)
				}
			}
			if err != nil {
				return err
			}
			kind := "release"
			if item.Draft {
				kind = "draft release"
			}
			a.done(cmd, "Created %s %s in %s", kind, item.Tag, target.Repo)
			var failures []error
			for _, file := range files {
				name := filepath.Base(file.Name())
				asset, err := service.UploadAsset(cmd.Context(), item.ID, name, file)
				if err != nil {
					failures = append(failures, fmt.Errorf("release %s created; asset %s upload failed: %w", item.Tag, name, err))
					continue
				}
				item.Assets = append(item.Assets, asset)
				a.done(cmd, "Uploaded %s (%s)", asset.Name, terminal.Bytes(asset.Size))
			}
			return errors.Join(append(failures, a.created(cmd, item, item.URL))...)
		},
	}
	fields.register(a, command)
	command.Flags().StringSliceVar(&assets, "asset", nil, "File to upload as an asset (repeatable)")
	command.Flags().BoolVar(&verifyTag, "verify-tag", false, "Fail if the tag does not exist on the server yet")
	command.ValidArgsFunction = pathArgs(1, true, completeFiles, a.completeTags())
	return command
}

// checkReleaseTag guards against a release whose tag Gitea would create from the default branch
// instead of from the local commit the tag names: --verify-tag refuses a tag the server lacks, and
// without it a tag that only exists locally is warned about.
func (a *application) checkReleaseTag(cmd *cobra.Command, t target, service services.Releases, tag string, hasTarget, verify bool) error {
	local := a.localRefExists(cmd.Context(), "refs/tags/"+tag)
	if !verify && (!local || hasTarget) {
		return nil
	}
	exists, err := service.TagExists(cmd.Context(), tag)
	if err != nil || exists {
		return err
	}
	hint := ""
	if local {
		hint = "push it first: git push " + a.releaseRemote(t) + " " + tag
	}
	if verify {
		missing := services.NotFound("tag", tag, t.Repo)
		return explain(missing, "%s; %s", missing, cmp.Or(hint, "create it first or drop --verify-tag"))
	}
	a.warn(cmd, "Tag %s exists in your clone but not in %s; the release creates it at the default branch, not at your local commit; %s", tag, t.Repo, hint)
	return nil
}

// pushHint tells how to publish a ref that only the local clone has, or returns "".
func (a *application) pushHint(ctx context.Context, t target, ref string) string {
	remote := a.releaseRemote(t)
	for _, prefix := range []string{"refs/tags/", "refs/heads/"} {
		if a.localRefExists(ctx, prefix+ref) {
			return "push it first: git push " + remote + " " + ref
		}
	}
	if ref != "" && a.localRefExists(ctx, ref+"^{commit}") {
		return "push the branch that has this commit first: git push " + remote
	}
	return ""
}

// localRefExists reports whether the working directory's checkout resolves ref.
func (a *application) localRefExists(ctx context.Context, ref string) bool {
	_, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// releaseRemote is the remote of the target repository, else origin.
func (a *application) releaseRemote(t target) string {
	if remote, ok := strings.CutPrefix(t.RepoSource, "remote "); ok {
		return remote
	}
	return cmp.Or(a.options.remote, "origin")
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

func (a *application) releaseEditCommand() *cobra.Command {
	var fields releaseFields
	command := &cobra.Command{
		Use:   "edit <tag>...",
		Short: "Edit releases",
		Example: `$ gt release edit v1.0.0 --title "1.0.0" --notes-file CHANGELOG.md
$ gt release edit v1.0.0-rc1 v1.0.0-rc2 --prerelease=false`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
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
				return clierr.Wrap(clierr.Usage, errors.New("release notes cannot be cleared because Gitea ignores empty notes; edit them to a placeholder, or delete and recreate the release"))
			}
			if cmd.Flags().Changed("tag") {
				if len(args) != 1 || strings.TrimSpace(fields.tag) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("--tag requires one release and a nonempty new tag"))
				}
				opts.Tag = &fields.tag
			}
			if !fields.editor && opts == (services.ReleaseOptions{}) {
				return clierr.Wrap(clierr.Usage, errors.New("supply release fields to edit"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
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
				return clierr.Wrap(clierr.Usage, errors.New("release notes cannot be cleared because Gitea ignores empty notes; edit them to a placeholder, or delete and recreate the release"))
			}
			return batchDone(a, cmd, args, func(tag string) string { return "release " + tag }, func(tag string) (services.Release, error) {
				item, err := service.View(cmd.Context(), tag)
				if err != nil {
					return item, err
				}
				return service.Edit(cmd.Context(), item.ID, opts)
			}, func(item services.Release) string { return "Edited release " + item.Tag + " in " + target.Repo })
		},
	}
	fields.register(a, command)
	command.Flags().StringVar(&fields.tag, "tag", "", "Change the release's tag (one release only)")
	command.ValidArgsFunction = a.completeReleases()
	return command
}

func (a *application) releasePublishCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "publish <tag>...",
		Short:   "Publish draft releases",
		Example: "$ gt release publish v1.1.0",
		Args:    usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			results, failures := applyBatch(cmd, args, func(tag string) string { return "release " + tag }, func(tag string) (services.Release, error) {
				item, err := service.View(cmd.Context(), tag)
				if err != nil {
					return item, err
				}
				if !item.Draft {
					a.warn(cmd, "Release %s is already published", item.Tag)
					return item, nil
				}
				published := false
				if item, err = service.Edit(cmd.Context(), item.ID, services.ReleaseOptions{Draft: &published}); err == nil {
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
		Args:        usageArgs(cobra.MinimumNArgs(1)),
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
				if err == nil {
					err = service.Delete(cmd.Context(), item, deleteTag)
				}
				return item, err
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
	return command
}
