// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

func (a *application) sshKeyCommand() *cobra.Command {
	group := &cobra.Command{Use: "ssh-key", Short: "Manage SSH keys", Long: "Manage the SSH public keys of your account, which authenticate Git operations over SSH."}
	group.AddCommand(a.sshKeyListCommand(), a.sshKeyViewCommand(), a.sshKeyCreateCommand(), a.sshKeyDeleteCommand())
	return jsonOutput(group, jsonFields[services.SSHKey]())
}

// sshKeyLabel names a key in messages: `SSH key "laptop" (SHA256:…)`.
func sshKeyLabel(key services.SSHKey) string {
	return fmt.Sprintf("SSH key %q (%s)", key.Title, key.Fingerprint)
}

// completeSSHKeys completes the key IDs of the account with their titles.
func (a *application) completeSSHKeys() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		keys, err := (services.SSHKeys{Client: t.client}).List(ctx, completionLimit, 1)
		return describe(keys.Items, func(key services.SSHKey) (string, string) {
			return strconv.FormatInt(key.ID, 10), key.Title + " (" + key.Fingerprint + ")"
		}), err
	})
}

func (a *application) sshKeyListCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use: "list", Short: "List SSH keys", Args: argsExact(0),
		Example: `$ gt ssh-key list
$ gt ssh-key list --json title,fingerprint`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			keys, err := (services.SSHKeys{Client: target.client}).List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			account := target.login()
			return writeTable(a, cmd, keys.Items, listSpec[services.SSHKey]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Title", Flex: true}, {Header: "Fingerprint"}, {Header: "Added"}},
				Row: func(key services.SSHKey) []terminal.Cell {
					return []terminal.Cell{{Text: strconv.FormatInt(key.ID, 10), Link: webURL(target, "user", "settings", "keys")}, {Text: key.Title, Bold: true}, {Text: key.Fingerprint, Muted: true}, {Time: terminal.ParseTime(key.CreatedAt)}}
				},
				Context: "SSH keys of " + account,
				Empty:   "no SSH keys for " + account + "; add one with: gt ssh-key add ~/.ssh/id_ed25519.pub",
			}, servicePage(keys, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return cmd
}

func (a *application) sshKeyViewCommand() *cobra.Command {
	return &cobra.Command{
		Use: "view <id>", Short: "View an SSH key", Args: argsExact(1), ValidArgsFunction: a.completeSSHKeys(),
		Example: `$ gt ssh-key view 2
# print the public key
$ gt ssh-key view 2 --jq .key`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			key, err := (services.SSHKeys{Client: target.client}).View(cmd.Context(), id)
			if err != nil {
				return err
			}
			d := a.display()
			return writeView(a, cmd, key, func(key services.SSHKey) terminal.View {
				added := timeField("Added", key.CreatedAt, d)
				meta := ""
				if added.Value != "" {
					meta = "added " + added.Value
				}
				added.PipedOnly = true
				algorithm, _, _ := strings.Cut(key.Key, " ")
				return terminal.View{
					Title: key.Title, ID: fmt.Sprintf("(ID %d)", key.ID), Meta: meta,
					Fields:   []terminal.Field{{Label: "Fingerprint", Value: key.Fingerprint}, {Label: "Type", Value: algorithm}, added, {Label: "Key", Value: key.Key}},
					Bodyless: true, Kind: "SSH key", URL: webURL(target, "user", "settings", "keys"),
				}
			})
		},
	}
}

// publicKey reads one SSH public key from data, which came from source, and returns it with its
// comment. It recognizes a private key and points to its public half.
func publicKey(data []byte, source string) (ssh.PublicKey, string, error) {
	if bytes.Contains(data, []byte("PRIVATE KEY-----")) {
		hint := "pipe the public key (.pub) instead"
		if source != "-" {
			hint = "add its public key instead: gt ssh-key add " + source + ".pub"
		}
		return nil, "", clierr.Wrap(clierr.Usage, fmt.Errorf("%s holds a private key; %s", sshKeySource(source), hint))
	}
	key, comment, _, rest, err := ssh.ParseAuthorizedKey(data)
	if err != nil || len(bytes.TrimSpace(rest)) != 0 {
		if key, comment, ok := parseSSH2PublicKey(data); ok {
			return key, comment, nil
		}
		return nil, "", clierr.Wrap(clierr.Usage, fmt.Errorf("%s must hold exactly one valid SSH public key", sshKeySource(source)))
	}
	return key, comment, nil
}

