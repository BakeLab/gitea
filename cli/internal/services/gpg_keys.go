// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// GPGKeyEmail is an email address of a GPG key and whether Gitea accepts it for the account.
type GPGKeyEmail struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

// GPGKey describes a GPG public key attached to the selected account.
type GPGKey struct {
	ID                int64         `json:"id"`
	KeyID             string        `json:"key_id"`
	PrimaryKeyID      string        `json:"primary_key_id"`
	PublicKey         string        `json:"public_key"`
	Emails            []GPGKeyEmail `json:"emails"`
	Subkeys           []string      `json:"subkeys"`
	CanSign           bool          `json:"can_sign"`
	CanEncryptComms   bool          `json:"can_encrypt_comms"`
	CanEncryptStorage bool          `json:"can_encrypt_storage"`
	CanCertify        bool          `json:"can_certify"`
	Verified          bool          `json:"verified"`
	CreatedAt         string        `json:"created_at"`
	ExpiresAt         string        `json:"expires_at"`
}

// GPGToken is the text that proves control of a GPG key once it is signed with it.
type GPGToken struct {
	Token string `json:"token"`
}

func gpgKeyFrom(key *gitea.GPGKey) GPGKey {
	emails := convertAll(key.Emails, func(email *gitea.GPGKeyEmail) GPGKeyEmail {
		return GPGKeyEmail{Email: email.Email, Verified: email.Verified}
	})
	subkeys := convertAll(key.SubsKey, func(subkey *gitea.GPGKey) string { return subkey.KeyID })
	return GPGKey{
		ID: key.ID, KeyID: key.KeyID, PrimaryKeyID: key.PrimaryKeyID, PublicKey: key.PublicKey, Emails: emails, Subkeys: subkeys,
		CanSign: key.CanSign, CanEncryptComms: key.CanEncryptComms, CanEncryptStorage: key.CanEncryptStorage, CanCertify: key.CanCertify,
		Verified: key.Verified, CreatedAt: timestamp(key.Created), ExpiresAt: timestamp(key.Expires),
	}
}

// GPGKeys manages GPG public keys for the authenticated account.
type GPGKeys struct{ Client *transport.Client }

// List returns a page of GPG keys.
func (s GPGKeys) List(ctx context.Context, limit, page int) (Page[GPGKey], error) {
	keys, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.GPGKey, *gitea.Response, error) {
		return c.Users.ListMyGPGKeys(ctx, &gitea.ListGPGKeysOptions{ListOptions: list})
	})
	return convertPage(keys, gpgKeyFrom), err
}

// View retrieves a GPG key of the account by ID.
func (s GPGKeys) View(ctx context.Context, id int64) (GPGKey, error) {
	key, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.GPGKey, *gitea.Response, error) {
		return c.Users.GetGPGKey(ctx, id)
	})
	if err != nil {
		return GPGKey{}, Classify(err, "GPG key", strconv.FormatInt(id, 10), "")
	}
	return gpgKeyFrom(key), nil
}

// Create registers an armored GPG public key for the account. signature, an armored signature of
// the token of Token, proves control of a key without an activated email address of the account.
func (s GPGKeys) Create(ctx context.Context, armored, signature string) (GPGKey, error) {
	created, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.GPGKey, *gitea.Response, error) {
		return c.Users.CreateGPGKey(ctx, gitea.CreateGPGKeyOption{ArmoredKey: armored, Signature: signature})
	})
	// Gitea answers 404 when the key names no activated email address, and stores nothing.
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusNotFound && strings.Contains(apiErr.Message, "valid signature for the token") {
		return GPGKey{}, errors.New("the key names no activated email address of your account; add it with --signature-file and a signature of what gt gpg-key token prints")
	}
	if err != nil {
		return GPGKey{}, Classify(err, "GPG key", "", "")
	}
	return gpgKeyFrom(created), nil
}

// Delete removes only the selected GPG key.
func (s GPGKeys) Delete(ctx context.Context, id int64) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Users.DeleteGPGKey(ctx, id)
	})
	return Classify(err, "GPG key", strconv.FormatInt(id, 10), "")
}

// Token returns the text to sign with a GPG key to verify it.
func (s GPGKeys) Token(ctx context.Context) (GPGToken, error) {
	token, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (string, *gitea.Response, error) {
		return c.Users.GetGPGKeyVerificationToken(ctx)
	})
	return GPGToken{Token: token}, err
}

// Verify proves control of the GPG key keyID with the armored signature of the token.
func (s GPGKeys) Verify(ctx context.Context, keyID, signature string) (GPGKey, error) {
	verified, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.GPGKey, *gitea.Response, error) {
		return c.Users.VerifyGPGKey(ctx, gitea.VerifyGPGKeyOption{KeyID: keyID, Signature: signature})
	})
	if err != nil {
		return GPGKey{}, Classify(err, "GPG key", keyID, "")
	}
	return gpgKeyFrom(verified), nil
}
