// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	stdctx "context"
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

	"gitea.dev/tea/modules/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/ssh"
)

func TestRunIssuesListWithSSHPubkeyLoginDoesNotDeadlock(t *testing.T) {
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

	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:              "ssh-login",
			URL:               server.URL,
			SSHKey:            sshKeyPath,
			SSHKeyFingerprint: fingerprint,
			VersionCheck:      true,
			Default:           true,
		}},
	})

	cmd := cli.Command{
		Name:  CmdIssuesList.Name,
		Flags: CmdIssuesList.Flags,
	}
	require.NoError(t, cmd.Set("login", "ssh-login"))
	require.NoError(t, cmd.Set("repo", "gitea/tea"))
	require.NoError(t, cmd.Set("output", "json"))

	done := make(chan error, 1)
	go func() {
		done <- RunIssuesList(stdctx.Background(), &cmd)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("RunIssuesList deadlocked while bootstrapping the server version for HTTPSign authentication")
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
