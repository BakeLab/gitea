// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"golang.org/x/crypto/ssh"
)

// findSSHKey retrieves the ssh keys registered in gitea, and tries to find
// a matching private key in ~/.ssh/. If no match is found, path is empty.
func findSSHKey(client *gitea.Client) (string, error) {
	// get keys registered on gitea instance
	var keys []*gitea.PublicKey
	for page := 1; ; {
		page_keys, resp, err := client.ListMyPublicKeys(gitea.ListPublicKeysOptions{
			ListOptions: gitea.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return "", err
		}
		keys = append(keys, page_keys...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	if len(keys) == 0 {
		return "", nil
	}

	// enumerate ~/.ssh/*.pub files
	glob, err := utils.AbsPathWithExpansion("~/.ssh/*.pub")
	if err != nil {
		return "", err
	}
	localPubkeyPaths, err := filepath.Glob(glob)
	if err != nil {
		return "", err
	}

	// parse each local key with present privkey & compare fingerprints to online keys
	for _, pubkeyPath := range localPubkeyPaths {
		var pubkeyFile []byte
		pubkeyFile, err = os.ReadFile(pubkeyPath)
		if err != nil {
			continue
		}
		fields := strings.Split(string(pubkeyFile), " ")
		if len(fields) < 2 { // first word is key type, second word is key material
			continue
		}

		var keymaterial []byte
		keymaterial, err = base64.StdEncoding.DecodeString(fields[1])
		if err != nil {
			continue
		}

		var pubkey ssh.PublicKey
		pubkey, err = ssh.ParsePublicKey(keymaterial)
		if err != nil {
			continue
		}

		privkeyPath := strings.TrimSuffix(pubkeyPath, ".pub")
		var exists bool
		exists, err = utils.FileExist(privkeyPath)
		if err != nil || !exists {
			continue
		}

		// if pubkey fingerprints match, return path to corresponding privkey.
		fingerprint := ssh.FingerprintSHA256(pubkey)
		for _, key := range keys {
			if fingerprint == key.Fingerprint {
				return privkeyPath, nil
			}
		}
	}

	return "", err
}
