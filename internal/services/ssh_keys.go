// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// SSHKey describes a public key attached to the selected account.
type SSHKey struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at"`
	ReadOnly    bool   `json:"read_only"`
}

func sshKeyFrom(key *gitea.PublicKey) SSHKey {
	return SSHKey{ID: key.ID, Title: key.Title, Key: key.Key, Fingerprint: key.Fingerprint, CreatedAt: timestamp(key.Created), ReadOnly: key.ReadOnly}
}

// SSHKeys manages public keys for the authenticated account.
type SSHKeys struct{ Client *transport.Client }

// List returns a page of public keys.
func (s SSHKeys) List(ctx context.Context, limit, page int) (Page[SSHKey], error) {
	keys, err := listPage(limit, page, func(limit, page int) ([]*gitea.PublicKey, http.Header, error) {
		keys, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.PublicKey, *gitea.Response, error) {
			return c.Users.ListMyPublicKeys(ctx, gitea.ListPublicKeysOptions{ListOptions: listOptions(limit, page)})
		})
		return keys, headerOf(resp), err
	})
	return convertPage(keys, sshKeyFrom), err
}

// View retrieves a public key by ID.
func (s SSHKeys) View(ctx context.Context, id int64) (SSHKey, error) {
	key, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.PublicKey, *gitea.Response, error) {
		return c.Users.GetPublicKey(ctx, id)
	})
	if err != nil {
		return SSHKey{}, err
	}
	return sshKeyFrom(key), nil
}

// Create registers one public key for the account.
func (s SSHKeys) Create(ctx context.Context, title, key string) (SSHKey, error) {
	created, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.PublicKey, *gitea.Response, error) {
		return c.Users.CreatePublicKey(ctx, gitea.CreateKeyOption{Title: title, Key: key})
	})
	if err != nil {
		return SSHKey{}, err
	}
	return sshKeyFrom(created), nil
}

// Delete removes only the selected public key.
func (s SSHKeys) Delete(ctx context.Context, id int64) error {
	return do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Users.DeletePublicKey(ctx, id)
	})
}
