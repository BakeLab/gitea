// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/openpgp"       //nolint:staticcheck // frozen but enough to make a throwaway key and sign with it, with no new dependency
	"golang.org/x/crypto/openpgp/armor" //nolint:staticcheck // see above
)

// integrationGPGKey makes a key with one email address and returns it, with its armored public key.
func integrationGPGKey(t *testing.T, email string) (*openpgp.Entity, string) {
	t.Helper()
	entity, err := openpgp.NewEntity("gt integration", "", email, nil)
	require.NoError(t, err)
	var public bytes.Buffer
	writer, err := armor.Encode(&public, openpgp.PublicKeyType, nil)
	require.NoError(t, err)
	require.NoError(t, entity.Serialize(writer))
	require.NoError(t, writer.Close())
	return entity, public.String()
}

func TestGiteaGPGKeyLifecycleAndVerification(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	create := func(armored string, args ...string) services.GPGKey {
		t.Helper()
		deps.Streams.In = strings.NewReader(armored)
		var key services.GPGKey
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append([]string{"gpg-key", "create", "--json"}, args...)))
		require.NoError(t, json.Unmarshal(out.Bytes(), &key))
		t.Cleanup(func() {
			_ = Execute(context.Background(), deps, []string{"gpg-key", "delete", strconv.FormatInt(key.ID, 10), "--yes"})
		})
		return key
	}
	// signedToken asks Gitea for a token and signs it with the key, into a file.
	signedToken := func(entity *openpgp.Entity) string {
		t.Helper()
		var token struct{ Token string }
		require.NoError(t, json.Unmarshal(execute("gpg-key", "token", "--json"), &token))
		require.NotEmpty(t, token.Token)
		require.Equal(t, token.Token+"\n", string(execute("gpg-key", "token")), "the token alone, for a pipe to gpg")
		var signature bytes.Buffer
		require.NoError(t, openpgp.ArmoredDetachSign(&signature, entity, strings.NewReader(token.Token), nil))
		file := filepath.Join(t.TempDir(), "token.asc")
		require.NoError(t, os.WriteFile(file, signature.Bytes(), 0o600))
		return file
	}

	// A key for an email address of the account is accepted as it is, and trusted for that address.
	trusted, trustedArmor := integrationGPGKey(t, "integration@example.test")
	key := create(trustedArmor)
	require.Positive(t, key.ID)
	require.NotEmpty(t, key.KeyID)
	require.False(t, key.Verified, "the key itself is not verified until it has signed a token")
	require.True(t, key.CanSign)
	require.Equal(t, []services.GPGKeyEmail{{Email: "integration@example.test", Verified: true}}, key.Emails)

	id := strconv.FormatInt(key.ID, 10)
	var viewed services.GPGKey
	require.NoError(t, json.Unmarshal(execute("gpg-key", "view", id, "--json"), &viewed))
	require.Equal(t, key.KeyID, viewed.KeyID)
	require.NotEmpty(t, viewed.PublicKey, "Gitea gives the key as base64, without the armor")
	require.Contains(t, string(execute("gpg-key", "view", id)), key.KeyID)
	var keys []services.GPGKey
	require.NoError(t, json.Unmarshal(execute("gpg-key", "list", "--json", "id,key_id,verified"), &keys))
	require.Contains(t, keys, services.GPGKey{ID: key.ID, KeyID: key.KeyID})
	require.Contains(t, string(execute("gpg-key", "list")), key.KeyID)

	var verified services.GPGKey
	require.NoError(t, json.Unmarshal(execute("gpg-key", "verify", key.KeyID, signedToken(trusted), "--json"), &verified))
	require.True(t, verified.Verified, "the signed token proves control of the key")
	require.Equal(t, key.ID, verified.ID)

	// A key that names no address of the account is refused, unless a signed token comes with it.
	other, otherArmor := integrationGPGKey(t, "someone-else@example.invalid")
	deps.Streams.In = strings.NewReader(otherArmor)
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"gpg-key", "create"}), "names no activated email address of your account")
	foreign := create(otherArmor, "--signature-file", signedToken(other))
	require.True(t, foreign.Verified, "it is stored as verified")
	require.Empty(t, foreign.Emails, "Gitea keeps no address of a key that it verified by signature")

	// What cannot be a public key is refused before it is sent.
	privateFile := filepath.Join(t.TempDir(), "private.asc")
	require.NoError(t, os.WriteFile(privateFile, []byte("-----BEGIN PGP PRIVATE KEY BLOCK-----\nAAAA\n-----END PGP PRIVATE KEY BLOCK-----\n"), 0o600))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"gpg-key", "create", privateFile}), "holds a private key")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"gpg-key", "verify", key.KeyID, privateFile}), "ASCII-armored signature")

	foreignID := strconv.FormatInt(foreign.ID, 10)
	require.Error(t, Execute(context.Background(), deps, []string{"gpg-key", "delete", foreignID, "--no-input"}), "without --yes and a terminal there is nobody to ask")
	execute("gpg-key", "delete", foreignID, "--yes")
	execute("gpg-key", "delete", id, "--yes")
	require.NoError(t, json.Unmarshal(execute("gpg-key", "list", "--json", "id"), &keys))
	for _, item := range keys {
		require.NotEqual(t, key.ID, item.ID)
		require.NotEqual(t, foreign.ID, item.ID)
	}
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"gpg-key", "view", id}), "GPG key "+id)
}
