// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testGPGPublicKey = "-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nmQENBF\n=abcd\n-----END PGP PUBLIC KEY BLOCK-----\n"
	testGPGSignature = "-----BEGIN PGP SIGNATURE-----\n\niQEzBA\n=wxyz\n-----END PGP SIGNATURE-----\n"
	gpgKeyJSON       = `{"id":4,"key_id":"3AA5C34371567BD2","primary_key_id":"","public_key":"` + `-----BEGIN PGP PUBLIC KEY BLOCK-----` + `","emails":[{"email":"alice@example.com","verified":true},{"email":"old@example.com","verified":false}],"subkeys":[{"key_id":"AAAA"}],"can_sign":true,"can_certify":true,"verified":false,"created_at":"2026-10-01T10:00:00Z","expires_at":"2027-10-01T10:00:00Z"}`
)

func TestGPGKeyInputIsCheckedBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"empty", "", "must hold exactly one ASCII-armored GPG public key; export it with: gpg --armor --export KEYID"},
		{"binary", "\x99\x01\x0d\x04" + strings.Repeat("\x00", 8), "must hold exactly one ASCII-armored GPG public key"},
		{"private", "-----BEGIN PGP PRIVATE KEY BLOCK-----\nAAAA\n-----END PGP PRIVATE KEY BLOCK-----\n", "holds a private key; export the public key with: gpg --armor --export KEYID"},
		{"two keys", testGPGPublicKey + testGPGPublicKey, "must hold exactly one ASCII-armored GPG public key"},
		{"a signature", testGPGSignature, "must hold exactly one ASCII-armored GPG public key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key.asc")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			requireUsageBeforeRequests(t, tc.want, "gpg-key", "create", path)
		})
	}
	path := filepath.Join(t.TempDir(), "token.asc")
	require.NoError(t, os.WriteFile(path, []byte(testGPGPublicKey), 0o600))
	requireUsageBeforeRequests(t, "must hold exactly one ASCII-armored signature; create it with: gpg --armor --detach-sign", "gpg-key", "verify", "3AA5C34371567BD2", path)
	requireUsageBeforeRequests(t, "invalid resource number \"x\"", "gpg-key", "delete", "x")
	requireUsageBeforeRequests(t, "must hold exactly one ASCII-armored signature", "gpg-key", "create", path, "--signature-file", path)
}

func TestGPGKeyCreateExplainsAKeyWithoutAnActivatedAddress(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"None of the emails attached to the GPG key could be found. It may still be added if you provide a valid signature for the token: 2f8a9c"}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_TOKEN": "a-credential"}) // a message holding the credential is dropped
	deps.Streams.In = bytes.NewBufferString(testGPGPublicKey)
	err := Execute(context.Background(), deps, []string{"gpg-key", "create"})
	require.ErrorContains(t, err, "names no activated email address")
	require.NotContains(t, err.Error(), "not found")
}

func TestGPGKeyCommands(t *testing.T) {
	var requests []string
	var bodies []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v1"))
		bodies = append(bodies, body)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/user/gpg_keys":
			writeFirstPage(w, r, `[`+gpgKeyJSON+`]`)
		case "GET /api/v1/user/gpg_keys/4":
			_, _ = w.Write([]byte(gpgKeyJSON))
		case "GET /api/v1/user/gpg_keys/9":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"GPGKeyNotExist"}`))
		case "POST /api/v1/user/gpg_keys":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(gpgKeyJSON))
		case "DELETE /api/v1/user/gpg_keys/4":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/user/gpg_key_token":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("tok-123"))
		case "POST /api/v1/user/gpg_key_verify":
			_, _ = w.Write([]byte(strings.Replace(gpgKeyJSON, `"verified":false,"created`, `"verified":true,"created`, 1)))
		default:
			t.Errorf("unexpected request %s", r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	run := func(stdin string, args ...string) (string, string, error) {
		deps, out, diagnostics := serverDeps(t, server.URL)
		deps.Streams.In = bytes.NewBufferString(stdin)
		err := Execute(context.Background(), deps, append([]string{"gpg-key"}, args...))
		return out.String(), diagnostics.String(), err
	}

	out, _, err := run("", "list", "--json", "id,key_id,verified,emails,subkeys,can_sign,expires_at")
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":4,"key_id":"3AA5C34371567BD2","verified":false,"emails":[{"email":"alice@example.com","verified":true},{"email":"old@example.com","verified":false}],"subkeys":["AAAA"],"can_sign":true,"expires_at":"2027-10-01T10:00:00Z"}]`, out)

	out, _, err = run("", "list")
	require.NoError(t, err)
	require.Equal(t, "4\t3AA5C34371567BD2\talice@example.com, old@example.com (unverified)\tunverified\t2027-10-01T10:00:00Z\t2026-10-01T10:00:00Z\n", out)

	out, _, err = run("", "view", "4")
	require.NoError(t, err)
	for _, want := range []string{"3AA5C34371567BD2", "verified:\tno", "subkeys:\tAAAA", "can:\tcertify, sign", "public key:\t-----BEGIN PGP PUBLIC KEY BLOCK-----"} {
		require.Contains(t, out, want)
	}

	requests, bodies = nil, nil
	out, errOut, err := run(testGPGPublicKey, "create", "--json", "key_id")
	require.NoError(t, err)
	require.JSONEq(t, `{"key_id":"3AA5C34371567BD2"}`, out)
	require.Equal(t, []string{"POST /user/gpg_keys"}, requests)
	require.Equal(t, testGPGPublicKey, bodies[0]["armored_public_key"])
	require.Empty(t, errOut, "a key that names an activated address needs nothing more")

	path := filepath.Join(t.TempDir(), "token.asc")
	require.NoError(t, os.WriteFile(path, []byte(testGPGSignature), 0o600))
	bodies = nil
	_, _, err = run(testGPGPublicKey, "create", "--signature-file", path)
	require.NoError(t, err)
	require.Equal(t, testGPGSignature, bodies[0]["armored_signature"])

	out, _, err = run("", "token")
	require.NoError(t, err)
	require.Equal(t, "tok-123\n", out)
	out, _, err = run("", "token", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"tok-123"}`, out)

	requests, bodies = nil, nil
	out, _, err = run(testGPGSignature, "verify", "3AA5C34371567BD2", "--json", "verified")
	require.NoError(t, err)
	require.JSONEq(t, `{"verified":true}`, out)
	require.Equal(t, map[string]any{"key_id": "3AA5C34371567BD2", "armored_signature": testGPGSignature}, bodies[0])

	requests = nil
	_, _, err = run("", "delete", "4", "--yes")
	require.NoError(t, err)
	require.Equal(t, []string{"GET /user/gpg_keys/4", "DELETE /user/gpg_keys/4"}, requests)

	_, _, err = run("", "view", "9")
	require.ErrorContains(t, err, "GPG key 9")
}
