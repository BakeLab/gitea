// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

const (
	pgpPublicBegin    = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	pgpPublicEnd      = "-----END PGP PUBLIC KEY BLOCK-----"
	pgpPrivateMarker  = "PGP PRIVATE KEY BLOCK-----"
	pgpSignatureBegin = "-----BEGIN PGP SIGNATURE-----"
	pgpSignatureEnd   = "-----END PGP SIGNATURE-----"
)

func (a *application) gpgKeyCommand() *cobra.Command {
	group := &cobra.Command{Use: "gpg-key", Short: "Manage GPG keys", Long: `Manage the GPG public keys of your account, which Gitea uses to mark commits and tags that you
signed as verified.

Gitea takes a key that names an activated email address of your account, and trusts it for those
addresses. A signed token, see gt gpg-key token, adds a key that names none, and trusts a key for
all of your activated addresses: gt gpg-key token
prints the token and gt gpg-key verify sends the signature.`}
	group.AddCommand(a.gpgKeyListCommand(), a.gpgKeyViewCommand(), a.gpgKeyCreateCommand(), a.gpgKeyDeleteCommand(), a.gpgKeyTokenCommand(), a.gpgKeyVerifyCommand())
	return jsonOutput[services.GPGKey](group)
}

// gpgKeyLabel names a key in messages: `GPG key 3AA5C34371567BD2 (ID 4)`.
func gpgKeyLabel(key services.GPGKey) string {
	return fmt.Sprintf("GPG key %s (ID %d)", key.KeyID, key.ID)
}

// completeGPGKeys completes the key IDs of the account with their GPG key IDs.
func (a *application) completeGPGKeys() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		keys, err := (services.GPGKeys{Client: t.client}).List(ctx, completionLimit, 1)
		return describe(keys.Items, func(key services.GPGKey) (string, string) {
			return strconv.FormatInt(key.ID, 10), key.KeyID
		}), err
	})
}

// completeGPGKeyIDs completes the GPG key IDs of the account.
func (a *application) completeGPGKeyIDs() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		keys, err := (services.GPGKeys{Client: t.client}).List(ctx, completionLimit, 1)
		return describe(keys.Items, func(key services.GPGKey) (string, string) { return key.KeyID, gpgKeyEmails(key) }), err
	})
}

// gpgKeyEmails joins the email addresses of a key, marking those Gitea has not verified.
func gpgKeyEmails(key services.GPGKey) string {
	emails := make([]string, len(key.Emails))
	for i, email := range key.Emails {
		emails[i] = email.Email
		if !email.Verified {
			emails[i] += " (unverified)"
		}
	}
	return strings.Join(emails, ", ")
}

func (a *application) gpgKeyListCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use: "list", Short: "List GPG keys", Args: argsExact(0),
		Example: `$ gt gpg-key list
$ gt gpg-key list --json key_id,verified,emails`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			keys, err := (services.GPGKeys{Client: target.client}).List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			account := target.login()
			return writeTable(a, cmd, keys.Items, listSpec[services.GPGKey]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Key ID"}, {Header: "Emails", Flex: true}, {Header: "Status"}, {Header: "Expires"}, {Header: "Added"}},
				Row: func(key services.GPGKey) []terminal.Cell {
					status, expires := "unverified", terminal.Cell{Text: "never", Muted: true}
					if key.Verified {
						status = "verified"
					}
					if t := terminal.ParseTime(key.ExpiresAt); !t.IsZero() {
						expires = terminal.Cell{Time: t}
					}
					return []terminal.Cell{
						{Text: strconv.FormatInt(key.ID, 10), Link: webURL(target, "user", "settings", "keys")},
						{Text: terminal.SafeLine(key.KeyID), Bold: true},
						{Text: terminal.SafeLine(gpgKeyEmails(key))},
						{Text: status, Muted: key.Verified},
						expires,
						{Time: terminal.ParseTime(key.CreatedAt)},
					}
				},
				Context: "GPG keys of " + account,
				Empty:   "no GPG keys for " + account + "; add one with: gpg --armor --export KEYID | gt gpg-key create",
			}, servicePage(keys, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	return cmd
}

