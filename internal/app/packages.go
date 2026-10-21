// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) packageCommand() *cobra.Command {
	var owner string
	group := &cobra.Command{Use: "package", Short: "Manage packages", Long: `Manage the packages in the registry of a user or organization: the owner of the selected
repository, else your account, unless --owner names another.

A package is addressed by its type, such as generic, npm or container, and its name.`}
	group.PersistentFlags().StringVar(&owner, "owner", "", "User or organization whose packages to manage")
	service := func(cmd *cobra.Command) (target, services.Packages, error) {
		// The owner of the clone's repository is the default, but outside a clone, or in one gt
		// cannot act on, it is the account. Only a repository the user named must resolve.
		t, err := a.resolve(cmd.Context(), owner == "")
		if err != nil && owner == "" && a.options.repo == "" && a.deps.Getenv("GITEA_CLI_REPO") == "" && cloneUnusable(t, err) {
			t, err = a.resolve(cmd.Context(), false)
		}
		if err != nil {
			return t, services.Packages{}, err
		}
		return t, services.Packages{Client: t.client, Owner: cmp.Or(owner, t.owner(), t.login())}, nil
	}
	group.AddCommand(a.packageListCommand(service), a.packageVersionsCommand(service), a.packageViewCommand(service),
		a.packageDeleteCommand(service), a.packageLinkCommand(service, true), a.packageLinkCommand(service, false), a.packageUploadCommand(service))
	return jsonOutput(group, jsonFields[services.Package]())
}

// cloneUnusable reports whether resolving failed because the working directory names no repository
// gt can act on: it is no clone, its remotes are ambiguous, or the repository's instance has no
// account. A canceled prompt or a failing credential would fail the same way for the account.
func cloneUnusable(t target, err error) bool {
	if errors.Is(err, errRepositoryRequired) || errors.Is(err, git.ErrAmbiguousRemote) {
		return true
	}
	if errors.Is(err, terminal.ErrCanceled) || !t.repoFromClone() || t.Host == "" {
		return false
	}
	_, exists := t.host.Users[t.User]
	return !exists
}

// packageService resolves the package registry a command acts on.
type packageService func(*cobra.Command) (target, services.Packages, error)

// packageTable lists packages, one row per version.
func packageTable(context string) listSpec[services.Package] {
	return listSpec[services.Package]{
		Columns: []terminal.Column{{Header: "Type"}, {Header: "Name", Flex: true}, {Header: "Version"}, {Header: "Repository"}, {Header: "Created"}},
		Row: func(p services.Package) []terminal.Cell {
			return []terminal.Cell{{Text: p.Type}, {Text: p.Name, Bold: true, Link: p.URL}, {Text: p.Version}, {Text: p.Repository, Muted: true}, {Time: terminal.ParseTime(p.CreatedAt)}}
		},
		Empty: "no " + context, Context: context,
	}
}

func (a *application) packageListCommand(service packageService) *cobra.Command {
	var opts services.PackageListOptions
	cmd := &cobra.Command{
		Use: "list", Short: "List packages", Args: argsExact(0),
		Example: `$ gt package list
$ gt package list --owner acme --type container
$ gt package list --search tool --json name,version`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, s, err := service(cmd)
			if err != nil {
				return err
			}
			page, err := s.List(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, page.Items, packageTable("packages of "+s.Owner), servicePage(page, opts.Page))
		},
	}
	pageFlags(cmd, &opts.Limit, &opts.Page)
	cmd.Flags().StringVarP(&opts.Type, "type", "t", "", "Only packages of this `type`, such as generic or npm")
	cmd.Flags().StringVarP(&opts.Query, "search", "S", "", "Only packages whose name contains this `text`")
	return cmd
}

func (a *application) packageVersionsCommand(service packageService) *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use: "versions <type> <name>", Short: "List the versions of a package", Args: argsExact(2),
		Example: "$ gt package versions generic tool",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := service(cmd)
			if err != nil {
				return err
			}
			versions, err := s.Versions(cmd.Context(), args[0], args[1], limit, page)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, versions.Items, packageTable("versions of "+args[0]+" package "+args[1]), servicePage(versions, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return cmd
}

// packageDetails is a package version with its files, as package view shows it.
type packageDetails struct {
	services.Package
	Files []services.PackageFile `json:"files"`
}

func (a *application) packageViewCommand(service packageService) *cobra.Command {
	cmd := &cobra.Command{
		Use: "view <type> <name> [<version>]", Short: "View a package version and its files", Args: usageArgs(cobra.RangeArgs(2, 3)),
		Long: "Show a version of a package with its files; without a version, its latest version.",
		Example: `$ gt package view generic tool
$ gt package view npm @acme/ui 2.1.0 --json files`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := service(cmd)
			if err != nil {
				return err
			}
			version := ""
			if len(args) == 3 {
				version = args[2]
			}
			p, err := s.View(cmd.Context(), args[0], args[1], version)
			if err != nil {
				return err
			}
			files, err := s.Files(cmd.Context(), p.Type, p.Name, p.Version)
			if err != nil {
				return err
			}
			d := a.display()
			return writeView(a, cmd, packageDetails{p, files}, func(details packageDetails) terminal.View {
				fields := []terminal.Field{{Label: "Version", Value: p.Version, PipedOnly: true}, {Label: "Type", Value: p.Type}, {Label: "Repository", Value: p.Repository}, {Label: "Creator", Value: p.Creator}, timeField("Created", p.CreatedAt, d)}
				for _, file := range details.Files {
					fields = append(fields, terminal.Field{Label: "File", Value: file.Name + " (" + terminal.Bytes(file.Size) + ")"})
				}
				return terminal.View{Title: p.Name, ID: p.Version, Fields: fields, Bodyless: true, Kind: "package", URL: p.URL}
			})
		},
	}
	return jsonOutput(cmd, jsonFields[packageDetails]())
}

