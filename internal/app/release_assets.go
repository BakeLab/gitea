// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"os"
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
	group := &cobra.Command{Use: "asset", Short: "Manage release assets"}
	var limit, page int
	list := &cobra.Command{Use: "list <tag>", Short: "List release assets", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
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
		return writeList(a, cmd, items, assetRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	var name string
	upload := &cobra.Command{Use: "upload <tag> <file>...", Short: "Upload release assets", Args: usageArgs(cobra.MinimumNArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("name") && (len(args) != 2 || strings.TrimSpace(name) == "") {
			return clierr.Wrap(clierr.Usage, errors.New("--name requires exactly one file and a nonempty name"))
		}
		files, err := openRegularFiles(args[1:])
		defer closeFiles(files)
		if err != nil {
			return err
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
		filename := func(file *os.File) string {
			if cmd.Flags().Changed("name") {
				return name
			}
			return filepath.Base(file.Name())
		}
		return batch(a, cmd, files, func(file *os.File) string { return "upload asset " + filename(file) }, func(file *os.File) (services.ReleaseAsset, error) {
			return service.UploadAsset(cmd.Context(), release.ID, filename(file), file)
		}, assetRow)
	}}

	upload.Flags().StringVar(&name, "name", "", "Uploaded asset name (defaults to filename)")
	group.AddCommand(upload)
	for _, verb := range []string{"edit", "delete"} {
		var newName string
		var byName bool
		command := &cobra.Command{Use: verb + " <tag> <asset-id>", Short: titleCase(verb) + " release assets", Args: argsExact(2)}
		if verb == "delete" {
			command.Use = "delete <tag> <asset-id>..."
			command.Args = usageArgs(cobra.MinimumNArgs(2))
			command.Flags().BoolVar(&byName, "by-name", false, "Select assets by exact name instead of ID")
			command.Annotations = textOutput("asset deletion does not return a resource")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			if verb == "edit" && strings.TrimSpace(newName) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("supply --name for asset rename"))
			}
			var ids []int64
			if !byName {
				var err error
				if ids, err = resourceNumbers(args[1:]); err != nil {
					return err
				}
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
			if verb == "edit" {
				item, err := service.RenameAsset(cmd.Context(), release.ID, ids[0], newName)
				if err != nil {
					return err
				}
				return writeItem(a, cmd, item, assetRow)
			}
			if byName {
				assets, err := service.AllAssets(cmd.Context(), release.ID)
				if err != nil {
					return err
				}
				for _, name := range args[1:] {
					var matches []int64
					for _, asset := range assets {
						if asset.Name == name {
							matches = append(matches, asset.ID)
						}
					}
					if len(matches) != 1 {
						return clierr.Wrap(clierr.Usage, fmt.Errorf("asset name %q matches %d assets; use an explicit asset ID", name, len(matches)))
					}
					if slices.Contains(ids, matches[0]) {
						return clierr.Wrap(clierr.Usage, errors.New("duplicate asset selections"))
					}
					ids = append(ids, matches[0])
				}
			}
			if err := a.confirm(cmd, "Delete release assets "+strings.Join(args[1:], ", ")+" on "+target.Repo); err != nil {
				return err
			}
			var failures []error
			for _, id := range ids {
				if err := service.DeleteAsset(cmd.Context(), release.ID, id); err != nil {
					failures = append(failures, fmt.Errorf("delete asset %d: %w", id, err))
					continue
				}
				if len(ids) > 1 {
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Deleted asset %d\n", id); err != nil {
						return err
					}
				}
			}
			return errors.Join(failures...)
		}
		if verb == "edit" {
			command.Flags().StringVar(&newName, "name", "", "New asset name")
		}
		group.AddCommand(command)
	}

	var output string
	download := &cobra.Command{Use: "download <tag> <asset-id>", Short: "Download an asset to a file or stdout", Annotations: textOutput("asset downloads are binary"), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[1])
		if err != nil {
			return err
		}
		if err := validateDownloadDestination(output); err != nil {
			return err
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
		body, err := service.DownloadAsset(cmd.Context(), release.ID, id)
		if err != nil {
			return err
		}
		defer body.Close()
		return writeDownload(cmd, body, output)
	}}
	download.Flags().StringVar(&output, "output-file", "", "Destination file (must not exist), or - for stdout")
	group.AddCommand(download)
	return group
}

func assetRow(item services.ReleaseAsset) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Name, strconv.FormatInt(item.Size, 10), item.URL)
}