// parseSSH2PublicKey reads the RFC 4716 format of PuTTYgen and ssh-keygen -e, which Gitea accepts
// as well. It returns the key and the Comment header without its quotes.
func parseSSH2PublicKey(data []byte) (ssh.PublicKey, string, bool) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r", "")), "\n")
	if len(lines) < 3 || lines[0] != "---- BEGIN SSH2 PUBLIC KEY ----" || lines[len(lines)-1] != "---- END SSH2 PUBLIC KEY ----" {
		return nil, "", false
	}
	var body strings.Builder
	var comment string
	continued := false
	for _, line := range lines[1 : len(lines)-1] {
		header := continued || strings.Contains(line, ":")
		continued = header && strings.HasSuffix(line, "\\")
		switch {
		case !header:
			body.WriteString(strings.TrimSpace(line))
		case strings.HasPrefix(line, "Comment:"):
			comment = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "Comment:")), `"`)
		}
	}
	blob, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil {
		return nil, "", false
	}
	key, err := ssh.ParsePublicKey(blob)
	return key, comment, err == nil
}

func sshKeySource(source string) string {
	if source == "-" {
		return "stdin"
	}
	return source
}

func (a *application) sshKeyCreateCommand() *cobra.Command {
	var title string
	cmd := &cobra.Command{
		Use: "create [<file>]", Aliases: []string{"add"}, Short: "Add an SSH public key from a file or stdin", Args: usageArgs(cobra.MaximumNArgs(1)), ValidArgsFunction: completeFiles,
		Long: `Add an SSH public key to your account. Without a file, or with -, the key is read from stdin.

The title defaults to the key's comment, such as alice@laptop, or else to this computer's name.`,
		Example: `$ gt ssh-key add ~/.ssh/id_ed25519.pub
$ gt ssh-key add ~/.ssh/id_ed25519.pub --title "Work laptop"
$ cat key.pub | gt ssh-key add`,
		RunE: func(cmd *cobra.Command, args []string) error {
			source := "-"
			if len(args) > 0 {
				source = args[0]
			}
			if len(args) == 0 && a.deps.Streams.InTTY {
				return clierr.Wrap(clierr.Usage, errors.New("supply a public key file, such as ~/.ssh/id_ed25519.pub, or pipe a key to stdin"))
			}
			data, err := readFileOrStdin(a.deps.Streams.In, source)
			if err != nil {
				return fmt.Errorf("read public key: %w", err)
			}
			key, comment, err := publicKey(data, source)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("title") {
				title = comment
				if title == "" {
					title, _ = os.Hostname()
				}
			}
			if strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("key title cannot be empty; pass --title"))
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.SSHKeys{Client: target.client}
			created, err := service.Create(cmd.Context(), title, strings.TrimSpace(string(data)))
			if err != nil {
				if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Reason == services.ReasonExists {
					conflict.Hint = fmt.Sprintf("another SSH key is titled %q; pass a different --title", title)
					return conflict
				}
				return a.registeredKey(cmd.Context(), service, key, err)
			}
			a.done(cmd, "Added %s", sshKeyLabel(created))
			return a.jsonResult(cmd, created)
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Key title (defaults to the key comment, then the computer's name)")
	return cmd
}

// registeredKey names the key of the account that already holds key when Gitea refuses to add it.
func (a *application) registeredKey(ctx context.Context, service services.SSHKeys, key ssh.PublicKey, err error) error {
	conflict, ok := errors.AsType[*services.ConflictError](err)
	if !ok || conflict.Reason != services.ReasonKeyInUse {
		return err
	}
	keys, listErr := service.List(ctx, maxLimit, 1)
	if listErr != nil {
		return conflict
	}
	fingerprint := ssh.FingerprintSHA256(key)
	for _, registered := range keys.Items {
		if registered.Fingerprint == fingerprint {
			conflict.Hint = fmt.Sprintf("it is your SSH key %q (ID %d)", registered.Title, registered.ID)
		}
	}
	return conflict
}

func (a *application) sshKeyDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete an SSH key", Args: argsExact(1), ValidArgsFunction: a.completeSSHKeys(),
		Example: `$ gt ssh-key delete 2
$ gt ssh-key delete 2 --yes`,
		Annotations: map[string]string{"gt.text": "SSH key deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.SSHKeys{Client: target.client}
			key, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.deleteConfirmed(cmd, sshKeyLabel(key), func() error { return service.Delete(cmd.Context(), id) })
		},
	}
}
