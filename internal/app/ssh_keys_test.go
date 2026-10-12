// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestSSHKeyValidationPrecedesTargetResolution(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{"", "must hold exactly one valid SSH public key"},
		{"not a public key", "must hold exactly one valid SSH public key"},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n", "holds a private key; add its public key instead: gt ssh-key add "},
	} {
		t.Run(tc.content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "id_ed25519")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			requireUsageBeforeRequests(t, tc.want, "ssh-key", "create", path)
		})
	}
}

// testPublicKey returns a new public key in authorized_keys format with comment, and its fingerprint.
func testPublicKey(t *testing.T, comment string) (string, string) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := ssh.NewPublicKey(public)
	require.NoError(t, err)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))) + " " + comment, ssh.FingerprintSHA256(key)
}

func TestSSHKeyAddTitlesKeysByTheirComment(t *testing.T) {
	key, fingerprint := testPublicKey(t, "alice@laptop")
	var titles []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			titles = append(titles, body["title"].(string))
			if len(titles) > 1 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Key content has been used as non-deploy key"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"id":3,"title":%q,"fingerprint":%q}`, body["title"], fingerprint)
		case http.MethodGet:
			_, _ = fmt.Fprintf(w, `[{"id":3,"title":"alice@laptop","fingerprint":%q}]`, fingerprint)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Streams.In = bytes.NewBufferString(key + "\n")
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"ssh-key", "add"}))
	require.Equal(t, []string{"alice@laptop"}, titles)
	require.Equal(t, fmt.Sprintf("✓ Added SSH key \"alice@laptop\" (%s)\n", fingerprint), withoutANSI(diagnostics.String()))

	deps.Streams.In = bytes.NewBufferString(key)
	err := Execute(context.Background(), deps, []string{"ssh-key", "create", "-", "--title", "again"})
	require.EqualError(t, err, `this SSH key is already registered; it is your SSH key "alice@laptop" (ID 3)`)
}

func TestSSHKeyDeleteNamesTheKey(t *testing.T) {
	var deleted bool
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"id":2,"title":"uxkey","fingerprint":"SHA256:aR5L"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	require.Error(t, Execute(context.Background(), deps, []string{"ssh-key", "delete", "2"}))
	require.Equal(t, []string{`Delete SSH key "uxkey" (SHA256:aR5L)?`}, prompt.confirmations)
	require.False(t, deleted)
}
