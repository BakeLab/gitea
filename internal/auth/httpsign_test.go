// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/transport"

	"github.com/42wim/httpsig"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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

func newUserCertificate(t *testing.T, principal string) (ed25519.PrivateKey, *ssh.Certificate) {
	t.Helper()
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	ca, err := ssh.NewSignerFromKey(caKey)
	require.NoError(t, err)
	_, userKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	user, err := ssh.NewSignerFromKey(userKey)
	require.NoError(t, err)
	cert := &ssh.Certificate{Key: user.PublicKey(), CertType: ssh.UserCert, ValidPrincipals: []string{principal}, ValidBefore: ssh.CertTimeInfinity}
	require.NoError(t, cert.SignCert(rand.Reader, ca))
	return userKey, cert
}

// requireVerifiedSignature checks req the way Gitea does: by the signing key, with the headers the signature names.
func requireVerifiedSignature(t *testing.T, req *http.Request, key ed25519.PrivateKey, keyID string, headers ...string) {
	t.Helper()
	verifier, err := httpsig.NewVerifier(req)
	require.NoError(t, err)
	require.Equal(t, keyID, verifier.KeyId())
	require.NoError(t, verifier.Verify(key.Public(), httpsig.ED25519))
	for _, header := range headers {
		require.Contains(t, req.Header.Get("Signature"), header)
	}
}

func TestHTTPSignCertificateFromKeyFile(t *testing.T) {
	key, cert := newUserCertificate(t, "alice")
	block, err := ssh.MarshalPrivateKey(key, "")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
	account := settings.Account{AuthMethod: settings.AuthSSH, SSHKey: path, SSHCertPrincipal: "alice"}

	_, err = SignerForAccount(account, nil)
	require.ErrorContains(t, err, "cannot read SSH certificate")
	require.NoError(t, os.WriteFile(path+"-cert.pub", []byte("not a key"), 0o600))
	_, err = SignerForAccount(account, nil)
	require.ErrorContains(t, err, "cannot parse SSH certificate")
	require.NoError(t, os.WriteFile(path+"-cert.pub", ssh.MarshalAuthorizedKey(cert.Key), 0o600))
	_, err = SignerForAccount(account, nil)
	require.ErrorContains(t, err, "does not contain a certificate")

	require.NoError(t, os.WriteFile(path+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0o600))
	signer, err := SignerForAccount(account, nil)
	require.NoError(t, err)
	require.True(t, signer.Cert)
	body := []byte(`{"ok":true}`)
	req := httptest.NewRequest(http.MethodPost, "https://example.test/api/v1/user", bytes.NewReader(body))
	require.NoError(t, signer.SignRequest(req, body))
	sent, err := base64.RawStdEncoding.DecodeString(req.Header.Get("x-ssh-certificate"))
	require.NoError(t, err)
	require.Equal(t, cert.Marshal(), sent)
	requireVerifiedSignature(t, req, key, "gitea", "x-ssh-certificate", "digest")

	plain, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	err = (&HTTPSign{Signer: plain, Cert: true}).SignRequest(httptest.NewRequest(http.MethodGet, "https://example.test/api/v1/user", nil), nil)
	require.ErrorContains(t, err, "no SSH certificate found")
}

func TestAgentSignerSelectsTheConfiguredKey(t *testing.T) {
	keyring := agent.NewKeyring()
	_, err := agentSigner(keyring, false, "")
	require.ErrorContains(t, err, "no signers found in ssh-agent")

	certKey, cert := newUserCertificate(t, "alice")
	require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: certKey, Certificate: cert}))
	var keys []ed25519.PrivateKey
	var public []ssh.PublicKey
	for range 2 {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: key}))
		pub, err := ssh.NewPublicKey(key.Public())
		require.NoError(t, err)
		keys, public = append(keys, key), append(public, pub)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public[1])))
	for _, tc := range []struct {
		name  string
		cert  bool
		match string
		want  ssh.PublicKey
		err   string
	}{
		{name: "first key", want: public[0]},
		{name: "fingerprint", match: ssh.FingerprintSHA256(public[1]), want: public[1]},
		{name: "authorized key", match: authorized, want: public[1]},
		{name: "unknown key", match: "SHA256:missing", err: `no public key found in ssh-agent for "SHA256:missing"`},
		{name: "any certificate", cert: true, want: cert},
		{name: "principal", cert: true, match: "alice", want: cert},
		{name: "unknown principal", cert: true, match: "bob", err: `no certificate found in ssh-agent for principal "bob"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer, err := agentSigner(keyring, tc.cert, tc.match)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.cert, signer.Cert)
			require.Equal(t, tc.want.Marshal(), signer.PublicKey().Marshal())
		})
	}

	signer, err := agentSigner(keyring, false, ssh.FingerprintSHA256(public[1]))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "https://example.test/api/v1/user", nil)
	require.NoError(t, signer.SignRequest(req, nil))
	require.Empty(t, req.Header.Get("x-ssh-certificate"))
	requireVerifiedSignature(t, req, keys[1], ssh.FingerprintSHA256(public[1]), "(request-target)")
}
