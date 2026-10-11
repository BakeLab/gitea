// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

func (a *application) sshKeyCommand() *cobra.Command {
	group := &cobra.Command{Use: "ssh-key", Short: "Manage account SSH public keys"}
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List account SSH keys", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, err := (services.SSHKeys{Client: target.client}).List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, sshKeyRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	var title string
	create := &cobra.Command{Use: "create <public-key-file>", Short: "Register an SSH public key; - reads stdin", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		data, err := readFileOrStdin(a.deps.Streams.In, args[0])
		if err != nil {
			return fmt.Errorf("read public key: %w", err)
		}
		_, _, _, rest, err := ssh.ParseAuthorizedKey(data)
		if err != nil || len(strings.TrimSpace(string(rest))) != 0 {
			return clierr.Wrap(clierr.Usage, errors.New("supply exactly one valid SSH public key"))
		}
		if title == "" {
			if args[0] == "-" {
				return clierr.Wrap(clierr.Usage, errors.New("--title is required when reading a key from stdin"))
			}
			base := filepath.Base(args[0])
			title = strings.TrimSuffix(base, filepath.Ext(base))
		}
		if strings.TrimSpace(title) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("key title cannot be empty"))
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		item, err := (services.SSHKeys{Client: target.client}).Create(cmd.Context(), title, strings.TrimSpace(string(data)))
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, sshKeyRow)
	}}
	create.Flags().StringVar(&title, "title", "", "Key title (defaults to filename)")
	group.AddCommand(create)
	for _, verb := range []string{"view", "delete"} {
		command := &cobra.Command{Use: verb + " <id>", Short: titleCase(verb) + " an SSH key", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.SSHKeys{Client: target.client}
			if verb == "delete" {
				if err := a.confirm(cmd, "Delete SSH key "+args[0]); err != nil {
					return err
				}
				return service.Delete(cmd.Context(), id)
			}
			item, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, sshKeyRow)
		}}
		if verb == "delete" {
			command.Annotations = textOutput("SSH key deletion does not return a resource")
		}
		group.AddCommand(command)
	}
	return group
}

func sshKeyRow(key services.SSHKey) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(key.ID, 10), key.Title, key.Fingerprint)
}
