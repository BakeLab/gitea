// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// SSHKeysList prints a table of SSH public keys
func SSHKeysList(keys []*gitea.PublicKey, output string) error {
	if len(keys) == 0 {
		fmt.Printf("No SSH keys found\n")
		return nil
	}

	t := tableWithHeader(
		"ID",
		"Title",
		"Fingerprint",
		"KeyType",
		"ReadOnly",
		"Created",
	)

	for _, k := range keys {
		readOnly := "false"
		if k.ReadOnly {
			readOnly = "true"
		}
		t.addRow(
			fmt.Sprintf("%d", k.ID),
			k.Title,
			k.Fingerprint,
			k.KeyType,
			readOnly,
			FormatTime(k.Created, false),
		)
	}

	return t.print(output)
}
