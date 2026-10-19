// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) deployKeyCommand() *cobra.Command {
	group := &cobra.Command{Use: "deploy-key", Short: "Manage deploy keys", Long: `Manage the deploy keys of a repository: SSH keys that grant access to that repository only,
such as for a build server. A deploy key can read the repository, and push to it when added
with --allow-write.`}
	group.AddCommand(a.deployKeyListCommand(), a.deployKeyAddCommand(), a.deployKeyDeleteCommand())
	return jsonOutput(group, jsonFields[services.DeployKey]())
}

// deployKeyLabel names a key in messages: `deploy key "ci" (SHA256:…)`.
func deployKeyLabel(key services.DeployKey) string {
	return fmt.Sprintf("deploy key %q (%s)", key.Title, key.Fingerprint)
}

// completeDeployKeys completes the deploy key IDs of the repository with their titles.
func (a *application) completeDeployKeys() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		keys, err := (services.DeployKeys{Client: t.client, Repository: t.Repo}).List(ctx, completionLimit, 1)
		return describe(keys.Items, func(key services.DeployKey) (string, string) {
			return strconv.FormatInt(key.ID, 10), key.Title + " (" + key.Fingerprint + ")"
		}), err
	})
}

func (a *application) deployKeyListCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use: "list", Short: "List deploy keys", Args: argsExact(0),
		Example: `$ gt repo deploy-key list
$ gt repo deploy-key list -R acme/site --json title,read_only`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			keys, err := (services.DeployKeys{Client: t.client, Repository: t.Repo}).List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, keys.Items, listSpec[services.DeployKey]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Title", Flex: true}, {Header: "Access"}, {Header: "Fingerprint"}, {Header: "Added"}},
				Row: func(key services.DeployKey) []terminal.Cell {
					access := "read-write"
					if key.ReadOnly {
						access = "read-only"
					}
					return []terminal.Cell{{Text: strconv.FormatInt(key.ID, 10), Link: repositoryURL(t) + "/settings/keys"}, {Text: key.Title, Bold: true}, {Text: access}, {Text: key.Fingerprint, Muted: true}, {Time: terminal.ParseTime(key.CreatedAt)}}
				},
				Context: "deploy keys of " + t.Repo,
				Empty:   "no deploy keys in " + t.Repo + "; add one with: gt repo deploy-key add key.pub --title NAME",
			}, servicePage(keys, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return cmd
}

func (a *application) deployKeyAddCommand() *cobra.Command {
	var title string
	var allowWrite bool
	cmd := &cobra.Command{
		Use: "add <file>", Short: "Add a deploy key from a public key file or stdin", Args: argsExact(1), ValidArgsFunction: completeFiles,
		Long: `Add an SSH public key as a deploy key of the repository; - reads it from stdin. The key can
only read the repository unless --allow-write lets it push. The title defaults to the key's
comment.`,
		Example: `$ gt repo deploy-key add ci.pub --title "Build server"
$ gt repo deploy-key add deploy.pub --title "Release bot" --allow-write`,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readFileOrStdin(a.deps.Streams.In, args[0])
			if err != nil {
				return fmt.Errorf("read public key: %w", err)
			}
			_, comment, err := publicKey(data, args[0])
			if err != nil {
				return err
			}
			if title, err = keyTitle(cmd, title, comment, ""); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			created, err := (services.DeployKeys{Client: t.client, Repository: t.Repo}).Create(cmd.Context(), title, strings.TrimSpace(string(data)), !allowWrite)
			if err != nil {
				return titleTaken(err, "deploy key", title)
			}
			a.done(cmd, "Added %s to %s", deployKeyLabel(created), t.Repo)
			return a.jsonResult(cmd, created)
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Key title (defaults to the key comment)")
	cmd.Flags().BoolVarP(&allowWrite, "allow-write", "w", false, "Let the key push to the repository")
	return cmd
}

func (a *application) deployKeyDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete a deploy key", Args: argsExact(1), ValidArgsFunction: a.completeDeployKeys(),
		Example: `$ gt repo deploy-key delete 4
$ gt repo deploy-key delete 4 --yes`,
		Annotations: map[string]string{"gt.text": "deploy key deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.DeployKeys{Client: t.client, Repository: t.Repo}
			key, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.deleteConfirmed(cmd, deployKeyLabel(key)+" of "+t.Repo, func() error { return service.Delete(cmd.Context(), id) })
		},
	}
}
