// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"golang.org/x/crypto/ssh"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginClientWithSSHPubkeyDoesNotDeadlockOnFirstRequest(t *testing.T) {
	t.Parallel()

	sshKeyPath, fingerprint := writeTestSSHKey(t)

	var versionRequests atomic.Int32
	var issueRequests atomic.Int32
	var signedVersionRequests atomic.Int32
	var signedIssueRequests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			versionRequests.Add(1)
			if r.Header.Get("Signature") != "" {
				signedVersionRequests.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"1.26.4"}`))
		case "/api/v1/repos/gitea/tea/issues":
			issueRequests.Add(1)
			if r.Header.Get("Signature") != "" {
				signedIssueRequests.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	login := &Login{
		Name:              "ssh-login",
		URL:               server.URL,
		SSHKey:            sshKeyPath,
		SSHKeyFingerprint: fingerprint,
		VersionCheck:      true,
	}

	type result struct {
		issues []*gitea.Issue
		err    error
	}

	done := make(chan result, 1)
	go func() {
		issues, _, err := login.Client().Issues.ListRepoIssues(context.Background(), "gitea", "tea", gitea.ListIssueOption{})
		done <- result{issues: issues, err: err}
	}()

	select {
	case res := <-done:
		require.NoError(t, res.err)
		assert.Empty(t, res.issues)
	case <-time.After(2 * time.Second):
		t.Fatal("ListRepoIssues deadlocked while bootstrapping the server version for SSH-signed requests")
	}

	assert.EqualValues(t, 1, versionRequests.Load())
	assert.EqualValues(t, 0, signedVersionRequests.Load())
	assert.EqualValues(t, 1, issueRequests.Load())
	assert.EqualValues(t, 1, signedIssueRequests.Load())
}

func writeTestSSHKey(t *testing.T) (string, string) {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	sshKeyPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(sshKeyPath, pemBytes, 0o600))

	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)

	return sshKeyPath, ssh.FingerprintSHA256(signer.PublicKey())
}
