// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) releaseAssetCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "asset",
		Short: "Manage release assets",
		Long:  "List, upload, download, rename and delete the files attached to a release. Assets are named by name or ID.",
	}
	group.AddCommand(a.assetListCommand(), a.releaseUploadCommand(), a.releaseDownloadCommand(), a.assetEditCommand(), a.assetDeleteCommand())
	return group
}

// assetFinder addresses the assets of the release tagged tag by name or ID.
func assetFinder(tag string, byName bool) services.NameOrID[services.ReleaseAsset] {
	return services.NameOrID[services.ReleaseAsset]{
		Kind: "asset", Scope: "release " + tag, ByName: byName,
		ID: func(asset services.ReleaseAsset) int64 { return asset.ID }, Name: func(asset services.ReleaseAsset) string { return asset.Name },
	}
}

// completeReleaseAssets completes a release tag, then the asset names of the release it tags.
func (a *application) completeReleaseAssets() cobra.CompletionFunc {
	releases := a.completeReleases()
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return releases(cmd, args, toComplete)
		}
		tag := args[0]
		return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			release, err := services.Releases{Client: t.client, Repository: t.Repo}.View(ctx, tag)
			return describe(release.Assets, func(asset services.ReleaseAsset) (string, string) { return asset.Name, terminal.Bytes(asset.Size) }), err
		})(cmd, args, toComplete)
	}
}

func (a *application) assetListCommand() *cobra.Command {
	var limit, page int
	command := &cobra.Command{
		Use:     "list <tag>",
		Short:   "List release assets",
		Example: "$ gt release asset list v1.0.0",
		Args:    argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			release, err := service.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			items, err := service.Assets(cmd.Context(), release.ID, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.ReleaseAsset]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Name", Flex: true}, {Header: "Size"}, {Header: "Downloads"}, {Header: "Created"}, {Header: "URL", PipedOnly: true}},
				Row: func(item services.ReleaseAsset) []terminal.Cell {
					size := terminal.Bytes(item.Size)
					if !a.deps.Streams.OutTTY {
						size = strconv.FormatInt(item.Size, 10)
					}
					return []terminal.Cell{{Text: strconv.FormatInt(item.ID, 10), Link: item.URL}, {Text: item.Name}, {Text: size}, {Text: strconv.FormatInt(item.DownloadCount, 10)}, {Time: terminal.ParseTime(item.CreatedAt)}, {Text: item.URL}}
				},
				Context: "assets of release " + release.Tag + " in " + target.Repo, Empty: "no assets in release " + release.Tag + " of " + target.Repo,
			}
			return writeTable(a, cmd, items, spec, listPage{Number: page, More: len(release.Assets) > page*limit})
		},
	}
	pageFlags(command, &limit, &page)
	command.ValidArgsFunction = a.completeReleases()
	return command
}

func (a *application) releaseUploadCommand() *cobra.Command {
	var name string
	var clobber bool
	command := &cobra.Command{
		Use:   "upload <tag> <file>...",
		Short: "Upload release assets",
		Long: `Upload files as assets of a release, named after the files unless --name renames a single file.
An asset with the same name is replaced only with --clobber.`,
		Example: `$ gt release upload v1.0.0 dist/gt-linux dist/gt-darwin
$ gt release upload v1.0.0 dist/gt-linux --clobber`,
		Args: usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("name") && (len(args) != 2 || strings.TrimSpace(name) == "") {
				return clierr.Wrap(clierr.Usage, errors.New("--name requires exactly one file and a nonempty name"))
			}
			files, err := openRegularFiles(args[1:])
			defer closeFiles(files)
			if err != nil {
				return err
			}
			filename := func(file *os.File) string {
				if cmd.Flags().Changed("name") {
					return name
				}
				return filepath.Base(file.Name())
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			release, err := service.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			replaced := map[string]services.ReleaseAsset{}
			for _, file := range files {
				for _, asset := range release.Assets {
					if asset.Name == filename(file) {
						if !clobber {
							return clierr.Wrap(clierr.Usage, fmt.Errorf("asset %s already exists in release %s; pass --clobber to replace it", asset.Name, release.Tag))
						}
						replaced[asset.Name] = asset
					}
				}
			}
			return batchDone(a, cmd, files, func(file *os.File) string { return "upload asset " + filename(file) }, func(file *os.File) (services.ReleaseAsset, error) {
				if old, ok := replaced[filename(file)]; ok {
					if err := service.DeleteAsset(cmd.Context(), release.ID, old.ID); err != nil {
						return services.ReleaseAsset{}, fmt.Errorf("replace asset %s: %w", old.Name, err)
					}
				}
				return service.UploadAsset(cmd.Context(), release.ID, filename(file), file)
			}, func(asset services.ReleaseAsset) string {
				return fmt.Sprintf("Uploaded %s (%s) to release %s", asset.Name, terminal.Bytes(asset.Size), release.Tag)
			})
		},
	}
	command.Flags().StringVar(&name, "name", "", "Asset name (default: the file's name)")
	command.Flags().BoolVar(&clobber, "clobber", false, "Replace assets with the same name")
	command.ValidArgsFunction = a.completeReleases()
	return command
}

