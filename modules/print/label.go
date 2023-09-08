// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"strconv"

	"code.gitea.io/sdk/gitea"
)

// LabelsList prints a listing of labels
func LabelsList(labels []*gitea.Label, output string) {
	t := tableWithHeader(
		"Index",
		"Color",
		"Name",
		"Description",
	)

	for _, label := range labels {
		t.addRow(
			strconv.FormatInt(label.ID, 10),
			formatLabel(label, !isMachineReadable(output), label.Color),
			label.Name,
			label.Description,
		)
	}
	t.print(output)
}
