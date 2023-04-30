// Copyright 2022 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package task

import (
	"io/ioutil"
	"path/filepath"
	"strings"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/utils"
	"golang.org/x/crypto/ssh"
)

// ListSSHPubkey lists all the ssh keys in the ssh agent and the ~/.ssh/*.pub files
// It returns a list of SSH keys in the format of:
// "fingerprint keytype comment - principals: principals (ssh-agent or path to pubkey file)"
func ListSSHPubkey() []string {
	var keys []string

	keys = append(keys, getAgentKeys()...)
	keys = append(keys, getLocalKeys()...)

	return keys
}

func getAgentKeys() []string {
	ag, err := gitea.GetAgent()
	if err != nil {
		return []string{}
	}

	akeys, err := ag.List()
	if err != nil {
		return nil
	}

	var keys []string

	for _, akey := range akeys {
		if key := parseKeys([]byte(akey.String()), "ssh-agent"); key != "" {
			keys = append(keys, key)
		}
	}

	return keys
}

func getLocalKeys() []string {
	var keys []string

	// enumerate ~/.ssh/*.pub files
	glob, err := utils.AbsPathWithExpansion("~/.ssh/*.pub")
	if err != nil {
		return []string{}
	}
	localPubkeyPaths, err := filepath.Glob(glob)
	if err != nil {
		return []string{}
	}

	// parse each local key with present privkey & compare fingerprints to online keys
	for _, pubkeyPath := range localPubkeyPaths {
		var pubkeyFile []byte
		pubkeyFile, err = ioutil.ReadFile(pubkeyPath)
		if err != nil {
			continue
		}

		if key := parseKeys(pubkeyFile, pubkeyPath); key != "" {
			keys = append(keys, key)
		}
	}

	return keys
}

func parseKeys(pkinput []byte, sshPath string) string {
	pkey, comment, _, _, err := ssh.ParseAuthorizedKey(pkinput)
	if err != nil {
		return ""
	}

	if strings.Contains(pkey.Type(), "cert-v01@openssh.com") {
		principals := pkey.(*ssh.Certificate).ValidPrincipals
		return ssh.FingerprintSHA256(pkey) + " " + pkey.Type() + " " + comment +
			" - principals: " + strings.Join(principals, ",") + " (" + sshPath + ")"
	}

	return ssh.FingerprintSHA256(pkey) + " " + pkey.Type() + " " + comment + " (" + sshPath + ")"
}