func (a *application) releaseDownloadCommand() *cobra.Command {
	var flags downloadFlags
	var patterns []string
	var archive string
	command := &cobra.Command{
		Use:   "download [<tag>] [<asset>...]",
		Short: "Download release assets",
		Long: `Download assets of a release, by name or ID, or those matching --pattern; without either, every
asset. --archive downloads the source code archive instead. Without a tag, the latest release is used.

Files are written to the current directory under their names, to --dir, or to --output; an existing
file is replaced only with --clobber. --output - writes one asset to standard output.`,
		Example: `# Download every asset of the latest release
$ gt release download

# Download the Linux builds of a release into dist/
$ gt release download v1.0.0 --pattern '*linux*' --dir dist

# Download the source code as a zip file
$ gt release download v1.0.0 --archive zip`,
		Annotations: textOutput("asset downloads are files"),
		Args:        usageArgs(cobra.ArbitraryArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, pattern := range patterns {
				if _, err := path.Match(pattern, ""); err != nil {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --pattern %q", pattern))
				}
			}
			tag, names := "", []string{}
			if len(args) > 0 {
				tag, names = args[0], args[1:]
			}
			if archive != "" && (len(names) > 0 || len(patterns) > 0) {
				return clierr.Wrap(clierr.Usage, errors.New("--archive downloads the source code only; it cannot be combined with assets or --pattern"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			release, err := viewRelease(cmd, service, tag)
			if err != nil {
				return err
			}
			if archive != "" {
				return a.downloadArchive(cmd, service, flags, release.Tag, archive)
			}
			assets, err := selectAssets(release, names, patterns)
			if err != nil {
				return err
			}
			if len(assets) > 1 && flags.output != "" && !isDirectory(flags.output) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--output names one file, but %d assets match; pass --dir", len(assets)))
			}
			paths := make([]string, len(assets))
			skipped := make([]bool, len(assets))
			for i, asset := range assets {
				if paths[i], skipped[i], err = flags.destination(asset.Name); err != nil {
					return err
				}
			}
			for i, asset := range assets {
				if skipped[i] {
					a.warn(cmd, "Skipped %s; %s already exists", asset.Name, paths[i])
					continue
				}
				body, err := service.DownloadAsset(cmd.Context(), release.ID, asset.ID)
				if err != nil {
					return fmt.Errorf("download %s: %w", asset.Name, err)
				}
				err = a.download(cmd, flags, paths[i], body)
				_ = body.Close()
				if err != nil {
					return err
				}
				if paths[i] != "-" {
					a.done(cmd, "Downloaded %s (%s) to %s", asset.Name, terminal.Bytes(asset.Size), paths[i])
				}
			}
			return nil
		},
	}
	flags.register(command)
	command.Flags().StringSliceVarP(&patterns, "pattern", "p", nil, "Download the assets whose names match this glob (repeatable)")
	enumFlag(command, &archive, "archive", "A", "", "Download the source code archive", "zip", "tar.gz")
	command.ValidArgsFunction = a.completeReleaseAssets()
	return command
}

// selectAssets returns the assets of release that names (names or IDs) and patterns (globs)
// select, or every asset when there are neither, each once.
func selectAssets(release services.Release, names, patterns []string) ([]services.ReleaseAsset, error) {
	if len(names) == 0 && len(patterns) == 0 {
		if len(release.Assets) == 0 {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("release %s has no assets; pass --archive to download its source code", release.Tag))
		}
		return release.Assets, nil
	}
	var selected []services.ReleaseAsset
	add := func(asset services.ReleaseAsset) {
		if !slices.ContainsFunc(selected, func(other services.ReleaseAsset) bool { return other.ID == asset.ID }) {
			selected = append(selected, asset)
		}
	}
	finder := assetFinder(release.Tag, false)
	for _, name := range names {
		asset, err := finder.Find(release.Assets, name)
		if err != nil {
			return nil, err
		}
		add(asset)
	}
	for _, pattern := range patterns {
		matched := false
		for _, asset := range release.Assets {
			if ok, _ := path.Match(pattern, asset.Name); ok {
				add(asset)
				matched = true
			}
		}
		if !matched {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("no asset of release %s matches %q", release.Tag, pattern))
		}
	}
	return selected, nil
}

