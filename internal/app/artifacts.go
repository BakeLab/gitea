// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"archive/zip"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
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

func (a *application) artifactCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "artifact",
		Short: "List the artifacts of runs",
		Long:  "List the artifacts that a run uploaded. To download them, use gt run download.",
	}
	var limit, page int
	list := &cobra.Command{
		Use:   "list [<run-id>]",
		Short: "List the artifacts of a run",
		Example: `# list the artifacts of a run
$ gt run artifact list 34`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			t, run, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			items, err := (services.Actions{Client: t.client, Repository: t.Repo}).Artifacts(cmd.Context(), run, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.Artifact]{Columns: artifactColumns, Row: artifactRow, Empty: fmt.Sprintf("no artifacts in run %d", run), Context: fmt.Sprintf("artifacts of run %d", run)}
			return writeTable(a, cmd, items.Items, spec, servicePage(items, page))
		},
	}
	pageFlags(list, &limit, &page)
	list.ValidArgsFunction = firstArg(a.completeRuns())
	group.AddCommand(list)

	return group
}

// artifactColumns head artifactRow: sizes and expiry for people on a terminal, bytes and times
// for scripts.
var artifactColumns = []terminal.Column{
	{Header: "ID"},
	{Header: "Name", Flex: true},
	{Header: "Size", TTYOnly: true},
	{Header: "Size", PipedOnly: true},
	{Header: "Expires", TTYOnly: true},
	{Header: "Expires", PipedOnly: true},
	{Header: "Expired", PipedOnly: true},
}

func artifactRow(item services.Artifact) []terminal.Cell {
	expires := terminal.Cell{}
	if item.ExpiresAt != nil {
		expires.Time = terminal.ParseTime(*item.ExpiresAt)
	}
	piped := terminal.Cell{Time: expires.Time}
	if item.Expired {
		expires = terminal.Cell{Text: "expired", Color: "red"}
	}
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.ID, 10)},
		{Text: item.Name},
		{Text: terminal.Bytes(item.Size)},
		{Text: strconv.FormatInt(item.Size, 10)},
		expires,
		piped,
		{Text: strconv.FormatBool(item.Expired)},
	}
}

func (a *application) runDownloadCommand() *cobra.Command {
	var names []string
	var asZip bool
	var destination downloadFlags
	cmd := &cobra.Command{
		Use:   "download [<run-id>]",
		Short: "Download the artifacts of a run",
		Long: `Download the artifacts of a run and extract them.

Each artifact is extracted into a directory named after it, except a single artifact selected
with --name, which is extracted into the destination directory itself. With --zip, the
artifacts are saved as ZIP archives instead.`,
		Example: `# download every artifact of a run into ./<artifact name>/
$ gt run download 34
# extract one artifact into dist/
$ gt run download 34 --name build --dir dist
# save the artifacts as ZIP archives
$ gt run download 34 --zip`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments, "gt.text": "run download writes files"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if destination.output != "" && (!asZip || len(names) != 1) {
				return clierr.Wrap(clierr.Usage, errors.New("--output saves one archive; combine it with --zip and a single --name, or use --dir"))
			}
			t, run, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			artifacts, err := service.AllArtifacts(cmd.Context(), run)
			if err != nil {
				return err
			}
			selected, err := selectArtifacts(latestArtifacts(artifacts), names, run)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(selected, func(artifact services.Artifact) bool { return !artifact.Expired }) {
				return fmt.Errorf("the artifacts of run %d have expired", run)
			}
			for _, artifact := range selected {
				if artifact.Expired {
					a.warn(cmd, "Artifact %s has expired; skipped", artifact.Name)
					continue
				}
				path, err := a.downloadArtifact(cmd, service, artifact, destination, asZip, len(names) != 1)
				if err != nil {
					return fmt.Errorf("artifact %s: %w", terminal.SafeLine(artifact.Name), err)
				}
				if path != "" {
					a.done(cmd, "Downloaded artifact %s to %s", artifact.Name, path)
				}
			}
			return nil
		},
	}
	destination.register(cmd)
	cmd.Flags().StringSliceVarP(&names, "name", "n", nil, "Only download the artifacts with these names")
	cmd.Flags().BoolVar(&asZip, "zip", false, "Save each artifact as a ZIP archive instead of extracting it")
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	_ = cmd.RegisterFlagCompletionFunc("name", commaSeparated(a.completeArtifactNames()))
	return cmd
}

// latestArtifacts keeps the newest artifact of each name: after a rerun, the run lists the artifacts
// of every attempt, which may share names.
func latestArtifacts(artifacts []services.Artifact) []services.Artifact {
	index := map[string]int{}
	latest := make([]services.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if i, seen := index[artifact.Name]; seen {
			if artifact.ID > latest[i].ID {
				latest[i] = artifact
			}
			continue
		}
		index[artifact.Name] = len(latest)
		latest = append(latest, artifact)
	}
	return latest
}

