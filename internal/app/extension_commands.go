// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func (a *application) extensionCommand() *cobra.Command {
	group := &cobra.Command{Use: "extension", Aliases: []string{"ext"}, Short: "Manage gt extensions", Long: `Install, run and remove extensions: executables named gt-NAME that run as gt NAME.

Extensions come from repositories on Gitea instances, or as GitHub CLI extensions named gh-NAME
from github.com; gt runs the gh extensions that do not depend on GitHub's own APIs. Built-in
commands always win over an extension of the same name; gt extension exec runs it anyway.

gt passes the global flags --host, --repo and --user before the extension's name on as
GITEA_CLI_HOST, GITEA_CLI_REPO and GITEA_CLI_USER, runs it in --cwd, accepts --no-input and
--yes, refuses other global flags, and passes everything after its name on unchanged.
Extensions do not get your Gitea token unless you allowed it at install. See gt help
extensions for the security model.`}
	group.AddCommand(a.extensionInstallCommand(), a.extensionListCommand(), a.extensionRemoveCommand(), a.extensionUpgradeCommand(), a.extensionExecCommand(), a.extensionCreateCommand(), a.extensionSearchCommand(), a.extensionBrowseCommand())
	return group
}

// extensionItem is an installed extension as gt extension list shows it.
type extensionItem struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	Host         string `json:"host"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	Commit       string `json:"commit"`
	SHA256       string `json:"sha256"`
	DigestSource string `json:"digest_source"`
	Pinned       bool   `json:"pinned"`
	Credentials  bool   `json:"credentials"`
	CompatGH     bool   `json:"compat_gh"`
	Path         string `json:"path"`
	Description  string `json:"description"`
	// Shadowed reports a built-in command of the same name, which wins over the extension.
	Shadowed bool `json:"shadowed"`
}

func extensionItemFrom(extension extensions.Extension, root *cobra.Command) extensionItem {
	m := extension.Manifest
	return extensionItem{
		Name: m.Name, Kind: m.Kind, Source: m.Source, Host: m.Host, Repository: m.Repository(), Tag: m.Tag, Commit: m.Commit,
		SHA256: m.SHA256, DigestSource: m.DigestSource, Pinned: m.Pinned, Credentials: m.Credentials, CompatGH: m.CompatGH,
		Path: extension.Path(), Description: m.Metadata.Description, Shadowed: isCommand(root, m.Name),
	}
}

func (a *application) extensionListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "list", Short: "List installed extensions", Args: argsExact(0),
		Annotations: map[string]string{"gt.offline": "true"},
		Example: `$ gt extension list
$ gt extension list --json name,tag,credentials`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			installed, listErr := a.extensionStore().List()
			items := make([]extensionItem, len(installed))
			for i, extension := range installed {
				items[i] = extensionItemFrom(extension, cmd.Root())
			}
			err := writeTable(a, cmd, items, listSpec[extensionItem]{
				Columns: []terminal.Column{{Header: "Name"}, {Header: "Source", Flex: true}, {Header: "Version"}, {Header: "Notes"}},
				Row: func(item extensionItem) []terminal.Cell {
					version := cmp.Or(item.Tag, shortCommit(item.Commit), item.Kind)
					var notes []string
					for note, set := range map[string]bool{"credentials": item.Credentials, "pinned": item.Pinned, "gh shim": item.CompatGH, "shadowed by gt " + item.Name: item.Shadowed} {
						if set {
							notes = append(notes, note)
						}
					}
					slices.Sort(notes)
					source := item.Host + "/" + item.Repository
					if item.Kind == extensions.KindLocal {
						source = item.Path
					}
					return []terminal.Cell{{Text: item.Name, Bold: true}, {Text: terminal.SafeLine(source)}, {Text: terminal.SafeLine(version), Muted: true}, {Text: strings.Join(notes, ", ")}}
				},
				Empty: "no extensions installed; find some with: gt extension search",
			}, listPage{Number: 1})
			return cmp.Or(err, listErr)
		},
	}
	return jsonOutput(cmd, jsonFields[extensionItem]())
}

// completeExtensions completes the names of the installed extensions with their sources.
func (a *application) completeExtensions(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	installed, _ := a.extensionStore().List()
	return describe(installed, func(extension extensions.Extension) (string, string) {
		return extension.Name, cmp.Or(extension.Metadata.Description, extensionOrigin(extension.Manifest))
	}), cobra.ShellCompDirectiveNoFileComp
}

func (a *application) extensionRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use: "remove <name>...", Aliases: []string{"rm"}, Short: "Remove installed extensions", Args: nonBlankArgs(cobra.MinimumNArgs(1), "extension name"), ValidArgsFunction: a.completeExtensions,
		Annotations: map[string]string{"gt.offline": "true", "gt.confirm": "true", "gt.text": "extension remove does not return a resource"},
		Example:     "$ gt extension remove hello\n$ gt extension remove hello world --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			store := a.extensionStore()
			for _, name := range args {
				if _, err := store.Lookup(name); errors.Is(err, extensions.ErrNotInstalled) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("no extension named %q; gt extension list shows them", name))
				}
			}
			for _, name := range args {
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Remove extension " + name + "?"}); err != nil {
					return err
				}
				if err := store.Remove(name); err != nil {
					return err
				}
				a.done(cmd, "Removed extension %s", name)
			}
			return nil
		},
	}
}

func (a *application) extensionExecCommand() *cobra.Command {
	return &cobra.Command{
		Use: "exec <name> [<args>...]", Short: "Run an extension, even when a built-in command has its name", DisableFlagParsing: true, ValidArgsFunction: a.completeExtensions,
		Annotations: map[string]string{"gt.offline": "true", "gt.text": "extensions print their own output"},
		Example: `$ gt extension exec hello --greeting hi
