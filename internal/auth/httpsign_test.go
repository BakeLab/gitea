// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestHTTPSignAttachesSignatureHeaders(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	path := filepath.Join(dir, "id_rsa")
	block, err := ssh.MarshalPrivateKey(key, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))

	var sawSignature, sawDigest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSignature = r.Header.Get("Signature") != ""
		sawDigest = r.Header.Get("Digest") != ""
		require.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	defer server.Close()

	httpSign, err := newHTTPSign(path, false, "", nil)
	require.NoError(t, err)
	require.Equal(t, ssh.FingerprintSHA256(signer.PublicKey()), ssh.FingerprintSHA256(httpSign.PublicKey()))

	client := transport.New(server.URL, "", false)
	client.Signer = httpSign
	response, err := client.Do(t.Context(), http.MethodGet, "user", nil, nil)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.True(t, sawSignature)
	require.False(t, sawDigest)

	sawSignature, sawDigest = false, false
	response, err = client.Do(t.Context(), http.MethodPost, "user", bytes.NewReader([]byte(`{"ok":true}`)), nil)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.True(t, sawSignature)
	require.True(t, sawDigest)

	account := settings.Account{AuthMethod: "httpsign", SSHKey: path}
	fromAccount, err := SignerForAccount(account, func() ([]byte, error) {
		t.Error("unencrypted key asked for a passphrase")
		return nil, nil
	})
	require.NoError(t, err)
	require.NotNil(t, fromAccount)
}

func TestEncryptedSSHKeyUsesInjectedPassphrase(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte("correct horse"))
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	account := settings.Account{AuthMethod: settings.AuthHTTPSign, SSHKey: path}

	_, err = SignerForAccount(account, nil)
	require.ErrorContains(t, err, "passphrase protected; add it to ssh-agent")

	prompts := 0
	signer, err := SignerForAccount(account, func() ([]byte, error) { prompts++; return []byte("correct horse"), nil })
	require.NoError(t, err)
	require.Equal(t, 1, prompts)
	require.Equal(t, ssh.KeyAlgoED25519, signer.PublicKey().Type())

	_, err = SignerForAccount(account, func() ([]byte, error) { return []byte("wrong"), nil })
	require.ErrorContains(t, err, "cannot parse SSH key")
}

func TestFindCertSignerHonorsValidity(t *testing.T) {
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ca, err := ssh.NewSignerFromKey(caKey)
	require.NoError(t, err)
	_, userKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	user, err := ssh.NewSignerFromKey(userKey)
	require.NoError(t, err)
	certSigner := func(validBefore uint64) ssh.Signer {
		cert := &ssh.Certificate{Key: user.PublicKey(), CertType: ssh.UserCert, ValidPrincipals: []string{"alice"}, ValidBefore: validBefore}
		require.NoError(t, cert.SignCert(rand.Reader, ca))
		signer, err := ssh.NewCertSigner(cert, user)
		require.NoError(t, err)
		return signer
	}
	for name, tc := range map[string]struct {
		validBefore uint64
		found       bool
	}{
		"unlimited": {ssh.CertTimeInfinity, true},
		"valid":     {uint64(time.Now().Add(time.Hour).Unix()), true},
		"expiring":  {uint64(time.Now().Add(5 * time.Second).Unix()), false},
		"expired":   {uint64(time.Now().Add(-time.Hour).Unix()), false},
	} {
		signer := certSigner(tc.validBefore)
		require.Equal(t, tc.found, findCertSigner([]ssh.Signer{signer}, "alice") != nil, name)
	}
}
