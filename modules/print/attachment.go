// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"code.gitea.io/sdk/gitea"
)

// ReleaseAttachmentsList prints a listing of release attachments
func ReleaseAttachmentsList(attachments []*gitea.Attachment, output string) {
	t := tableWithHeader(
		"Name",
		"Size",
	)

	for _, attachment := range attachments {
		t.addRow(
			attachment.Name,
			formatSize(attachment.Size),
		)
	}

	t.print(output)
}