func (a *application) gpgKeyViewCommand() *cobra.Command {
	return &cobra.Command{
		Use: "view <id>", Short: "View a GPG key", Args: argsExact(1), ValidArgsFunction: a.completeGPGKeys(),
		Long: "View a GPG key by the ID that gt gpg-key list shows, not by its GPG key ID.",
		Example: `$ gt gpg-key view 4
# print the email addresses of the key
$ gt gpg-key view 4 --jq '.emails[].email'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			key, err := (services.GPGKeys{Client: target.client}).View(cmd.Context(), id)
			if err != nil {
				return err
			}
			d := a.display()
			return writeView(a, cmd, key, func(key services.GPGKey) terminal.View {
				badge := terminal.Cell{Text: "unverified"}
				if key.Verified {
					badge = terminal.Cell{Text: "verified", Muted: true}
				}
				created := timeField("Created", key.CreatedAt, d)
				var uses []string
				for _, use := range []struct {
					name string
					set  bool
				}{{"certify", key.CanCertify}, {"encrypt communications", key.CanEncryptComms}, {"encrypt storage", key.CanEncryptStorage}, {"sign", key.CanSign}} {
					if use.set {
						uses = append(uses, use.name)
					}
				}
				return terminal.View{
					Title: key.KeyID, ID: fmt.Sprintf("(ID %d)", key.ID), Badges: []terminal.Cell{badge},
					Fields: []terminal.Field{
						{Label: "Verified", Value: yesNo(key.Verified), PipedOnly: true},
						{Label: "Emails", Value: gpgKeyEmails(key)},
						{Label: "Subkeys", Value: strings.Join(key.Subkeys, ", ")},
						{Label: "Can", Value: strings.Join(uses, ", ")},
						created, timeField("Expires", key.ExpiresAt, d),
						{Label: "Public key", Value: key.PublicKey},
					},
					Bodyless: true, Kind: "GPG key", URL: webURL(target, "user", "settings", "keys"),
				}
			})
		},
	}
}

// armoredBlock checks that data is exactly one armored block between begin and end, and returns it.
// what names the block in messages, and fix says how to get a correct one.
func armoredBlock(data []byte, source, begin, end, what, fix string) (string, error) {
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	switch {
	case !strings.HasPrefix(text, begin) || !strings.HasSuffix(text, end) || strings.Count(text, begin) != 1:
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("%s must hold exactly one ASCII-armored %s; %s", sshKeySource(source), what, fix))
	case bytes.IndexByte(data, 0) >= 0:
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("%s holds binary data; %s", sshKeySource(source), fix))
	}
	return text + "\n", nil
}

// armoredPublicKey reads the GPG public key in data and points to gpg --export when it is not one.
func armoredPublicKey(data []byte, source string) (string, error) {
	if bytes.Contains(data, []byte(pgpPrivateMarker)) {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("%s holds a private key; export the public key with: gpg --armor --export KEYID", sshKeySource(source)))
	}
	return armoredBlock(data, source, pgpPublicBegin, pgpPublicEnd, "GPG public key", "export it with: gpg --armor --export KEYID")
}

func (a *application) gpgKeyCreateCommand() *cobra.Command {
	var signatureFile string
	cmd := &cobra.Command{
		Use: "create [<file>]", Aliases: []string{"add"}, Short: "Add a GPG public key from a file or stdin", Args: nonBlankArgs(cobra.MaximumNArgs(1), "file"), ValidArgsFunction: completeFiles,
		Long: `Add an ASCII-armored GPG public key to your account. Without a file, or with -, the key is read
from stdin. Export it with gpg --armor --export KEYID.

Gitea refuses a key that names no activated email address of your account, unless --signature-file
gives a signature of what gt gpg-key token prints. It then stores the key as verified.`,
		Example: `$ gpg --armor --export 3AA5C34371567BD2 | gt gpg-key create
$ gt gpg-key create public.asc
$ gt gpg-key token | gpg --armor --detach-sign --default-key 3AA5C34371567BD2 > token.asc
$ gt gpg-key create public.asc --signature-file token.asc`,
		RunE: func(cmd *cobra.Command, args []string) error {
			source := "-"
			if len(args) > 0 {
				source = args[0]
			}
			if len(args) == 0 && a.deps.Streams.InTTY {
				return clierr.Wrap(clierr.Usage, errors.New("supply a public key file, or pipe one to stdin: gpg --armor --export KEYID | gt gpg-key create"))
			}
			if source == "-" && signatureFile == "-" {
				return clierr.Wrap(clierr.Usage, errors.New("--signature-file - requires the public key in a file"))
			}
			data, err := readFileOrStdin(a.deps.Streams.In, source)
			if err != nil {
				return fmt.Errorf("read public key: %w", err)
			}
			armored, err := armoredPublicKey(data, source)
			if err != nil {
				return err
			}
			var signature string
			if signatureFile != "" {
				data, err := readFileOrStdin(a.deps.Streams.In, signatureFile)
				if err != nil {
					return fmt.Errorf("read signature: %w", err)
				}
				if signature, err = armoredBlock(data, signatureFile, pgpSignatureBegin, pgpSignatureEnd, "signature", "create it with: gpg --armor --detach-sign"); err != nil {
					return err
				}
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			created, err := (services.GPGKeys{Client: target.client}).Create(cmd.Context(), armored, signature)
			if err != nil {
				return err
			}
			a.done(cmd, "Added %s", gpgKeyLabel(created))
			if !created.Verified {
				a.nextStep(cmd, "Trust it for all your email addresses: sign what gt gpg-key token prints, then run gt gpg-key verify %s", created.KeyID)
			}
			return a.jsonResult(cmd, created)
		},
	}
	cmd.Flags().StringVar(&signatureFile, "signature-file", "", "Read a signature of the token from `file`, or - for stdin, for a key without an activated email address")
	return cmd
}

func (a *application) gpgKeyDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete a GPG key", Args: argsExact(1), ValidArgsFunction: a.completeGPGKeys(),
		Long: `Delete a GPG key by the ID that gt gpg-key list shows. Gitea stops marking commits that this key
signed as verified.`,
		Example: `$ gt gpg-key delete 4
$ gt gpg-key delete 4 --yes`,
		Annotations: map[string]string{"gt.text": "GPG key deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.GPGKeys{Client: target.client}
			key, err := service.View(cmd.Context(), id)
			if err != nil {
				return err
			}
			return a.deleteConfirmed(cmd, gpgKeyLabel(key), func() error { return service.Delete(cmd.Context(), id) })
		},
	}
}

func (a *application) gpgKeyTokenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "token", Short: "Print the token that verifies a GPG key", Args: argsExact(0),
		Long: `Print the token that proves you control a GPG key. Sign it with the key and send the signature
with gt gpg-key verify. The token changes every minute and Gitea takes only the last two.`,
		Example: `$ gt gpg-key token | gpg --armor --detach-sign --default-key 3AA5C34371567BD2 | gt gpg-key verify 3AA5C34371567BD2`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			token, err := (services.GPGKeys{Client: target.client}).Token(cmd.Context())
			if err != nil {
				return err
			}
			return a.created(cmd, token, token.Token)
		},
	}
	return jsonOutput[services.GPGToken](cmd)
}

func (a *application) gpgKeyVerifyCommand() *cobra.Command {
	return &cobra.Command{
		Use: "verify <key-id> [<file>]", Short: "Verify a GPG key with a signed token", Args: nonBlankArgs(cobra.RangeArgs(1, 2), "key-id", "file"), ValidArgsFunction: pathArgs(1, false, completeFiles, a.completeGPGKeyIDs()),
		Long: `Verify a GPG key that Gitea holds as unverified, so that it is trusted for all of your activated
email addresses, not only those it names. Sign the token that gt gpg-key token prints with
the key, as an ASCII-armored detached signature, and pass the signature as a file or on stdin. The key
is named by its GPG key ID, as gt gpg-key list shows it.`,
		Example: `$ gt gpg-key token | gpg --armor --detach-sign --default-key 3AA5C34371567BD2 > token.asc
$ gt gpg-key verify 3AA5C34371567BD2 token.asc`,
		RunE: func(cmd *cobra.Command, args []string) error {
			source := "-"
			if len(args) > 1 {
				source = args[1]
			}
			if len(args) == 1 && a.deps.Streams.InTTY {
				return clierr.Wrap(clierr.Usage, errors.New("supply the signature as a file, or pipe it to stdin"))
			}
			data, err := readFileOrStdin(a.deps.Streams.In, source)
			if err != nil {
				return fmt.Errorf("read signature: %w", err)
			}
			signature, err := armoredBlock(data, source, pgpSignatureBegin, pgpSignatureEnd, "signature", "create it with: gpg --armor --detach-sign")
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			key, err := (services.GPGKeys{Client: target.client}).Verify(cmd.Context(), args[0], signature)
			if err != nil {
				return err
			}
			a.done(cmd, "Verified %s", gpgKeyLabel(key))
			return a.jsonResult(cmd, key)
		},
	}
}