// downloadArchive downloads the source code of the tag as a zip or tar.gz file.
func (a *application) downloadArchive(cmd *cobra.Command, service services.Releases, flags downloadFlags, tag, format string) error {
	_, repo, _ := strings.Cut(service.Repository, "/")
	name := repo + "-" + strings.ReplaceAll(tag, "/", "-") + "." + format
	destination, skip, err := flags.destination(name)
	if err != nil || skip {
		if skip {
			a.warn(cmd, "Skipped %s; %s already exists", name, destination)
		}
		return err
	}
	body, err := service.Archive(cmd.Context(), tag, format)
	if err != nil {
		return err
	}
	defer body.Close()
	counter := &countingReader{reader: body}
	if err := a.download(cmd, flags, destination, counter); err != nil {
		return err
	}
	if destination != "-" {
		a.done(cmd, "Downloaded %s (%s) to %s", name, terminal.Bytes(counter.n), destination)
	}
	return nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	reader io.Reader
	n      int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.n += int64(n)
	return n, err
}

func (a *application) assetEditCommand() *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:     "edit <tag> <asset>",
		Short:   "Rename a release asset",
		Example: "$ gt release asset edit v1.0.0 gt-linux --name gt-linux-amd64",
		Args:    argsExact(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(name) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("supply --name for asset rename"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			release, err := service.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			asset, err := assetFinder(release.Tag, false).Find(release.Assets, args[1])
			if err != nil {
				return err
			}
			item, err := service.RenameAsset(cmd.Context(), release.ID, asset.ID, name)
			if err != nil {
				return err
			}
			a.done(cmd, "Renamed asset %s to %s in release %s", asset.Name, item.Name, release.Tag)
			return a.writeMutationJSON(cmd, item)
		},
	}
	command.Flags().StringVar(&name, "name", "", "New asset name")
	command.ValidArgsFunction = a.completeReleaseAssets()
	return command
}

func (a *application) assetDeleteCommand() *cobra.Command {
	var byName bool
	command := &cobra.Command{
		Use:   "delete <tag> <asset>...",
		Short: "Delete release assets",
		Example: `$ gt release asset delete v1.0.0 gt-linux
$ gt release asset delete v1.0.0 12 13 --yes`,
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "asset deletion does not return a resource"},
		Args:        usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
			release, err := service.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			finder := assetFinder(release.Tag, byName)
			var assets []services.ReleaseAsset
			for _, raw := range args[1:] {
				asset, err := finder.Find(release.Assets, raw)
				if err != nil {
					return err
				}
				if slices.ContainsFunc(assets, func(other services.ReleaseAsset) bool { return other.ID == asset.ID }) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("asset %s is selected more than once", asset.Name))
				}
				assets = append(assets, asset)
			}
			names := make([]string, len(assets))
			for i, asset := range assets {
				names[i] = asset.Name
			}
			subject := "asset " + names[0]
			if len(names) > 1 {
				subject = fmt.Sprintf("%d assets (%s)", len(names), strings.Join(names, ", "))
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + " from release " + release.Tag + " in " + target.Repo + "?", NonTTY: "delete " + subject}); err != nil {
				return err
			}
			return batchDone(a, cmd, assets, func(asset services.ReleaseAsset) string { return "delete asset " + asset.Name }, func(asset services.ReleaseAsset) (services.ReleaseAsset, error) {
				return asset, service.DeleteAsset(cmd.Context(), release.ID, asset.ID)
			}, func(asset services.ReleaseAsset) string {
				return fmt.Sprintf("Deleted asset %s (ID %d) from release %s", asset.Name, asset.ID, release.Tag)
			})
		},
	}
	command.Flags().BoolVar(&byName, "by-name", false, "Match assets by name only, also when numeric")
	command.ValidArgsFunction = a.completeReleaseAssets()
	return command
}