func (a *application) packageDeleteCommand(service packageService) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use: "delete <type> <name> [<version>]", Short: "Delete a package version, or with --all the whole package", Args: usageArgs(cobra.RangeArgs(2, 3)),
		Example: `$ gt package delete generic tool 1.0.0
$ gt package delete generic tool --all --yes`,
		Annotations: map[string]string{"gt.text": "package deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			version := ""
			if len(args) == 3 {
				version = args[2]
			}
			if (version == "") != all {
				return clierr.Wrap(clierr.Usage, errors.New("name the version to delete, or pass --all to delete every version"))
			}
			_, s, err := service(cmd)
			if err != nil {
				return err
			}
			subject := fmt.Sprintf("%s package %s@%s of %s", args[0], args[1], version, s.Owner)
			if all {
				subject = fmt.Sprintf("every version of %s package %s of %s", args[0], args[1], s.Owner)
			}
			return a.deleteConfirmed(cmd, subject, func() error { return s.Delete(cmd.Context(), args[0], args[1], version) })
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Delete every version of the package")
	return cmd
}

func (a *application) packageLinkCommand(service packageService, link bool) *cobra.Command {
	cmd := &cobra.Command{
		Use: "link <type> <name> <repository>", Short: "Link a package to a repository of its owner", Args: argsExact(3),
		Example:     "$ gt package link generic tool tool-source",
		Annotations: textOutput("linking a package does not return a resource"),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, s, err := service(cmd)
			if err != nil {
				return err
			}
			repository := ""
			if link {
				repository = args[2]
				// A package links only to a repository of its owner, which OWNER/REPO may repeat.
				if repoOwner, name, found := strings.Cut(repository, "/"); found {
					if !strings.EqualFold(repoOwner, s.Owner) || name == "" || strings.Contains(name, "/") {
						return clierr.Wrap(clierr.Usage, fmt.Errorf("a package of %s links only to a repository of %s; got %q", s.Owner, s.Owner, repository))
					}
					repository = name
				}
			}
			if err := s.Link(cmd.Context(), args[0], args[1], repository); err != nil {
				return err
			}
			if link {
				a.done(cmd, "Linked %s package %s to %s/%s", args[0], args[1], s.Owner, repository)
			} else {
				a.done(cmd, "Unlinked %s package %s from its repository", args[0], args[1])
			}
			return nil
		},
	}
	if !link {
		cmd.Use, cmd.Short, cmd.Args, cmd.Example = "unlink <type> <name>", "Unlink a package from its repository", argsExact(2), "$ gt package unlink generic tool"
		cmd.Annotations = textOutput("unlinking a package does not return a resource")
	}
	return cmd
}

func (a *application) packageUploadCommand(service packageService) *cobra.Command {
	return &cobra.Command{
		Use: "upload <name> <version> <file>...", Short: "Upload files to a generic package", Args: usageArgs(cobra.MinimumNArgs(3)),
		Long: `Upload files to a version of a generic package, creating the package and the version when
they are new. Each file is stored under its base name; a version cannot hold two files of the same
name.`,
		Example:           "$ gt package upload tool 1.0.0 dist/tool-linux-amd64 dist/tool-darwin-arm64",
		Annotations:       textOutput("uploads do not return a resource"),
		ValidArgsFunction: pathArgs(2, true, completeFiles, cobra.NoFileCompletions),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, version, paths := args[0], args[1], args[2:]
			// Gitea refuses a file whose name differs only in case from another of the version.
			seen := map[string]string{}
			for _, path := range paths {
				key := strings.ToLower(filepath.Base(path))
				if previous, ok := seen[key]; ok {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("%s and %s would both be uploaded as %s, ignoring case; rename one of them", previous, path, filepath.Base(path)))
				}
				seen[key] = path
			}
			files, err := openRegularFiles(paths)
			defer closeFiles(files)
			if err != nil {
				return err
			}
			_, s, err := service(cmd)
			if err != nil {
				return err
			}
			names := make([]string, len(files))
			for i, file := range files {
				names[i] = filepath.Base(file.Name())
			}
			// A version keeps its files: refuse before uploading any rather than stop halfway.
			if err := s.CheckGenericUpload(cmd.Context(), name, version, names); err != nil {
				return err
			}
			for i, file := range files {
				if err := s.UploadGeneric(cmd.Context(), name, version, names[i], file); err != nil {
					if i > 0 {
						return fmt.Errorf("uploaded %s to generic package %s@%s, but not %s: %w", strings.Join(names[:i], ", "), name, version, names[i], err)
					}
					return err
				}
				a.done(cmd, "Uploaded %s to generic package %s@%s of %s", names[i], name, version, s.Owner)
			}
			return nil
		},
	}
}