# Global flags go before exec
$ gt --repo alice/project extension exec hello`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Flag parsing is off, so the global flags before the extension's name arrive here.
			at := commandIndex(cmd.Root(), args)
			if at == len(args) {
				if slices.Contains(args, "-h") || slices.Contains(args, "--help") {
					return cmd.Help()
				}
				return clierr.Wrap(clierr.Usage, errors.New("extension exec requires an extension name"))
			}
			extension, err := a.extensionStore().Lookup(args[at])
			if errors.Is(err, extensions.ErrNotInstalled) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("no extension named %q; gt extension list shows them", args[at]))
			}
			if err != nil {
				return err
			}
			if isCommand(cmd.Root(), extension.Name) {
				a.warn(cmd, "gt %s runs the built-in command; this runs the extension of that name", extension.Name)
			}
			return a.runExtension(cmd.Context(), cmd.Root(), extension, args[:at], args[at+1:])
		},
	}
}

func (a *application) extensionBrowseCommand() *cobra.Command {
	var index string
	cmd := &cobra.Command{
		Use: "browse [<name>]", Short: "Open an extension's repository, or the extension index, in the browser", Args: usageArgs(cobra.MaximumNArgs(1)), ValidArgsFunction: a.completeExtensions,
		Annotations: textOutput("extension browse opens a page"),
		Example: `$ gt extension browse
$ gt extension browse hello`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.openBrowser(cmd, index)
			}
			extension, err := a.extensionStore().Lookup(args[0])
			if errors.Is(err, extensions.ErrNotInstalled) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("no extension named %q; gt extension list shows them", args[0]))
			}
			if err != nil {
				return err
			}
			if extension.Kind == extensions.KindLocal {
				return fmt.Errorf("extension %s runs from %s and has no repository", extension.Name, extensionOrigin(extension.Manifest))
			}
			return a.openBrowser(cmd, extensionOrigin(extension.Manifest))
		},
	}
	cmd.Flags().StringVar(&index, "index", defaultExtensionIndex, "Extension index `repository` to open")
	return cmd
}

// extensionIndex is extensions.yml, the list of extensions an index repository offers.
type extensionIndex struct {
	Extensions []extensionListing `yaml:"extensions"`
}

// extensionListing is an extension of an index.
type extensionListing struct {
	Name        string   `yaml:"name" json:"name"`
	Repository  string   `yaml:"repository" json:"repository"`
	Description string   `yaml:"description" json:"description"`
	Platforms   []string `yaml:"platforms" json:"platforms"`
	Installed   bool     `yaml:"-" json:"installed"`
}

func (a *application) extensionSearchCommand() *cobra.Command {
	var index string
	cmd := &cobra.Command{
		Use: "search [<query>...]", Short: "Search an index of extensions",
		Long: `Search the extensions an index repository lists in its extensions.yml, by name and
description. The default index lists the extensions the Gitea project supports; --index
reads another repository, such as one of your organization:

  extensions:
    - name: hello
      repository: https://gitea.com/alice/gt-hello
      description: Say hello`,
		Example: `$ gt extension search
$ gt extension search review
$ gt extension search --index https://git.example.com/platform/gt-extensions`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			source, err := a.extensionSourceFor(ctx, index)
			if err != nil {
				return err
			}
			repo, err := source.repository(ctx)
			if services.IsNotFound(err) {
				return fmt.Errorf("extension index %s not found; select another one with --index", index)
			}
			if err != nil {
				return err
			}
			data, exists, err := source.file(ctx, repo.defaultBranch, "extensions.yml")
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%s has no extensions.yml", index)
			}
			var listed extensionIndex
			if err := yaml.Unmarshal(data, &listed); err != nil {
				return fmt.Errorf("parse extensions.yml of %s: %w", index, err)
			}
			installed := a.extensionStore().Names()
			query := strings.ToLower(strings.Join(args, " "))
			matches := []extensionListing{}
			for _, listing := range listed.Extensions {
				if query == "" || strings.Contains(strings.ToLower(listing.Name+" "+listing.Description), query) {
					listing.Installed = installed[listing.Name]
					if listing.Platforms == nil {
						listing.Platforms = []string{}
					}
					matches = append(matches, listing)
				}
			}
			return writeTable(a, cmd, matches, listSpec[extensionListing]{
				Columns: []terminal.Column{{Header: "Name"}, {Header: "Repository"}, {Header: "Description", Flex: true}, {Header: "Installed"}},
				Row: func(listing extensionListing) []terminal.Cell {
					installed := ""
					if listing.Installed {
						installed = "installed"
					}
					return []terminal.Cell{{Text: terminal.SafeLine(listing.Name), Bold: true}, {Text: terminal.SafeLine(listing.Repository), Link: listing.Repository}, {Text: terminal.SafeLine(listing.Description)}, {Text: installed, Muted: true}}
				},
				Context: "extensions in " + displayURL(index),
				Empty:   "no extensions in " + displayURL(index) + " match",
			}, listPage{Number: 1})
		},
	}
	cmd.Flags().StringVar(&index, "index", defaultExtensionIndex, "Index `repository` to search")
	return jsonOutput(cmd, jsonFields[extensionListing]())
}