// selectArtifacts returns the artifacts of a run with the given names, or all of them.
func selectArtifacts(artifacts []services.Artifact, names []string, run int64) ([]services.Artifact, error) {
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("run %d has no artifacts", run)
	}
	if len(names) == 0 {
		return artifacts, nil
	}
	selected := make([]services.Artifact, 0, len(names))
	for _, name := range names {
		index := slices.IndexFunc(artifacts, func(artifact services.Artifact) bool { return artifact.Name == name })
		if index < 0 {
			available := make([]string, len(artifacts))
			for i, artifact := range artifacts {
				available[i] = terminal.SafeLine(artifact.Name)
			}
			return nil, fmt.Errorf("artifact %q not found in run %d (artifacts: %s)", name, run, strings.Join(available, ", "))
		}
		selected = append(selected, artifacts[index])
	}
	return selected, nil
}

// downloadArtifact saves an artifact as a ZIP archive, or extracts it into the destination
// directory, or into a directory named after it inside, with isolate. It returns where the
// artifact went, or "" when --skip-existing kept an existing archive.
func (a *application) downloadArtifact(cmd *cobra.Command, service services.Actions, artifact services.Artifact, f downloadFlags, asZip, isolate bool) (string, error) {
	if asZip {
		path, skip, err := f.destination(artifact.Name + ".zip")
		if err != nil || skip {
			return "", err
		}
		body, err := service.DownloadArtifact(cmd.Context(), artifact.ID)
		if err != nil {
			return "", err
		}
		defer body.Close()
		return path, a.download(cmd, f, path, body)
	}
	if f.clobber && f.skipExisting {
		return "", clierr.Wrap(clierr.Usage, errors.New("--clobber and --skip-existing cannot be combined"))
	}
	dir := cmp.Or(f.dir, ".")
	if isolate {
		if !filepath.IsLocal(artifact.Name) || strings.ContainsAny(artifact.Name, `/\`) {
			return "", fmt.Errorf("cannot use %q as a directory name", artifact.Name)
		}
		dir = filepath.Join(dir, artifact.Name)
	}
	body, err := service.DownloadArtifact(cmd.Context(), artifact.ID)
	if err != nil {
		return "", err
	}
	defer body.Close()
	return dir, extractZip(cmd.Context(), body, dir, f.clobber, f.skipExisting)
}

// extractZip extracts the ZIP archive in body into dir. Entries outside dir, links and other
// special files are refused, and an existing file stops the extraction before anything is
// written, unless clobber replaces or skipExisting keeps it.
func extractZip(ctx context.Context, body io.Reader, dir string, clobber, skipExisting bool) error {
	archive, err := os.CreateTemp("", "gt-artifact-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	size, err := io.Copy(archive, body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	reader, err := zip.NewReader(archive, size)
	if err != nil {
		return fmt.Errorf("invalid ZIP archive: %w", err)
	}
	for _, entry := range reader.File {
		name := filepath.FromSlash(entry.Name)
		mode := entry.Mode()
		switch {
		case !filepath.IsLocal(name):
			return fmt.Errorf("refusing to extract %q outside %s", entry.Name, dir)
		case !mode.IsRegular() && !mode.IsDir():
			return fmt.Errorf("refusing to extract %q: not a regular file", entry.Name)
		}
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil && !mode.IsDir() && !clobber && !skipExisting {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%s already exists; pass --clobber to replace it or --skip-existing to keep it", filepath.Join(dir, name)))
		}
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	// The root keeps every write inside dir, also through symbolic links already in it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, entry := range reader.File {
		name := filepath.FromSlash(entry.Name)
		if entry.Mode().IsDir() {
			if err := root.MkdirAll(name, 0o777); err != nil {
				return err
			}
			continue
		}
		if err := extractFile(root, entry, name, clobber, skipExisting); err != nil {
			return err
		}
	}
	return nil
}

// extractFile writes one archive entry to name in root with its permission bits, such as an
// executable's, as far as the umask allows; special bits such as setuid are dropped.
func extractFile(root *os.Root, entry *zip.File, name string, clobber, skipExisting bool) error {
	if err := root.MkdirAll(filepath.Dir(name), 0o777); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if clobber {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	file, err := root.OpenFile(name, flags, cmp.Or(entry.Mode().Perm(), 0o666))
	if errors.Is(err, os.ErrExist) && skipExisting {
		return nil
	}
	if err != nil {
		return err
	}
	content, err := entry.Open()
	if err != nil {
		_ = file.Close()
		return err
	}
	_, copyErr := io.Copy(file, content)
	return cmp.Or(copyErr, content.Close(), file.Close())
}

// completeArtifactNames completes the names of the artifacts of the run given as the first argument.
func (a *application) completeArtifactNames() cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		run, err := parseRef(args[0], runRef)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return a.apiCompletionIn(run.Repo, true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			artifacts, err := services.Actions{Client: t.client, Repository: t.Repo}.AllArtifacts(ctx, run.Number)
			return describe(artifacts, func(artifact services.Artifact) (string, string) { return artifact.Name, terminal.Bytes(artifact.Size) }), err
		})(cmd, args, toComplete)
	}
}
