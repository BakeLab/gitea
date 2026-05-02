// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	sshkeyscmd "code.gitea.io/tea/cmd/sshkeys"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"golang.org/x/crypto/ssh"
)

// generateTestPublicKey creates a fresh ed25519 keypair and returns a temp
// file path containing the public key in authorized_keys format.
func generateTestPublicKey(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	sshPub, err := ssh.NewPublicKey(priv.Public())
	require.NoError(t, err)

	pubKeyStr := fmt.Sprintf("ssh-ed25519 %s tea-test-key", base64.StdEncoding.EncodeToString(sshPub.Marshal()))

	f, err := os.CreateTemp(t.TempDir(), "test-*.pub")
	require.NoError(t, err)
	_, err = f.WriteString(pubKeyStr)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	return f.Name()
}

func sshKeysCmd() *cli.Command {
	return &cli.Command{
		Name: "ssh-keys",
		Commands: []*cli.Command{
			&sshkeyscmd.CmdSSHKeyList,
			&sshkeyscmd.CmdSSHKeyAdd,
			&sshkeyscmd.CmdSSHKeyDelete,
		},
	}
}

func TestSSHKeyAddAndDelete(t *testing.T) {
	login := createIntegrationLogin(t)
	pubKeyFile := generateTestPublicKey(t)
	keyTitle := fmt.Sprintf("tea-test-%d", time.Now().Unix())

	cmd := sshKeysCmd()
	client := login.Client()

	err := cmd.Run(context.Background(), []string{
		"ssh-keys", "add", pubKeyFile,
		"--title", keyTitle,
		"--login", login.Name,
	})
	require.NoError(t, err)

	keys, _, err := client.ListMyPublicKeys(gitea.ListPublicKeysOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	require.NoError(t, err)

	var addedKey *gitea.PublicKey
	for _, key := range keys {
		if key.Title == keyTitle {
			addedKey = key
			break
		}
	}
	require.NotNil(t, addedKey, "added key not found in key list")

	t.Cleanup(func() {
		client.DeletePublicKey(addedKey.ID) //nolint:errcheck
	})

	err = cmd.Run(context.Background(), []string{
		"ssh-keys", "delete", strconv.FormatInt(addedKey.ID, 10),
		"--confirm",
		"--login", login.Name,
	})
	assert.NoError(t, err)

	_, resp, err := client.GetPublicKey(addedKey.ID)
	assert.Error(t, err)
	if assert.NotNil(t, resp) {
		assert.Equal(t, 404, resp.StatusCode)
	}
}

func TestSSHKeyList(t *testing.T) {
	login := createIntegrationLogin(t)

	cmd := sshKeysCmd()
	err := cmd.Run(context.Background(), []string{
		"ssh-keys", "list",
		"--login", login.Name,
	})
	assert.NoError(t, err)
}
