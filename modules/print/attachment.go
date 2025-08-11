// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

func formatByteSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	return formatSize(size / 1024)
}

// ReleaseAttachmentsList prints a listing of release attachments
func ReleaseAttachmentsList(attachments []*gitea.Attachment, output string) {
	t := tableWithHeader(
		"Name",
		"Size",
	)

	for _, attachment := range attachments {
		t.addRow(
			attachment.Name,
			formatByteSize(attachment.Size),
		)
	}

	t.print(output)
}
